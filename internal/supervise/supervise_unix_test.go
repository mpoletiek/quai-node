//go:build !windows

package supervise

import (
	"bytes"
	"context"
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
