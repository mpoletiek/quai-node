package services

import (
	"path/filepath"
	"testing"

	"github.com/mpoletiek/quai-node/internal/home"
)

// fakeMgr has installed services as name → definition.
type fakeMgr struct {
	Manager
	defs map[string]string
}

func (f fakeMgr) Definition(name string) (string, error) { return f.defs[name], nil }

func TestOwns(t *testing.T) {
	// Absolute on every OS (Windows wants a drive letter).
	base := t.TempDir()
	mine := home.Home{Dir: filepath.Join(base, "quai-node")}
	other := home.Home{Dir: filepath.Join(base, "quai-node2")}
	m := fakeMgr{defs: map[string]string{
		Name: "ExecStart=" + mine.QuaiNode() + " run --service --home " + mine.Dir + "\n",
		Node: "command=\"" + other.GoQuai() + "\"\n",
	}}
	cases := []struct {
		name            string
		h               home.Home
		installed, ours bool
	}{
		{Name, mine, true, true},
		{Name, other, true, false}, // quai-node2 isn't quai-node
		{Node, other, true, true},  // legacy go-quai service of the other home
		{Node, mine, true, false},
		{Dash, mine, false, false}, // not installed
	}
	for _, c := range cases {
		installed, ours, err := Owns(m, c.name, c.h)
		if err != nil || installed != c.installed || ours != c.ours {
			t.Errorf("Owns(%s, %s) = %v, %v, %v; want %v, %v", c.name, c.h.Dir, installed, ours, err, c.installed, c.ours)
		}
	}
	if got := OwnerOf(m.defs[Name]); got != mine.QuaiNode() {
		t.Errorf("OwnerOf = %q", got)
	}
}
