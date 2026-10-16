//go:build !linux && !darwin && !windows

package services

import "errors"

func needSudo() bool { return false }

// LogDir is unused where services aren't supported.
func LogDir(homeDir string) string { return homeDir + "/logs" }

var errUnsupported = errors.New("services aren't supported on this OS; use `quai-node run`")

func Detect(*Runner) (Manager, error)            { return nil, errUnsupported }
func ForceKind(*Runner, string) (Manager, error) { return nil, errUnsupported }
