//go:build darwin

package services

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// On macOS the services are LaunchAgents of the user who ran setup: they
// start at login and need no admin rights.

func needSudo() bool { return false }

// LogDir is where launchd sends the services' output.
func LogDir(homeDir string) string { return filepath.Join(homeDir, "logs") }

func Detect(r *Runner) (Manager, error) { return &launchd{r}, nil }

func ForceKind(r *Runner, kind string) (Manager, error) {
	if kind != "launchd" {
		return nil, fmt.Errorf("--init %q: macOS has launchd only", kind)
	}
	return &launchd{r}, nil
}

type launchd struct{ r *Runner }

func (l *launchd) Kind() string { return "launchd" }

const labelPrefix = "io.github.mpoletiek.quai-node."

func label(name string) string { return labelPrefix + name }

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func (l *launchd) plistPath(name string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", label(name)+".plist")
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Render writes the agent's plist. ProcessType is Standard, set outright:
// Background throttles the node's CPU and disk I/O as macOS does for
// background tasks (and leaving the key out still applies light limits).
// Under that, two Macs' nodes stalled for good at the snapshot head, on a
// deadlock in go-quai's zone that a slow process makes likelier.
func (l *launchd) Render(sp Spec) (string, string, error) {
	var args strings.Builder
	for _, a := range append([]string{sp.Exec}, sp.Args...) {
		fmt.Fprintf(&args, "\t\t<string>%s</string>\n", esc(a))
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Installed by quai-node; reinstalled by 'quai-node service install'. -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ExitTimeOut</key>
	<integer>%d</integer>
	<key>ProcessType</key>
	<string>Standard</string>
	<key>SoftResourceLimits</key>
	<dict>
		<key>NumberOfFiles</key>
		<integer>65536</integer>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, esc(label(sp.Name)), args.String(), esc(sp.Dir), sp.StopTimeout, esc(sp.LogFile), esc(sp.LogFile))
	return l.plistPath(sp.Name), plist, nil
}

func (l *launchd) Install(sp Spec) error {
	path, plist, err := l.Render(sp)
	if err != nil {
		return err
	}
	if !l.r.DryRun {
		if err := os.MkdirAll(filepath.Dir(sp.LogFile), 0o755); err != nil {
			return err
		}
	}
	_ = l.Uninstall(sp.Name)
	if err := l.r.WriteFile(false, path, plist, 0o644); err != nil {
		return err
	}
	return l.r.Run(false, "launchctl", "bootstrap", domain(), path)
}

func (l *launchd) Uninstall(name string) error {
	path := l.plistPath(name)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	_ = l.r.Run(false, "launchctl", "bootout", domain()+"/"+label(name))
	if l.r.DryRun {
		l.r.say("  rm %s", path)
		return nil
	}
	return os.Remove(path)
}

// Start loads the agent; Stop unloads it until the next login or Start.
func (l *launchd) Start(name string) error {
	return l.r.Run(false, "launchctl", "bootstrap", domain(), l.plistPath(name))
}

func (l *launchd) Stop(name string) error {
	return l.r.Run(false, "launchctl", "bootout", domain()+"/"+label(name))
}

func (l *launchd) Definition(name string) (string, error) {
	b, err := os.ReadFile(l.plistPath(name))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(b), err
}

func (l *launchd) Status(name string) (Status, error) {
	if _, err := os.Stat(l.plistPath(name)); err != nil {
		return NotInstalled, nil
	}
	out, err := l.r.Output("launchctl", "print", domain()+"/"+label(name))
	if err != nil {
		return Stopped, nil // not loaded
	}
	if strings.Contains(out, "state = running") {
		return Running, nil
	}
	return Stopped, nil
}
