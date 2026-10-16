//go:build !windows

package onpath

import (
	"fmt"
	"os"
	"path/filepath"
)

// linkPath is ~/.local/bin/<name of target>: the per-user bin directory
// of the XDG base directory spec, on PATH on most Linux desktops.
func linkPath(target string) (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "bin", filepath.Base(target)), nil
}

// Add links ~/.local/bin/<name> to target. It leaves alone a file of that
// name there that isn't a link.
func Add(target string) (Result, error) {
	link, err := linkPath(target)
	if err != nil {
		return Result{}, err
	}
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return Result{}, fmt.Errorf("%s exists and isn't a link; remove it to put %s there", link, filepath.Base(target))
		}
		if cur, _ := os.Readlink(link); cur == target {
			return result(link), nil
		}
		if err := os.Remove(link); err != nil {
			return Result{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.Symlink(target, link); err != nil {
		return Result{}, err
	}
	return result(link), nil
}

func result(link string) Result {
	r := Result{Path: link}
	if !inPath(filepath.Dir(link)) {
		r.Note = fmt.Sprintf("%s isn't on your PATH. Add it in your shell's profile (~/.zshrc, ~/.bashrc, …):\n  export PATH=\"$HOME/.local/bin:$PATH\"", filepath.Dir(link))
	}
	return r
}

// Remove deletes the link if it points at target.
func Remove(target string) error {
	link, err := linkPath(target)
	if err != nil {
		return err
	}
	if cur, err := os.Readlink(link); err == nil && trim(cur) == target {
		return os.Remove(link)
	}
	return nil
}

// dashConfigPath is where quai-dash looks for its config first:
// $XDG_CONFIG_HOME/quai-dash/config.toml, else ~/.config/quai-dash/config.toml.
func dashConfigPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "quai-dash", "config.toml"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "quai-dash", "config.toml"), nil
}

// LinkDashConfig links quai-dash's default config to target, so a plain
// `quai-dash tui` watches this node. A config of the user's own there is
// left alone; ok is false then.
func LinkDashConfig(target string) (path string, ok bool, err error) {
	path, err = dashConfigPath()
	if err != nil {
		return "", false, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return path, false, nil // the user's own
		}
		if cur, _ := os.Readlink(path); cur == target {
			return path, true, nil
		}
		if err := os.Remove(path); err != nil {
			return path, false, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	return path, true, os.Symlink(target, path)
}

// UnlinkDashConfig removes that link if it points at target.
func UnlinkDashConfig(target string) error {
	path, err := dashConfigPath()
	if err != nil {
		return err
	}
	if cur, err := os.Readlink(path); err == nil && cur == target {
		return os.Remove(path)
	}
	return nil
}
