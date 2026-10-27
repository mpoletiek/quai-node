//go:build !windows

package supervise

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mpoletiek/quai-node/internal/services"
)

// syncBuf is a bytes.Buffer safe for the supervisor's goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func sh(name, script string, critical bool) services.Spec {
	return services.Spec{Name: name, Exec: "/bin/sh", Args: []string{"-c", script}, StopTimeout: 2, Critical: critical}
}

func TestDashboardRestartsAlone(t *testing.T) {
	firstDelay, maxDelay = 50*time.Millisecond, 100*time.Millisecond
	out := &syncBuf{}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	// The node runs until interrupted; the dashboard dies at once, again and again.
	err := Run(ctx, []services.Spec{
		sh("node", "trap 'exit 0' INT; while :; do sleep 0.05; done", true),
		sh("dash", "echo dash-ran; exit 3", false),
	}, Options{Out: out, Service: true})
	if err != nil {
		t.Fatalf("Run = %v, want nil after ctx ends", err)
	}
	if n := strings.Count(out.String(), "dash-ran"); n < 3 {
		t.Errorf("dash ran %d times, want it restarted repeatedly:\n%s", n, out)
	}
	if strings.Contains(out.String(), "node stopped") {
		t.Errorf("node was stopped by the dashboard's exits:\n%s", out)
	}
}

func TestNodeExitStopsAll(t *testing.T) {
	out := &syncBuf{}
	start := time.Now()
	err := Run(context.Background(), []services.Spec{
		sh("node", "sleep 0.2; exit 7", true),
		sh("dash", "trap 'echo dash-interrupted; exit 0' INT; while :; do sleep 0.05; done", false),
	}, Options{Out: out, Service: true})
	if err == nil || !strings.Contains(err.Error(), "node stopped") {
		t.Fatalf("Run = %v, want the node's exit", err)
	}
	if !strings.Contains(out.String(), "dash-interrupted") {
		t.Errorf("dash wasn't interrupted:\n%s", out)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s; dash should stop on the interrupt, not the kill", d)
	}
}

func TestQuietStdout(t *testing.T) {
	out := &syncBuf{}
	node := sh("node", "echo to-stdout; echo to-stderr >&2; exit 0", true)
	node.QuietStdout = true
	_ = Run(context.Background(), []services.Spec{node}, Options{Out: out, Service: true})
	if strings.Contains(out.String(), "to-stdout") || !strings.Contains(out.String(), "to-stderr") {
		t.Errorf("want stderr kept and stdout dropped:\n%s", out)
	}
}

// previous starts a run of /bin/sh left over from an earlier quai-node and
// records its pid where node's spec looks.
func previous(t *testing.T, script string) (services.Spec, *exec.Cmd) {
	t.Helper()
	pollEvery = 20 * time.Millisecond
	cmd := exec.Command("/bin/sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	node := sh("node", "exit 0", true)
	node.PidFile = filepath.Join(t.TempDir(), "run", "node.pid")
	os.MkdirAll(filepath.Dir(node.PidFile), 0o755)
	os.WriteFile(node.PidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	return node, cmd
}

func TestStartWaitsForPreviousRun(t *testing.T) {
	node, _ := previous(t, "sleep 0.5; exit 0")
	out := &syncBuf{}
	start := time.Now()
	_ = Run(context.Background(), []services.Spec{node}, Options{Out: out, Service: true})
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Errorf("started after %s, before the previous run exited:\n%s", d, out)
	}
	if !strings.Contains(out.String(), "waiting for the previous node") {
		t.Errorf("no word of the wait:\n%s", out)
	}
	if _, err := os.Stat(node.PidFile); err == nil {
		t.Error("pid file left behind after the run exited")
	}
}

func TestStartKillsPreviousRunAfterStopTimeout(t *testing.T) {
	node, _ := previous(t, "sleep 30; exit 0")
	node.StopTimeout = 0
	out := &syncBuf{}
	start := time.Now()
	_ = Run(context.Background(), []services.Spec{node}, Options{Out: out, Service: true})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s; the previous run should have been killed", d)
	}
	if !strings.Contains(out.String(), "killing it") {
		t.Errorf("previous run not killed:\n%s", out)
	}
}

func TestStartIgnoresAnotherProgramsPid(t *testing.T) {
	node, _ := previous(t, "sleep 30; exit 0")
	node.Exec = "/no/such/go-quai" // the pid now runs some other program
	node.Args = nil
	out := &syncBuf{}
	start := time.Now()
	_ = Run(context.Background(), []services.Spec{node}, Options{Out: out, Service: true})
	if d := time.Since(start); d > 2*time.Second || strings.Contains(out.String(), "waiting") {
		t.Errorf("waited for a process that isn't the node (%s):\n%s", d, out)
	}
}

func TestPidFileHoldsTheRunningPid(t *testing.T) {
	node := sh("node", "trap 'exit 0' INT; while :; do sleep 0.05; done", true)
	node.PidFile = filepath.Join(t.TempDir(), "run", "node.pid")
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuf{}
	done := make(chan struct{})
	go func() { _ = Run(ctx, []services.Spec{node}, Options{Out: out, Service: true}); close(done) }()
	var b []byte
	for i := 0; i < 100 && len(b) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		b, _ = os.ReadFile(node.PidFile)
	}
	if !strings.Contains(out.String(), "(pid "+strings.TrimSpace(string(b))+")") {
		t.Errorf("pid file %q doesn't match the started pid:\n%s", b, out)
	}
	cancel()
	<-done
	if _, err := os.Stat(node.PidFile); err == nil {
		t.Error("pid file left behind after stopping")
	}
}
