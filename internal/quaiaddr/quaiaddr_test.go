package quaiaddr

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	cyprus1 := Zone{0, 0}
	cases := []struct {
		addr   string
		ledger Ledger
		zone   Zone
		err    string
	}{
		{"0x002a8cf994379232561556Da89C148eeec9539cd", Quai, cyprus1, ""},
		{"0x0080000000000000000000000000000000000001", Qi, cyprus1, ""},
		{"0x0080000000000000000000000000000000000001", Quai, cyprus1, "Qi address"},
		{"0x002a8cf994379232561556Da89C148eeec9539cd", Qi, cyprus1, "Quai address"},
		{"0x102a8cf994379232561556Da89C148eeec9539cd", Quai, cyprus1, "in paxos1"},
		{"0x00a3e45a", Quai, cyprus1, "40 hex digits"},
		{"0xzza3e45aa16163F2663015b6695894D918866d19", Quai, cyprus1, "40 hex digits"},
	}
	for _, c := range cases {
		err := Check(c.addr, c.ledger, c.zone)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("Check(%s, %v, %s) = %v, want %q", c.addr, c.ledger, c.zone.Name(), err, c.err)
		}
	}
}

func TestPorts(t *testing.T) {
	// go-quai: prime 9001, region 9002+r, zone 9001+199+20r+z.
	p, r, z := Zone{0, 0}.RPCPorts()
	if p != 9001 || r != 9002 || z != 9200 {
		t.Errorf("cyprus1 ports = %d %d %d", p, r, z)
	}
	if _, r, z := (Zone{2, 1}).RPCPorts(); r != 9004 || z != 9241 {
		t.Errorf("hydra2 ports = %d %d", r, z)
	}
	if n := (Zone{1, 2}).Name(); n != "paxos3" {
		t.Errorf("name = %s", n)
	}
}
