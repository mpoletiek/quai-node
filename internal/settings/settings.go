// Package settings holds what setup chose (quai-node.toml) and renders the
// config files go-quai and quai-dash read from it.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/mpoletiek/quai-node/internal/geo"
	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/keepers"
	"github.com/mpoletiek/quai-node/internal/quaiaddr"
)

// Networks quai-node offers. go-quai calls them environments.
var Networks = []string{"colosseum", "orchard"}

// Settings is quai-node.toml.
type Settings struct {
	Network string `toml:"network"` // colosseum (mainnet) or orchard (testnet)
	Region  int    `toml:"region"`
	Zone    int    `toml:"zone"`
	DataDir string `toml:"data_dir"`
	// go-quai's database engine for the chain data: "" (go-quai's
	// default, leveldb) or pebble, which a quikshot.dev snapshot sets.
	DBEngine string `toml:"db_engine,omitempty"`
	// RestoreSnapshot asks setup to start the chain from quikshot.dev's
	// snapshot. It is a request for this run, not a setting.
	RestoreSnapshot bool `toml:"-"`
	// WipeChain asks setup to delete the chain in DataDir, so the node
	// syncs it again from the network (not saved either).
	WipeChain bool `toml:"-"`
	P2PPort   int  `toml:"p2p_port"`
	PortMap   bool `toml:"portmap"` // UPnP/NAT-PMP port forwarding
	// IndexUTXOs makes this an index node: go-quai keeps an index of each
	// address's Qi UTXOs (--node.index-address-utxos), which Qi balances
	// and UTXO lists on its RPC come from. It covers the blocks the node
	// processes while it is on.
	IndexUTXOs bool `toml:"index_utxos,omitempty"`

	Mining       bool   `toml:"mining"` // run go-quai's stratum for local miners
	QuaiCoinbase string `toml:"quai_coinbase,omitempty"`
	QiCoinbase   string `toml:"qi_coinbase,omitempty"`

	Dashboard       bool   `toml:"dashboard"`
	DashboardListen string `toml:"dashboard_listen"`
	// The dashboard's map: where peer locations come from (quai-dash's
	// geoip: auto, online, db or off), a City database for db, and this
	// node's own position as LAT,LON ("" leaves it off the map).
	GeoIP    string `toml:"geoip"`
	GeoIPDB  string `toml:"geoip_db,omitempty"`
	Location string `toml:"location,omitempty"`

	// Reporting to the Keepers on quainode.org: on when the tag is set. The
	// secret reserves the tag; it is also how `quai-node status` reads the
	// node's standing there. Reporting makes the node's RPC public.
	// (Load also reads the legacy quaination_tag/quaination_secret keys.)
	KeepersTag    string `toml:"keepers_tag,omitempty"`
	KeepersSecret string `toml:"keepers_secret,omitempty"`

	Service bool   `toml:"service"`          // node (and dashboard) run as services
	RunAs   string `toml:"run_as,omitempty"` // Linux/macOS: user the services run as
}

// Defaults for a home.
func Defaults(h home.Home) Settings {
	return Settings{
		Network:         "colosseum",
		DataDir:         h.DefaultData(),
		P2PPort:         4002,
		PortMap:         true,
		Dashboard:       true,
		DashboardListen: "127.0.0.1:8090",
		GeoIP:           geo.Auto,
		Service:         true,
	}
}

// DashboardPublic reports whether the dashboard listens on every
// interface rather than loopback.
func (s Settings) DashboardPublic() bool {
	host, _, _ := net.SplitHostPort(s.DashboardListen)
	return host == "0.0.0.0" || host == "::" || host == ""
}

// DashboardPort is the dashboard's port.
func (s Settings) DashboardPort() string {
	_, port, _ := net.SplitHostPort(s.DashboardListen)
	return port
}

// Reporting reports whether the node reports to the Keepers.
func (s Settings) Reporting() bool { return s.KeepersTag != "" }

// ZoneOf is the zone the node runs.
func (s Settings) ZoneOf() quaiaddr.Zone { return quaiaddr.Zone{Region: s.Region, Zone: s.Zone} }

// Engine is the database engine go-quai runs with: DBEngine, or go-quai's
// default, leveldb.
func (s Settings) Engine() string {
	if s.DBEngine == "" {
		return "leveldb"
	}
	return s.DBEngine
}

// ZoneRPC is the zone's JSON-RPC URL on this host.
func (s Settings) ZoneRPC() string {
	_, _, z := s.ZoneOf().RPCPorts()
	return fmt.Sprintf("http://127.0.0.1:%d", z)
}

