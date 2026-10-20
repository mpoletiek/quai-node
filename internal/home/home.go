// Package home lays out the quai-node home directory: the binaries and
// runtime files unpacked from the bundle, the configs written by setup, the
// chain data and go-quai's nodelogs.
//
//	<home>/
//	  quai-node.toml        settings chosen in setup (ours)
//	  bin/                  go-quai, quai-dash, a copy of quai-node
//	  VERSION, params/      read by go-quai from its working directory
//	  config/config.toml    go-quai's config (--global.config-dir)
//	  config/extra.toml     go-quai keys of the user's own, merged into config.toml
//	  dash/config.toml      quai-dash's config (--config)
//	  data/                 chain data (default; setup can move it)
//	  nodelogs/             written by go-quai in its working directory
//
// go-quai is always started with <home> as its working directory, because
// it reads VERSION and params/*.json from there and writes nodelogs/ there.
package home

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Home is a quai-node home directory.
type Home struct {
	Dir string
}

// Default is the per-OS default home:
//
//	Linux:   $XDG_DATA_HOME/quai-node, else ~/.local/share/quai-node
//	macOS:   ~/Library/Application Support/quai-node
//	Windows: %ProgramData%\quai-node (the Windows service runs as
//	         LocalSystem, which has no use for one user's profile)
func Default() (Home, error) {
	if d := os.Getenv("QUAI_NODE_HOME"); d != "" {
		return Home{Dir: d}, nil
	}
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return Home{Dir: filepath.Join(pd, "quai-node")}, nil
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil {
			return Home{}, err
		}
		return Home{Dir: filepath.Join(h, "Library", "Application Support", "quai-node")}, nil
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return Home{Dir: filepath.Join(x, "quai-node")}, nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return Home{}, err
		}
		return Home{Dir: filepath.Join(h, ".local", "share", "quai-node")}, nil
	}
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func (h Home) Settings() string    { return filepath.Join(h.Dir, "quai-node.toml") }
func (h Home) BinDir() string      { return filepath.Join(h.Dir, "bin") }
func (h Home) GoQuai() string      { return filepath.Join(h.BinDir(), exe("go-quai")) }
func (h Home) QuaiDash() string    { return filepath.Join(h.BinDir(), exe("quai-dash")) }
func (h Home) QuaiNode() string    { return filepath.Join(h.BinDir(), exe("quai-node")) }
func (h Home) ConfigDir() string   { return filepath.Join(h.Dir, "config") }
func (h Home) NodeConfig() string  { return filepath.Join(h.ConfigDir(), "config.toml") }
func (h Home) ExtraConfig() string { return filepath.Join(h.ConfigDir(), "extra.toml") }
func (h Home) DashDir() string     { return filepath.Join(h.Dir, "dash") }
func (h Home) DashConfig() string  { return filepath.Join(h.DashDir(), "config.toml") }
func (h Home) DefaultData() string { return filepath.Join(h.Dir, "data") }
func (h Home) NodeLogs() string    { return filepath.Join(h.Dir, "nodelogs") }
func (h Home) Logs() string        { return filepath.Join(h.Dir, "logs") }
func (h Home) BundleStamp() string { return filepath.Join(h.Dir, ".bundle") }

// Exists reports whether setup has run here.
func (h Home) Exists() bool {
	_, err := os.Stat(h.Settings())
	return err == nil
}

// ChainUse is what the chain databases in dataDir use now (prime,
// region-N, zone-N-M), or 0.
func ChainUse(dataDir string) int64 {
	var total int64
	for _, pat := range []string{"prime", "region-*", "zone-*"} {
		dirs, _ := filepath.Glob(filepath.Join(dataDir, pat))
		for _, d := range dirs {
			_ = filepath.WalkDir(d, func(_ string, e fs.DirEntry, err error) error {
				if err == nil && e.Type().IsRegular() {
					if fi, err := e.Info(); err == nil {
						total += fi.Size()
					}
				}
				return nil
			})
		}
	}
	return total
}

// RemoveChain deletes the chain databases in dataDir (prime, region-N,
// zone-N-M), the ones ChainUse measures, and the index marker with them.
func RemoveChain(dataDir string) error {
	for _, pat := range []string{"prime", "region-*", "zone-*"} {
		dirs, _ := filepath.Glob(filepath.Join(dataDir, pat))
		for _, d := range dirs {
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	}
	return UnmarkIndexed(dataDir)
}

// indexMarker, in a data directory, says its chain was indexed (go-quai's
// address index of Qi UTXOs) from its first block. go-quai indexes only the
// blocks it processes while indexing is on, and quai-node doesn't reindex,
// so an index node needs a chain made with it on.
const indexMarker = ".quai-node-indexed"

const indexMarkerText = `quai-node wrote this when the node started this chain as an index node
(index_utxos): go-quai has indexed every address's Qi UTXOs from the first
block. quai-node removes it when the chain is deleted or replaced, or
indexing is turned off.
`

// IndexedFromStart reports whether the chain in dataDir has been indexed
// from its first block.
func IndexedFromStart(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, indexMarker))
	return err == nil
}

// MarkIndexed records that the chain in dataDir, which must be empty now,
// is indexed from its first block.
func MarkIndexed(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, indexMarker), []byte(indexMarkerText), 0o644)
}

// UnmarkIndexed forgets that the chain in dataDir is indexed.
func UnmarkIndexed(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, indexMarker))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ChainEngine is the database engine of the chain in dataDir, "pebble" or
// "leveldb", from its files; "" when there is no chain there. Pebble
// writes an OPTIONS file; LevelDB doesn't.
func ChainEngine(dataDir string) string {
	var dbs []string
	for _, pat := range []string{"zone-*", "region-*", "prime"} {
		m, _ := filepath.Glob(filepath.Join(dataDir, pat, "go-quai", "chaindata"))
		dbs = append(dbs, m...)
	}
	for _, d := range dbs {
		if m, _ := filepath.Glob(filepath.Join(d, "OPTIONS-*")); len(m) > 0 {
			return "pebble"
		}
		if m, _ := filepath.Glob(filepath.Join(d, "*.ldb")); len(m) > 0 {
			return "leveldb"
		}
		if _, err := os.Stat(filepath.Join(d, "CURRENT")); err == nil {
			return "leveldb"
		}
	}
	return ""
}
