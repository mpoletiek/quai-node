package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/mpoletiek/quai-node/internal/home"
)

// extra.toml holds go-quai keys of the user's own. quai-node never writes
// it (past a commented starter) and merges it into config.toml every time it
// rewrites that, so the keys outlive setup, updates and snapshot restores.

// GoQuaiTables are the tables in the bundled go-quai's config (`go-quai
// config` prints them all). A table not here is a typo: go-quai would
// ignore it without a word.
var GoQuaiTables = []string{"global", "metrics", "node", "peers", "rpc", "txpool", "workshare"}

// owned are the go-quai keys NodeConfig can write, each with where to change
// it instead. extra.toml can't set them: quai-node acts on its own copy
// (snapshots restore into data_dir, the dashboard reads the RPC it set up).
var owned = map[string]map[string]string{
	"global": {"data-dir": "data_dir"},
	"node": {
		"environment":         "network",
		"slices":              "region and zone",
		"port":                "p2p_port",
		"portmap":             "portmap",
		"db-engine":           "db_engine",
		"index-address-utxos": "index_utxos",
		"quai-coinbases":      "quai_coinbase",
		"qi-coinbases":        "qi_coinbase",
		"stratum-enabled":     "mining",
		"stratum-api-addr":    "mining",
		"quaistats":           "keepers_tag",
	},
	"rpc": {
		"http":        "",
		"http-addr":   "",
		"http-vhosts": "",
	},
}

const extraStarter = `# go-quai keys of your own. quai-node merges this file into config.toml
# whenever it rewrites that (setup, updates, setup --apply, snapshot restore),
# so keys here last; edits made to config.toml itself don't.
#
# Use go-quai's tables and key names: 'go-quai config' prints every key with
# its default. Keys quai-node writes from quai-node.toml (the network, zone,
# ports, data dir, mining, the index and the RPC) can't be set here.
#
# After editing:  quai-node setup --apply
#
# [metrics]
# enabled = true
#
# [node]
# max-peers = '100'
`

// LoadExtra reads extra.toml: nil if there is none.
func LoadExtra(h home.Home) (map[string]map[string]any, error) {
	b, err := os.ReadFile(h.ExtraConfig())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", h.ExtraConfig(), err)
	}
	out := map[string]map[string]any{}
	for name, v := range raw {
		table, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %q is outside a table: put it under one of [%s]", h.ExtraConfig(), name, strings.Join(GoQuaiTables, "] ["))
		}
		if !known(name) {
			return nil, fmt.Errorf("%s: go-quai has no [%s] table: expected one of [%s]", h.ExtraConfig(), name, strings.Join(GoQuaiTables, "] ["))
		}
		for key, val := range table {
			if _, nested := val.(map[string]any); nested {
				return nil, fmt.Errorf("%s: [%s.%s]: go-quai's tables hold plain keys, not tables", h.ExtraConfig(), name, key)
			}
			if setting, ok := owned[name][key]; ok {
				where := "quai-node sets it"
				if setting != "" {
					where = "change " + setting + " in quai-node.toml, then run quai-node setup --apply"
				}
				return nil, fmt.Errorf("%s: [%s] %s is written by quai-node: %s", h.ExtraConfig(), name, key, where)
			}
		}
		out[name] = table
	}
	return out, nil
}

// CheckExtra turns away keys of extra that go wrong with s. go-quai's
// reindex (node.reindex) opens the zone database as LevelDB whatever the
// chain's engine, so on a Pebble chain, as quikshot.dev's snapshots are,
// it fails or damages the chain.
func CheckExtra(h home.Home, extra map[string]map[string]any, s Settings) error {
	if v, ok := extra["node"]["reindex"]; ok && v != false && v != "false" && s.Engine() == "pebble" {
		return fmt.Errorf("%s: [node] reindex can't run on this chain: go-quai reindexes the zone database as LevelDB, and this chain is Pebble; remove it", h.ExtraConfig())
	}
	return nil
}

// ExtraNames lists extra's keys as table.key, sorted (for setup's output).
func ExtraNames(extra map[string]map[string]any) []string {
	var out []string
	for name, t := range extra {
		for key := range t {
			out = append(out, name+"."+key)
		}
	}
	sort.Strings(out)
	return out
}

func known(table string) bool {
	for _, t := range GoQuaiTables {
		if t == table {
			return true
		}
	}
	return false
}

// merge adds extra's keys to the node config. LoadExtra has already turned
// away the keys quai-node writes, so nothing of quai-node's is replaced.
func merge(node, extra map[string]map[string]any) {
	for name, t := range extra {
		if node[name] == nil {
			node[name] = map[string]any{}
		}
		for k, v := range t {
			node[name][k] = v
		}
	}
}

// writeExtraStarter puts a commented extra.toml beside config.toml, once.
func writeExtraStarter(h home.Home) error {
	f, err := os.OpenFile(h.ExtraConfig(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(extraStarter); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