// Validate checks the settings before anything is written.
func (s Settings) Validate() error {
	ok := false
	for _, n := range Networks {
		ok = ok || n == s.Network
	}
	if !ok {
		return fmt.Errorf("network %q: expected one of %v", s.Network, Networks)
	}
	if s.Region < 0 || s.Region > 2 || s.Zone < 0 || s.Zone > 2 {
		return fmt.Errorf("zone [%d %d] does not exist", s.Region, s.Zone)
	}
	if s.P2PPort < 1 || s.P2PPort > 65535 {
		return fmt.Errorf("p2p port %d out of range", s.P2PPort)
	}
	if !filepath.IsAbs(s.DataDir) {
		return fmt.Errorf("data directory %q must be an absolute path", s.DataDir)
	}
	switch s.GeoIP {
	case geo.Auto, geo.Online, geo.Off, "":
	case geo.DB:
		if s.GeoIPDB == "" {
			return fmt.Errorf("geoip db needs geoip_db, a MaxMind City database (.mmdb)")
		}
	default:
		return fmt.Errorf("geoip %q: expected auto, online, db or off", s.GeoIP)
	}
	switch s.DBEngine {
	case "", "leveldb", "pebble":
	default:
		return fmt.Errorf("db_engine %q: expected leveldb or pebble", s.DBEngine)
	}
	if _, _, err := net.SplitHostPort(s.DashboardListen); err != nil {
		return fmt.Errorf("dashboard_listen %q: expected HOST:PORT", s.DashboardListen)
	}
	if s.Reporting() {
		if err := keepers.CheckTag(s.KeepersTag); err != nil {
			return fmt.Errorf("keepers_tag %q: %w", s.KeepersTag, err)
		}
		if n := len(s.KeepersSecret); n < 16 || n > 128 {
			return errors.New("keepers_secret: 16 to 128 characters")
		}
		// The Keepers verify nodes with Cyprus-1 state proofs, on mainnet.
		if s.Network != "colosseum" || s.ZoneOf() != (quaiaddr.Zone{}) {
			return errors.New("the Keepers on quainode.org take mainnet (colosseum) nodes running cyprus1")
		}
	}
	if s.Location != "" {
		if _, err := geo.ParseLatLon(s.Location); err != nil {
			return fmt.Errorf("location %q: %w", s.Location, err)
		}
	}
	if s.Mining {
		if err := quaiaddr.Check(s.QuaiCoinbase, quaiaddr.Quai, s.ZoneOf()); err != nil {
			return fmt.Errorf("quai payout address: %w", err)
		}
		if err := quaiaddr.Check(s.QiCoinbase, quaiaddr.Qi, s.ZoneOf()); err != nil {
			return fmt.Errorf("qi payout address: %w", err)
		}
	}
	return nil
}

// Load reads quai-node.toml.
func Load(h home.Home) (Settings, error) {
	s := Defaults(h)
	b, err := os.ReadFile(h.Settings())
	if err != nil {
		return s, err
	}
	if err := toml.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", h.Settings(), err)
	}
	// Legacy: installs from before the Keepers moved to quainode.org keep
	// their tag and secret under quaination_tag/quaination_secret. Move them
	// over; Save writes only the keepers_* keys.
	var legacy struct {
		Tag    string `toml:"quaination_tag"`
		Secret string `toml:"quaination_secret"`
	}
	if err := toml.Unmarshal(b, &legacy); err != nil {
		return s, fmt.Errorf("%s: %w", h.Settings(), err)
	}
	if s.KeepersTag == "" && legacy.Tag != "" {
		s.KeepersTag, s.KeepersSecret = legacy.Tag, legacy.Secret
		// quainode.org wants 16+ characters. Nothing is reserved there yet (the old roll was a separate
		// service), so a short hand-picked secret is simply replaced rather than failing the update.
		if len(s.KeepersSecret) < 16 {
			s.KeepersSecret = keepers.NewSecret()
		}
	}
	return s, nil
}

