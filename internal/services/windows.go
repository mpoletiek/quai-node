//go:build windows

package services

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// On Windows the service is an SCM service running as LocalSystem. Its
// program is quai-node itself (`quai-node run --service quai-node`), which
// speaks the SCM protocol and supervises go-quai and quai-dash.

func needSudo() bool { return false }

// LogDir is where the supervisor writes the services' output.
func LogDir(homeDir string) string { return filepath.Join(homeDir, "logs") }

// Elevated reports whether this process can install services.
func Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func Detect(r *Runner) (Manager, error) {
	if !Elevated() {
		return nil, errors.New("installing services needs an administrator: run quai-node from an elevated terminal (right-click, Run as administrator)")
	}
	return &scm{r}, nil
}

func ForceKind(r *Runner, kind string) (Manager, error) {
	if kind != "windows" {
		return nil, fmt.Errorf("--init %q: Windows has its service manager only", kind)
	}
	return Detect(r)
}

type scm struct{ r *Runner }

func (s *scm) Kind() string { return "windows" }

// Config is the kardianos config for spec; `quai-node run --service`
// builds the same one to run under the SCM.
func Config(sp Spec) *service.Config {
	return &service.Config{
		Name:        sp.Name,
		DisplayName: sp.Description,
		Description: sp.Description,
		Executable:  sp.Exec,
		Arguments:   sp.Args,
		Option: service.KeyValue{
			"StartType":              "automatic",
			"DelayedAutoStart":       true,
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "10s",
			"OnFailureResetPeriod":   600,
		},
	}
}

func (s *scm) svc(sp Spec) (service.Service, error) {
	return service.New(nil, Config(sp))
}

func (s *scm) byName(name string) (service.Service, error) {
	return service.New(nil, &service.Config{Name: name})
}

func (s *scm) Render(sp Spec) (string, string, error) {
	c := Config(sp)
	return "", fmt.Sprintf("Windows service %q: %s %s (automatic, delayed start; restarts on failure)",
		c.Name, c.Executable, strings.Join(c.Arguments, " ")), nil
}

func (s *scm) Install(sp Spec) error {
	_, desc, _ := s.Render(sp)
	s.r.say("  install %s", desc)
	if s.r.DryRun {
		return nil
	}
	_ = s.Uninstall(sp.Name)
	v, err := s.svc(sp)
	if err != nil {
		return err
	}
	if err := v.Install(); err != nil {
		return err
	}
	return v.Start()
}

func (s *scm) Uninstall(name string) error {
	st, _ := s.Status(name)
	if st == NotInstalled {
		return nil
	}
	s.r.say("  remove Windows service %q", name)
	if s.r.DryRun {
		return nil
	}
	v, err := s.byName(name)
	if err != nil {
		return err
	}
	_ = v.Stop()
	return v.Uninstall()
}

func (s *scm) Start(name string) error {
	s.r.say("  start Windows service %q", name)
	if s.r.DryRun {
		return nil
	}
	v, err := s.byName(name)
	if err != nil {
		return err
	}
	return v.Start()
}

func (s *scm) Stop(name string) error {
	s.r.say("  stop Windows service %q", name)
	if s.r.DryRun {
		return nil
	}
	v, err := s.byName(name)
	if err != nil {
		return err
	}
	return v.Stop()
}

// Definition is the service's command line (its ImagePath).
func (s *scm) Definition(name string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE)
	if err != nil {
		return "", nil // not installed
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ImagePath")
	if err != nil {
		return "", nil
	}
	return v, nil
}

func (s *scm) Status(name string) (Status, error) {
	v, err := s.byName(name)
	if err != nil {
		return Unknown, err
	}
	st, err := v.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		return NotInstalled, nil
	case err != nil:
		return Unknown, err
	case st == service.StatusRunning:
		return Running, nil
	case st == service.StatusStopped:
		return Stopped, nil
	}
	return Unknown, nil
}
