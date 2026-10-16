package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/mpoletiek/quai-node/internal/home"
)

func TestSaveLoad(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	s := Defaults(h)
	s.Mining = true
	s.QuaiCoinbase = "0x002a8cf994379232561556Da89C148eeec9539cd"
	s.QiCoinbase = "0x0080000000000000000000000000000000000001"
	s.GeoIP, s.Location = "online", "47.4,8.6"
	if err := Save(h, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(h)
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Errorf("loaded %+v, saved %+v", got, s)
	}

	// go-quai reads nested tables keyed like its flags.
	var node map[string]map[string]any
	b, _ := os.ReadFile(h.NodeConfig())
	if err := toml.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	if node["node"]["slices"] != "[0 0]" || node["node"]["environment"] != "colosseum" ||
		node["node"]["stratum-api-addr"] != "127.0.0.1:3336" || node["global"]["data-dir"] != s.DataDir {
		t.Errorf("node config: %v", node)
	}

	b, _ = os.ReadFile(h.DashConfig())
	for _, want := range []string{`rpc = 'http://127.0.0.1:9200'`, `logs = '` + filepath.Join(h.NodeLogs(), "zone-0-0.log") + "'", `stratum_api = 'http://127.0.0.1:3336'`, `geoip = 'online'`, `here = '47.4,8.6'`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dash config lacks %q:\n%s", want, b)
		}
	}
}

func TestValidate(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	s := Defaults(h)
	s.Mining = true
	s.QuaiCoinbase = "0x0080000000000000000000000000000000000001" // a Qi address
	s.QiCoinbase = "0x0080000000000000000000000000000000000001"
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "quai payout") {
		t.Errorf("Validate = %v", err)
	}
	s = Defaults(h)
	for _, bad := range []func(*Settings){
		func(s *Settings) { s.GeoIP = "db" }, // no database
		func(s *Settings) { s.GeoIP = "sometimes" },
		func(s *Settings) { s.Location = "north" },
	} {
		c := Defaults(h)
		bad(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	s.DataDir = "relative/data"
	if err := s.Validate(); err == nil {
		t.Error("relative data dir accepted")
	}
}

func TestExtra(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	s := Defaults(h)

	// No extra.toml: Save leaves a commented starter, which adds nothing.
	if err := Save(h, s); err != nil {
		t.Fatal(err)
	}
	extra, err := LoadExtra(h)
	if err != nil || len(extra) != 0 {
		t.Fatalf("starter: %v, %v", extra, err)
	}

	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(h.ExtraConfig(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[metrics]\nenabled = true\n\n[node]\nmax-peers = '100'\n")
	if err := Save(h, s); err != nil {
		t.Fatal(err)
	}
	var node map[string]map[string]any
	b, _ := os.ReadFile(h.NodeConfig())
	if err := toml.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	if node["metrics"]["enabled"] != true || node["node"]["max-peers"] != "100" || node["node"]["environment"] != "colosseum" {
		t.Errorf("merged config: %v", node)
	}
	if got := ExtraNames(map[string]map[string]any{"node": {"max-peers": "100"}, "metrics": {"enabled": true}}); strings.Join(got, " ") != "metrics.enabled node.max-peers" {
		t.Errorf("ExtraNames = %v", got)
	}
	// Save never rewrites extra.toml once it's there.
	if b, _ := os.ReadFile(h.ExtraConfig()); !strings.HasPrefix(string(b), "[metrics]") {
		t.Errorf("extra.toml was rewritten:\n%s", b)
	}

	for body, want := range map[string]string{
		"[node]\nport = '5000'\n":        "p2p_port",
		"[rpc]\nhttp-addr = '0.0.0.0'\n": "written by quai-node",
		"[metric]\nenabled = true\n":     "no [metric] table",
		"max-peers = '100'\n":            "outside a table",
		"[node.extra]\nx = 1\n":          "plain keys",
		"[node]\nmax-peers = \n":         h.ExtraConfig(),
	} {
		write(body)
		before, _ := os.ReadFile(h.NodeConfig())
		err := Save(h, s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: Save = %v, want %q", body, err, want)
		}
		if after, _ := os.ReadFile(h.NodeConfig()); string(after) != string(before) {
			t.Errorf("%q: config.toml changed although extra.toml was refused", body)
		}
	}
}
