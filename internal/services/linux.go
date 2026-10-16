//go:build linux

package services

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func needSudo() bool { return os.Geteuid() != 0 }

// LogDir is where services keep output their manager doesn't (OpenRC).
func LogDir(string) string { return "/var/log/quai-node" }

// Detect picks the running init system: systemd when /run/systemd/system
// exists (sd_booted's test), OpenRC when /run/openrc does.
func Detect(r *Runner) (Manager, error) {
	if fi, err := os.Stat("/run/systemd/system"); err == nil && fi.IsDir() {
		return &systemd{r}, nil
	}
	if fi, err := os.Stat("/run/openrc"); err == nil && fi.IsDir() {
		return &openrc{r}, nil
	}
	return nil, errors.New("no supported init system found (systemd or OpenRC); use `quai-node run` instead")
}

// ForceKind picks a manager by name, for --init.
func ForceKind(r *Runner, kind string) (Manager, error) {
	switch kind {
	case "systemd":
		return &systemd{r}, nil
	case "openrc":
		return &openrc{r}, nil
	}
	return nil, fmt.Errorf("--init %q: expected systemd or openrc", kind)
}

// ---------------------------------------------------------------- systemd

type systemd struct{ r *Runner }

func (s *systemd) Kind() string { return "systemd" }

func (s *systemd) unitPath(name string) string { return "/etc/systemd/system/" + name + ".service" }

func (s *systemd) Render(sp Spec) (string, string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `# Installed by quai-node; reinstalled by 'quai-node service install'.
[Unit]
Description=%s
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=10
TimeoutStopSec=%d
# SIGINT to quai-node alone, which stops go-quai and quai-dash in order;
# SIGKILL to all of them only if that runs past the timeout.
KillMode=mixed
KillSignal=SIGINT
LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`, sp.Description, sp.User, systemdQuote(sp.Dir), systemdCmd(sp.Exec, sp.Args), sp.StopTimeout)
	return s.unitPath(sp.Name), b.String(), nil
}

func (s *systemd) Install(sp Spec) error {
	path, unit, err := s.Render(sp)
	if err != nil {
		return err
	}
	if err := s.r.WriteFile(true, path, unit, 0o644); err != nil {
		return err
	}
	if err := s.r.Run(true, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := s.r.Run(true, "systemctl", "enable", sp.Name+".service"); err != nil {
		return err
	}
	return s.r.Run(true, "systemctl", "restart", sp.Name+".service")
}

func (s *systemd) Uninstall(name string) error {
	if _, err := os.Stat(s.unitPath(name)); err != nil {
		return nil
	}
	_ = s.r.Run(true, "systemctl", "disable", "--now", name+".service")
	if err := s.r.Run(true, "rm", "-f", s.unitPath(name)); err != nil {
		return err
	}
	return s.r.Run(true, "systemctl", "daemon-reload")
}

func (s *systemd) Start(name string) error {
	return s.r.Run(true, "systemctl", "start", name+".service")
}
func (s *systemd) Stop(name string) error { return s.r.Run(true, "systemctl", "stop", name+".service") }

func (s *systemd) Definition(name string) (string, error) { return readDef(s.unitPath(name)) }

func (s *systemd) Status(name string) (Status, error) {
	if _, err := os.Stat(s.unitPath(name)); err != nil {
		return NotInstalled, nil
	}
	out, _ := s.r.Output("systemctl", "is-active", name+".service")
	switch strings.TrimSpace(out) {
	case "active", "activating", "reloading":
		return Running, nil
	case "inactive", "failed", "deactivating":
		return Stopped, nil
	}
	return Unknown, nil
}

// systemdQuote double-quotes s for a unit file when it holds a space.
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func systemdCmd(exe string, args []string) string {
	parts := []string{systemdQuote(exe)}
	for _, a := range args {
		parts = append(parts, systemdQuote(a))
	}
	return strings.Join(parts, " ")
}

// ----------------------------------------------------------------- OpenRC

type openrc struct{ r *Runner }

func (o *openrc) Kind() string { return "openrc" }

func (o *openrc) scriptPath(name string) string { return "/etc/init.d/" + name }

func (o *openrc) Render(sp Spec) (string, string, error) {
	for _, s := range append([]string{sp.Exec, sp.Dir, sp.LogFile}, sp.Args...) {
		if strings.ContainsAny(s, `'"$`+"`\\\n") {
			return "", "", fmt.Errorf("OpenRC: path %q holds a quote, $, backquote or backslash; pick a home without one", s)
		}
	}
	args := make([]string, len(sp.Args))
	for i, a := range sp.Args {
		args[i] = "'" + a + "'"
	}
	logDir := sp.LogFile[:strings.LastIndex(sp.LogFile, "/")]
	var b strings.Builder
	fmt.Fprintf(&b, `#!/sbin/openrc-run
# Installed by quai-node; reinstalled by 'quai-node service install'.

description="%s"

command="%s"
command_args="%s"
command_user="%s"
directory="%s"
supervisor="supervise-daemon"
respawn_delay=10
respawn_max=5
respawn_period=600
output_log="%s"
error_log="%s"
# Stop with SIGINT, as Ctrl-C would; SIGKILL if still running after %ds.
retry="INT/%d/KILL/5"

depend() {
	need net
}

start_pre() {
	checkpath --directory --owner "${command_user}" --mode 0750 "%s"
}
`, sp.Description, sp.Exec, strings.Join(args, " "), sp.User, sp.Dir, sp.LogFile, sp.LogFile,
		sp.StopTimeout, sp.StopTimeout, logDir)
	return o.scriptPath(sp.Name), b.String(), nil
}

func (o *openrc) Install(sp Spec) error {
	path, script, err := o.Render(sp)
	if err != nil {
		return err
	}
	if err := o.r.WriteFile(true, path, script, 0o755); err != nil {
		return err
	}
	if err := o.r.Run(true, "rc-update", "add", sp.Name, "default"); err != nil {
		return err
	}
	return o.r.Run(true, "rc-service", sp.Name, "restart")
}

func (o *openrc) Uninstall(name string) error {
	if _, err := os.Stat(o.scriptPath(name)); err != nil {
		return nil
	}
	_ = o.r.Run(true, "rc-service", name, "stop")
	_ = o.r.Run(true, "rc-update", "del", name, "default")
	return o.r.Run(true, "rm", "-f", o.scriptPath(name))
}

func (o *openrc) Start(name string) error { return o.r.Run(true, "rc-service", name, "start") }
func (o *openrc) Stop(name string) error  { return o.r.Run(true, "rc-service", name, "stop") }

func (o *openrc) Definition(name string) (string, error) { return readDef(o.scriptPath(name)) }

func (o *openrc) Status(name string) (Status, error) {
	if _, err := os.Stat(o.scriptPath(name)); err != nil {
		return NotInstalled, nil
	}
	// rc-service status: 0 started, 3 stopped, others crashed/unknown.
	_, err := o.r.Output("rc-service", name, "status")
	var ee *exec.ExitError
	switch {
	case err == nil:
		return Running, nil
	case errors.As(err, &ee) && ee.ExitCode() == 3:
		return Stopped, nil
	}
	return Unknown, nil
}

// readDef is a service file's contents, "" when it doesn't exist.
func readDef(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}
