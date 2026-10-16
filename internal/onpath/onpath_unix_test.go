//go:build !windows

package onpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRemove(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("PATH", "/usr/bin")
	target := filepath.Join(h, "qn", "bin", "quai-node")
	r, err := Add(target)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(h, ".local", "bin", "quai-node")
	if got, _ := os.Readlink(link); got != target || r.Path != link {
		t.Errorf("link %s -> %s, result %+v", link, got, r)
	}
	if !strings.Contains(r.Note, "isn't on your PATH") {
		t.Errorf("no PATH hint: %q", r.Note)
	}
	t.Setenv("PATH", filepath.Join(h, ".local", "bin"))
	if r, _ := Add(target); r.Note != "" {
		t.Errorf("hint with the dir on PATH: %q", r.Note)
	}
	// Someone else's link is not removed; ours is.
	if err := Remove("/elsewhere/quai-node"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("removed a link that pointed elsewhere")
	}
	if err := Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("link still there")
	}
	// A regular file in the way is left alone.
	os.WriteFile(link, []byte("x"), 0o755)
	if _, err := Add(target); err == nil {
		t.Error("replaced a regular file")
	}
}
