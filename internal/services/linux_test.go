//go:build linux

package services

import (
	"io"
	"strings"
	"testing"

	"github.com/mpoletiek/quai-node/internal/home"
)

func TestRender(t *testing.T) {
	h := home.Home{Dir: "/home/alice/.local/share/quai-node"}
	sp := Service(h, "alice", LogDir(h.Dir))
	r := NewRunner(io.Discard, true)

	_, unit, err := (&systemd{r}).Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"User=alice",
		"WorkingDirectory=/home/alice/.local/share/quai-node\n",
		"ExecStart=/home/alice/.local/share/quai-node/bin/quai-node run --service --home /home/alice/.local/share/quai-node\n",
		"KillMode=mixed", "KillSignal=SIGINT", "TimeoutStopSec=330",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("systemd unit lacks %q:\n%s", want, unit)
		}
	}

	_, script, err := (&openrc{r}).Render(sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`command="/home/alice/.local/share/quai-node/bin/quai-node"`,
		`command_args="'run' '--service' '--home' '/home/alice/.local/share/quai-node'"`,
		`command_user="alice"`, `directory="/home/alice/.local/share/quai-node"`,
		`supervisor="supervise-daemon"`, `retry="INT/330/KILL/5"`,
		`output_log="/var/log/quai-node/quai-node.log"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("openrc script lacks %q:\n%s", want, script)
		}
	}

	bad := sp
	bad.Dir = `/home/o'brien/quai-node`
	if _, _, err := (&openrc{r}).Render(bad); err == nil {
		t.Error("openrc accepted a path with a quote")
	}
	bad.Dir = "/home/al ice/quai node"
	if _, unit, _ := (&systemd{r}).Render(bad); !strings.Contains(unit, `WorkingDirectory="/home/al ice/quai node"`) {
		t.Errorf("systemd didn't quote a path with spaces:\n%s", unit)
	}
}

func TestChildren(t *testing.T) {
	h := home.Home{Dir: "/q"}
	c := Children(h, true, "/q/logs")
	if len(c) != 2 || c[0].Name != Node || !c[0].Critical || !c[0].QuietStdout || c[1].Name != Dash || c[1].Critical {
		t.Errorf("children: %+v", c)
	}
	if len(Children(h, false, "/q/logs")) != 1 {
		t.Error("dashboard off still runs quai-dash")
	}
}
