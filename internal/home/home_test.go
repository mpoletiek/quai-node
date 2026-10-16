package home

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChainEngine(t *testing.T) {
	empty := t.TempDir()
	if got := ChainEngine(empty); got != "" {
		t.Fatalf("no chain: %q", got)
	}

	pebble := t.TempDir()
	for _, f := range []string{"CURRENT", "MANIFEST-000001", "OPTIONS-000003", "000005.sst"} {
		touch(t, filepath.Join(pebble, "zone-0-0", "go-quai", "chaindata", f))
	}
	touch(t, filepath.Join(pebble, "quaibackend", "CURRENT")) // always LevelDB; not the chain
	if got := ChainEngine(pebble); got != "pebble" {
		t.Fatalf("pebble chain: %q", got)
	}

	level := t.TempDir()
	for _, f := range []string{"CURRENT", "MANIFEST-000002", "000004.ldb", "LOG"} {
		touch(t, filepath.Join(level, "prime", "go-quai", "chaindata", f))
	}
	if got := ChainEngine(level); got != "leveldb" {
		t.Fatalf("leveldb chain: %q", got)
	}
	if ChainUse(level) == 0 {
		t.Fatal("ChainUse sees no chain")
	}
}

func TestRemoveChain(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"prime/go-quai/chaindata/OPTIONS-000001",
		"region-0/go-quai/chaindata/OPTIONS-000001",
		"zone-0-0/go-quai/chaindata/000002.sst",
		"quaibackend/CURRENT",
		"0xff2b/0,0/blocks/peers",
	} {
		touch(t, filepath.Join(dir, f))
	}
	if err := RemoveChain(dir); err != nil {
		t.Fatal(err)
	}
	if ChainUse(dir) != 0 || ChainEngine(dir) != "" {
		t.Fatal("chain still there")
	}
	for _, keep := range []string{"quaibackend/CURRENT", "0xff2b/0,0/blocks/peers"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("%s went too: %v", keep, err)
		}
	}
}