// Save writes quai-node.toml, go-quai's config.toml (with extra.toml's keys
// merged in) and, when the dashboard is on, quai-dash's config.toml.
func Save(h home.Home, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	extra, err := LoadExtra(h)
	if err != nil {
		return err
	}
	if err := CheckExtra(h, extra, s); err != nil {
		return err
	}
	for _, d := range []string{h.Dir, h.ConfigDir(), s.DataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	b, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	head := "# quai-node settings, written by `quai-node setup`. Edit and run\n# `quai-node setup --apply` to rewrite the configs below from it.\n\n"
	// Owner-only: both can hold the Keepers secret.
	if err := writeFile(h.Settings(), append([]byte(head), b...)); err != nil {
		return err
	}
	node := NodeConfig(h, s)
	merge(node, extra)
	if err := writeTOML(h.NodeConfig(), nodeConfigHead, node); err != nil {
		return err
	}
	if err := writeExtraStarter(h); err != nil {
		return err
	}
	if s.Dashboard {
		if err := os.MkdirAll(h.DashDir(), 0o755); err != nil {
			return err
		}
		return writeTOML(h.DashConfig(), dashConfigHead, DashConfig(h, s))
	}
	return nil
}

const nodeConfigHead = `# go-quai config, written by quai-node from quai-node.toml and extra.toml
# (rewritten by 'quai-node setup'). Edits here don't last: put keys of your
# own in extra.toml beside this file. Keys not here take go-quai's defaults;
# run 'go-quai config' for the full list.

`

const dashConfigHead = `# quai-dash config, written by quai-node from quai-node.toml (rewritten by
# 'quai-node setup'). Every key is listed in quai-dash's contrib/config.toml.

`

// NodeConfig is go-quai's config.toml, as nested tables keyed like its flags.
func NodeConfig(h home.Home, s Settings) map[string]map[string]any {
	node := map[string]any{
		"environment": s.Network,
		"slices":      s.ZoneOf().Slices(),
		"port":        fmt.Sprint(s.P2PPort),
		"portmap":     s.PortMap,
	}
	if s.DBEngine != "" {
		node["db-engine"] = s.DBEngine
	}
	if s.IndexUTXOs {
		node["index-address-utxos"] = true
	}
	if s.Mining {
		node["quai-coinbases"] = s.QuaiCoinbase
		node["qi-coinbases"] = s.QiCoinbase
		// Miners on the LAN reach the stratum ports (3333-3335 on all
		// interfaces, go-quai's default). Its API has no authentication,
		// so it stays on loopback, where quai-dash reads it.
		node["stratum-enabled"] = true
		node["stratum-api-addr"] = "127.0.0.1:3336"
	}
	rpc := map[string]any{"http": true, "http-addr": "127.0.0.1"}
	if s.Reporting() {
		_, _, zone := s.ZoneOf().RPCPorts()
		node["quaistats"] = keepers.StatsFlag(s.KeepersTag, s.KeepersSecret, zone)
		// The Keepers check the node by calling its RPC from the internet.
		rpc["http-addr"] = "0.0.0.0"
		rpc["http-vhosts"] = "*"
	}
	return map[string]map[string]any{
		"global": {"data-dir": s.DataDir},
		"node":   node,
		"rpc":    rpc,
	}
}

// DashConfig is quai-dash's config.toml. Pointing it at the node's RPC and
// log means it needs no detection, which on macOS (no /proc) it couldn't do.
//
// The log is the zone's own file, not the nodelogs directory: quai-dash
// picks a file from a directory once, at startup, and on a first start
// go-quai hasn't written zone-R-Z.log yet, so it would settle on
// global.log. A named file it waits for.
func DashConfig(h home.Home, s Settings) map[string]any {
	prime, region, _ := s.ZoneOf().RPCPorts()
	z := s.ZoneOf()
	c := map[string]any{
		"rpc":       s.ZoneRPC(),
		"region":    fmt.Sprintf("http://127.0.0.1:%d", region),
		"prime":     fmt.Sprintf("http://127.0.0.1:%d", prime),
		"node_kind": "go-quai",
		"logs":      filepath.Join(h.NodeLogs(), fmt.Sprintf("zone-%d-%d.log", z.Region, z.Zone)),
		"listen":    s.DashboardListen,
		"label":     fmt.Sprintf("%s · %s", s.Network, s.ZoneOf().Name()),
	}
	if s.Mining {
		c["stratum_api"] = "http://127.0.0.1:3336"
	} else {
		c["stratum_api"] = "off"
	}
	if s.Network != "colosseum" {
		c["explorer"] = "off" // explorer.qu.ai shows mainnet
	}
	if s.GeoIP != "" {
		c["geoip"] = s.GeoIP
	}
	if s.GeoIPDB != "" {
		c["geoip_db"] = s.GeoIPDB
	}
	if s.Location != "" {
		c["here"] = s.Location
	}
	return c
}

func writeTOML(path, head string, v any) error {
	var buf bytes.Buffer
	buf.WriteString(head)
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes())
}

// writeFile replaces path with b, readable by its owner only.
func writeFile(path string, b []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // a leftover .new keeps its mode
		return err
	}
	return os.Rename(tmp, path)
}
