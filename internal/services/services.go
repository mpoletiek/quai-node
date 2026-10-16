// Package services installs the node as a service of the host's service
// manager: systemd or OpenRC on Linux, launchd on macOS, the Service Control
// Manager on Windows.
//
// There is one service, quai-node, so one start or stop covers everything.
// It runs `quai-node run`, which supervises go-quai and, when set up,
// quai-dash (see package supervise).
package services

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mpoletiek/quai-node/internal/home"
)

// Name is the service's name. Legacy are the separate services earlier
// quai-node builds installed; setup and uninstall remove them.
const Name = "quai-node"

var Legacy = []string{"quai-dash", "go-quai"}

// Child names, for the processes the service supervises.
const (
	Node = "go-quai"
	Dash = "quai-dash"
)

// Spec is the service, or one process it supervises.
type Spec struct {
	Name        string
	Description string
	Exec        string   // absolute path
	Args        []string // arguments after Exec
	Dir         string   // working directory
	User        string   // Linux: user it runs as
	LogFile     string   // where its output goes, where the manager doesn't keep it
	StopTimeout int      // seconds to wait after the stop signal

	// For supervised processes:
	Critical    bool // its exit stops the service (else it is restarted alone)
	QuietStdout bool // drop stdout: go-quai copies its whole log there
}

// Status of an installed service.
type Status string

const (
	Running      Status = "running"
	Stopped      Status = "stopped"
	NotInstalled Status = "not installed"
	Unknown      Status = "unknown"
)

// Manager is the host's service manager.
type Manager interface {
	// Kind names it: systemd, openrc, launchd or windows.
	Kind() string
	// Render returns the file Install writes for spec and its path; empty
	// for managers that keep services in a registry (Windows).
	Render(spec Spec) (path, content string, err error)
	// Install writes, enables (start at boot or login) and starts spec,
	// replacing an existing service of the same name.
	Install(spec Spec) error
	Uninstall(name string) error
	Start(name string) error
	Stop(name string) error
	Status(name string) (Status, error)
	// Definition is the installed service's definition (its unit file,
	// script or plist, or on Windows its command line), "" when none is
	// installed. Service names are machine-wide, so callers check it to
	// act only on a service that belongs to their home: see Owns.
	Definition(name string) (string, error)
}

// Owns reports whether the installed service name runs a program from home
// h: the quai-node service runs h's quai-node; the legacy go-quai and
// quai-dash services ran h's go-quai and quai-dash. installed is false
// when there is no such service.
func Owns(m Manager, name string, h home.Home) (installed, ours bool, err error) {
	def, err := m.Definition(name)
	if err != nil || def == "" {
		return false, false, err
	}
	prog := h.QuaiNode()
	switch name {
	case Node:
		prog = h.GoQuai()
	case Dash:
		prog = h.QuaiDash()
	}
	return true, containsPath(def, prog), nil
}

// OwnerOf is the program an installed service definition runs, best
// effort, for messages: the first absolute path to a quai-node, go-quai
// or quai-dash binary in it.
func OwnerOf(def string) string {
	for _, f := range strings.FieldsFunc(def, func(r rune) bool { return strings.ContainsRune(" \t\n\"'<>=", r) }) {
		b := strings.ToLower(filepath.Base(f))
		if filepath.IsAbs(f) && (strings.HasPrefix(b, "quai-node") || strings.HasPrefix(b, "go-quai") || strings.HasPrefix(b, "quai-dash")) {
			return f
		}
	}
	return "an unknown program"
}

// Service is the quai-node service for a home. Its output (quai-node's own
// lines, quai-dash's, and go-quai's stderr, where a crash would show) goes
// to the manager's log or LogFile; go-quai's full log is in nodelogs/.
func Service(h home.Home, user, logDir string) Spec {
	return Spec{
		Name:        Name,
		Description: "Quai Network node (go-quai and quai-dash), managed by quai-node",
		Exec:        h.QuaiNode(),
		Args:        []string{"run", "--service", "--home", h.Dir},
		Dir:         h.Dir,
		User:        user,
		LogFile:     filepath.Join(logDir, Name+".log"),
		// The node's own allowance, and a margin for quai-node to wind up.
		StopTimeout: nodeStopTimeout + 30,
	}
}

