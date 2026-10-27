// Package supervise runs go-quai and quai-dash as child processes, for the
// quai-node service and for `quai-node run` in a terminal.
//
// The node is critical: when it exits, Run stops the dashboard and returns
// an error, and the service manager restarts the whole service. The
// dashboard is not: when it exits it is started again on its own, after a
// delay that grows while it keeps failing, and the node runs on.
package supervise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mpoletiek/quai-node/internal/services"
)

// Options say where output goes.
type Options struct {
	// Out gets quai-node's own lines and, in a service, the children's
	// output (but go-quai's stdout, a copy of nodelogs/global.log).
	Out io.Writer
	// Service is true under a service manager. In a terminal go-quai's
	// output goes to Out and quai-dash's to its LogFile.
	Service bool
	// Alongside, if set, runs while the processes do (it gets Out), and
	// its context ends when Run stops them.
	Alongside func(ctx context.Context, out io.Writer)
}

// Restart delays for a non-critical process; variables for tests.
var (
	firstDelay = 10 * time.Second
	maxDelay   = 5 * time.Minute
	healthy    = 10 * time.Minute // a run this long resets the delay
	pollEvery  = time.Second      // how often waitPrevious looks again
)

type proc struct {
	spec services.Spec
	opts Options

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed when cmd has been waited for
	err     error         // cmd's exit, valid once done is closed
	stopped bool          // no more starts
	logFile *os.File
}

// Run starts the specs and supervises them until ctx ends or a critical
// one exits, then stops the rest, last started first.
func Run(ctx context.Context, specs []services.Spec, o Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fatal := make(chan error, len(specs))
	var procs []*proc
	var wg sync.WaitGroup
	if o.Alongside != nil {
		go o.Alongside(ctx, o.Out)
	}

	stopAll := func() {
		cancel()
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].stop()
		}
		wg.Wait()
		for _, p := range procs {
			if p.logFile != nil {
				p.logFile.Close()
			}
		}
	}

	for _, sp := range specs {
		p := &proc{spec: sp, opts: o}
		if err := p.start(ctx); err != nil {
			if sp.Critical {
				stopAll()
				return err
			}
			fmt.Fprintf(o.Out, "quai-node: %v\n", err)
		}
		procs = append(procs, p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.watch(ctx, fatal)
		}()
	}

	var err error
	select {
	case <-ctx.Done():
		fmt.Fprintln(o.Out, "quai-node: stopping")
	case err = <-fatal:
		fmt.Fprintf(o.Out, "quai-node: %v; stopping\n", err)
	}
	stopAll()
	return err
}

// start starts the process unless stop was called or ctx ends while the
// previous run is still exiting.
func (p *proc) start(ctx context.Context) error {
	p.waitPrevious(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || ctx.Err() != nil {
		return nil
	}
	cmd := exec.Command(p.spec.Exec, p.spec.Args...)
	cmd.Dir = p.spec.Dir
	out, err := p.output()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = out, out
	if p.opts.Service && p.spec.QuietStdout {
		cmd.Stdout = io.Discard
	}
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.spec.Name, err)
	}
	fmt.Fprintf(p.opts.Out, "quai-node: started %s (pid %d)\n", p.spec.Name, cmd.Process.Pid)
	p.writePid(cmd.Process.Pid)
	done := make(chan struct{})
	p.cmd, p.done = cmd, done
	go func() {
		err := cmd.Wait()
		p.clearPid(cmd.Process.Pid)
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(done)
	}()
	return nil
}

// waitPrevious waits for the previous run of the program in this home, as
// its pid file records, to exit. A service restart can start quai-node again
// while the go-quai it started before is still closing its databases
// (launchd doesn't wait for it), and the new go-quai then dies on the old
// one's database locks. A previous run still going after StopTimeout is
// killed, as its own supervisor would have.
func (p *proc) waitPrevious(ctx context.Context) {
	if p.spec.PidFile == "" {
		return
	}
	b, err := os.ReadFile(p.spec.PidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || !running(pid, p.spec.Exec) {
		return
	}
	fmt.Fprintf(p.opts.Out, "quai-node: waiting for the previous %s (pid %d) to exit\n", p.spec.Name, pid)
	deadline := time.Now().Add(time.Duration(p.spec.StopTimeout) * time.Second)
	killed := false
	for running(pid, p.spec.Exec) {
		if time.Now().After(deadline) {
			if killed {
				fmt.Fprintf(p.opts.Out, "quai-node: the previous %s (pid %d) won't exit; starting anyway\n", p.spec.Name, pid)
				return
			}
			fmt.Fprintf(p.opts.Out, "quai-node: the previous %s (pid %d) is still running after %ds; killing it\n",
				p.spec.Name, pid, p.spec.StopTimeout)
			kill(pid)
			killed, deadline = true, time.Now().Add(10*time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollEvery):
		}
	}
}

// writePid records the run's pid; a failure only costs the next start its
// wait, so it is reported and otherwise ignored.
func (p *proc) writePid(pid int) {
	if p.spec.PidFile == "" {
		return
	}
	err := os.MkdirAll(filepath.Dir(p.spec.PidFile), 0o755)
	if err == nil {
		err = os.WriteFile(p.spec.PidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	}
	if err != nil {
		fmt.Fprintf(p.opts.Out, "quai-node: %s's pid file: %v\n", p.spec.Name, err)
	}
}

// clearPid removes the pid file if it is still this run's.
func (p *proc) clearPid(pid int) {
	if p.spec.PidFile == "" {
		return
	}
	if b, err := os.ReadFile(p.spec.PidFile); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(pid) {
		_ = os.Remove(p.spec.PidFile)
	}
}

// output is where the process writes: Out, or in a terminal its LogFile
// when it has one (quai-dash's lines would interleave with go-quai's).
func (p *proc) output() (io.Writer, error) {
	if p.opts.Service || p.spec.LogFile == "" {
		return p.opts.Out, nil
	}
	if p.logFile == nil {
		if err := os.MkdirAll(filepath.Dir(p.spec.LogFile), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(p.spec.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		p.logFile = f
	}
	return p.logFile, nil
}

// watch waits for each run of the process to end; a critical process's end
// is reported to fatal, another's is followed by a restart.
func (p *proc) watch(ctx context.Context, fatal chan<- error) {
	delay := firstDelay
	for {
		p.mu.Lock()
		done, began := p.done, time.Now()
		p.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				return
			}
			p.mu.Lock()
			err := p.err
			p.mu.Unlock()
			if err == nil {
				err = errors.New("exited")
			}
			if p.spec.Critical {
				fatal <- fmt.Errorf("%s stopped: %w", p.spec.Name, err)
				return
			}
			if time.Since(began) > healthy {
				delay = firstDelay
			}
			fmt.Fprintf(p.opts.Out, "quai-node: %s stopped (%v); restarting it in %s\n", p.spec.Name, err, delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if err := p.start(ctx); err != nil {
			fmt.Fprintf(p.opts.Out, "quai-node: %v\n", err)
		}
		delay = min(delay*2, maxDelay)
	}
}

// stop prevents restarts, interrupts the process and kills it if it hasn't
// exited within its StopTimeout.
func (p *proc) stop() {
	p.mu.Lock()
	p.stopped = true
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	if err := interrupt(cmd); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(time.Duration(p.spec.StopTimeout) * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
