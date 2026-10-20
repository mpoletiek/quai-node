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

// A quai-node.toml from before the Keepers moved to quainode.org has
// quaination_tag/quaination_secret: Load moves them over, Save writes the
// keepers_* keys only, and go-quai reports to keepers.quainode.org.
func TestLegacyKeepers(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "network = 'colosseum'\nregion = 0\nzone = 0\ndata_dir = '" + filepath.Join(h.Dir, "data") + "'\n" +
		"p2p_port = 4002\nportmap = true\nmining = false\ndashboard = false\ndashboard_listen = '127.0.0.1:8090'\n" +
		"geoip = 'auto'\nquaination_tag = 'MyNode'\nquaination_secret = 'abcdefghijklmnopqrstuvwxyz'\nservice = false\n"
	if err := os.WriteFile(h.Settings(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(h)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeepersTag != "MyNode" || s.KeepersSecret != "abcdefghijklmnopqrstuvwxyz" || !s.Reporting() {
		t.Fatalf("legacy keys not migrated: %+v", s)
	}
	if err := Save(h, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(h.Settings())
	if strings.Contains(string(b), "quaination") ||
		!strings.Contains(string(b), "keepers_tag = 'MyNode'") || !strings.Contains(string(b), "keepers_secret = 'abcdefghijklmnopqrstuvwxyz'") {
		t.Errorf("saved settings:\n%s", b)
	}
	if again, err := Load(h); err != nil || again != s {
		t.Errorf("reloaded %+v (%v), want %+v", again, err, s)
	}
	var node map[string]map[string]any
	b, _ = os.ReadFile(h.NodeConfig())
	if err := toml.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	if got := node["node"]["quaistats"]; got != "MyNode:abcdefghijklmnopqrstuvwxyz@keepers.quainode.org" {
		t.Errorf("node.quaistats = %v", got)
	}
	if node["rpc"]["http-addr"] != "0.0.0.0" {
		t.Errorf("rpc: %v", node["rpc"])
	}

	// The new keys win over leftover legacy ones.
	both := legacy + "keepers_tag = 'NewTag'\nkeepers_secret = 'zyxwvutsrqponmlkjihgfedcba'\n"
	os.WriteFile(h.Settings(), []byte(both), 0o600)
	if s, err := Load(h); err != nil || s.KeepersTag != "NewTag" || s.KeepersSecret != "zyxwvutsrqponmlkjihgfedcba" {
		t.Errorf("Load = %+v, %v", s, err)
	}

	// Secrets the Keepers refuse are refused here too.
	s.KeepersSecret = "short"
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "keepers_secret") {
		t.Errorf("Validate = %v", err)
	}
	s.KeepersSecret = "abcdefghijklmnopqrstuvwxyz"
	s.Zone = 1
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "cyprus1") {
		t.Errorf("Validate = %v", err)
	}
}

// A hand-picked legacy secret shorter than quainode.org's 16 is replaced, not left to fail the update.
func TestLegacyShortSecret(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "network = 'colosseum'\nregion = 0\nzone = 0\ndata_dir = '" + filepath.Join(h.Dir, "data") +
		"'\np2p_port = 4002\nportmap = true\nmining = false\ndashboard = false\ndashboard_listen = '127.0.0.1:8090'\n" +
		"geoip = 'off'\nservice = false\nquaination_tag = 'Ember-Forge'\nquaination_secret = 'short123'\n"
	if err := os.WriteFile(h.Settings(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(h)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeepersTag != "Ember-Forge" || len(s.KeepersSecret) < 16 || s.KeepersSecret == "short123" {
		t.Fatalf("tag %q secret %q", s.KeepersTag, s.KeepersSecret)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("migrated settings don't validate: %v", err)
	}
}

func TestIndexNode(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	s := Defaults(h)
	if _, ok := NodeConfig(h, s)["node"]["index-address-utxos"]; ok {
		t.Error("not an index node, yet index-address-utxos is set")
	}
	s.IndexUTXOs = true
	if v := NodeConfig(h, s)["node"]["index-address-utxos"]; v != true {
		t.Errorf("index node: index-address-utxos = %v", v)
	}

	write := func(body string) {
		t.Helper()
		if err := os.MkdirAll(h.ConfigDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(h.ExtraConfig(), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// It's quai-node's to write now, so extra.toml can't.
	write("[node]\nindex-address-utxos = true\n")
	if err := Save(h, s); err == nil || !strings.Contains(err.Error(), "index_utxos") {
		t.Errorf("index-address-utxos in extra.toml: Save = %v, want it pointed at index_utxos", err)
	}

	// go-quai's reindex opens the zone database as LevelDB.
	write("[node]\nreindex = true\n")
	s.DBEngine = "pebble"
	if err := Save(h, s); err == nil || !strings.Contains(err.Error(), "Pebble") {
		t.Errorf("reindex on Pebble: Save = %v, want a refusal", err)
	}
	s.DBEngine = ""
	if err := Save(h, s); err != nil {
		t.Errorf("reindex on LevelDB: Save = %v", err)
	}
	write("[node]\nreindex = false\n")
	s.DBEngine = "pebble"
	if err := Save(h, s); err != nil {
		t.Errorf("reindex = false on Pebble: Save = %v", err)
	}
}