// go-quai flushes its databases on the way out.
const nodeStopTimeout = 300

// Children are the processes the service runs: go-quai, and quai-dash when
// the dashboard is on. logDir holds quai-dash's output in a foreground run.
func Children(h home.Home, dashboard bool, logDir string) []Spec {
	out := []Spec{{
		Name:        Node,
		Exec:        h.GoQuai(),
		Args:        []string{"start", "--global.config-dir", h.ConfigDir()},
		Dir:         h.Dir,
		StopTimeout: nodeStopTimeout,
		Critical:    true,
		QuietStdout: true,
	}}
	if dashboard {
		out = append(out, Spec{
			Name:        Dash,
			Exec:        h.QuaiDash(),
			Args:        []string{"--config", h.DashConfig(), "web"},
			Dir:         h.Dir,
			LogFile:     filepath.Join(logDir, Dash+".log"),
			StopTimeout: 10,
		})
	}
	return out
}

// Runner runs the commands and writes the files that installing a service
// takes, printing each first. On Linux it uses sudo for what needs root
// unless it is root already; with DryRun it only prints.
type Runner struct {
	Out    io.Writer
	DryRun bool
	Sudo   bool // prefix privileged commands with sudo
}

// NewRunner is a Runner for this process.
func NewRunner(out io.Writer, dryRun bool) *Runner {
	return &Runner{Out: out, DryRun: dryRun, Sudo: needSudo()}
}

func (r *Runner) say(format string, a ...any) {
	fmt.Fprintf(r.Out, format+"\n", a...)
}

// Run runs argv, privileged or not.
func (r *Runner) Run(privileged bool, argv ...string) error {
	if privileged && r.Sudo {
		argv = append([]string{"sudo"}, argv...)
	}
	r.say("  $ %s", shellJoin(argv))
	if r.DryRun {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, r.Out, r.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", shellJoin(argv), err)
	}
	return nil
}

// Output runs argv quietly and returns its output and exit error.
func (r *Runner) Output(argv ...string) (string, error) {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// WriteFile writes content to path, through sudo install when privileged.
func (r *Runner) WriteFile(privileged bool, path, content string, mode os.FileMode) error {
	r.say("  write %s", path)
	if r.DryRun {
		for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
			r.say("  | %s", l)
		}
		return nil
	}
	if !privileged || !r.Sudo {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), mode)
	}
	tmp, err := os.CreateTemp("", "quai-node-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	argv := []string{"sudo", "install", "-D", "-m", fmt.Sprintf("%o", mode), "-o", "root", "-g", "root", tmp.Name(), path}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, r.Out, r.Out
	return cmd.Run()
}

// shellJoin quotes argv for display.
func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shQuote(a)
	}
	return strings.Join(q, " ")
}

// shQuote single-quotes s for sh when it needs quoting.
func shQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LogNote says where the logs are, for a service of kind ("" when the node
// runs in the foreground), one indented line each.
func LogNote(kind string, h home.Home) string {
	lines := []string{
		"  Node:     " + h.NodeLogs() + string(filepath.Separator) + "  (go-quai's logs, one per chain, rotated by go-quai)",
	}
	switch kind {
	case "systemd":
		lines = append(lines, "  Service:  journalctl -u "+Name+"  (quai-node's and the dashboard's output, node crashes)")
	case "openrc":
		lines = append(lines, "  Service:  "+filepath.Join(LogDir(h.Dir), Name+".log")+"  (written by OpenRC as root into /var/log;",
			"            quai-node's and the dashboard's output, node crashes)")
	case "launchd", "windows":
		lines = append(lines, "  Service:  "+filepath.Join(LogDir(h.Dir), Name+".log")+"  (quai-node's and the dashboard's output, node crashes)")
	case "":
		lines = append(lines, "  Dashboard: "+filepath.Join(h.Logs(), "quai-dash.log"))
	}
	return strings.Join(lines, "\n")
}
