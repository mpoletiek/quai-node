package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/settings"
)

func chainIn(t *testing.T, dir string) {
	t.Helper()
	db := filepath.Join(dir, "zone-0-0", "go-quai", "chaindata")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(db, "000001.ldb"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndexFromStart(t *testing.T) {
	h := home.Home{Dir: t.TempDir()}
	s := settings.Defaults(h)
	s.IndexUTXOs = true

	// An empty data dir: an index node starts its chain, and marks it.
	if err := checkIndex(s); err != nil {
		t.Fatalf("empty data dir: %v", err)
	}
	if err := markIndex(s); err != nil || !home.IndexedFromStart(s.DataDir) {
		t.Fatalf("markIndex on an empty chain: %v, marked %v", err, home.IndexedFromStart(s.DataDir))
	}
	chainIn(t, s.DataDir)
	if err := checkIndex(s); err != nil {
		t.Fatalf("a chain indexed from its start: %v", err)
	}

	// No snapshot for an index node.
	s.RestoreSnapshot = true
	if err := checkIndex(s); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("index node with a snapshot: %v", err)
	}
	s.RestoreSnapshot = false

	// Deleting the chain forgets the mark.
	if err := home.RemoveChain(s.DataDir); err != nil || home.IndexedFromStart(s.DataDir) {
		t.Fatalf("RemoveChain: %v, still marked %v", err, home.IndexedFromStart(s.DataDir))
	}

	// A chain made without indexing can't become an index node's, unless
	// it's deleted.
	chainIn(t, s.DataDir)
	if err := checkIndex(s); err == nil || !strings.Contains(err.Error(), "--from-scratch") {
		t.Errorf("unindexed chain: %v", err)
	}
	s.WipeChain = true
	if err := checkIndex(s); err != nil {
		t.Errorf("unindexed chain, deleted: %v", err)
	}
	if err := markIndex(s); err != nil || home.IndexedFromStart(s.DataDir) {
		t.Errorf("markIndex marked a chain it didn't start: %v", err)
	}

	// Turning indexing off ends the mark for good.
	os.RemoveAll(filepath.Join(s.DataDir, "zone-0-0"))
	if err := markIndex(s); err != nil || !home.IndexedFromStart(s.DataDir) {
		t.Fatal("not marked")
	}
	s.IndexUTXOs = false
	if err := markIndex(s); err != nil || home.IndexedFromStart(s.DataDir) {
		t.Errorf("indexing off: %v, still marked %v", err, home.IndexedFromStart(s.DataDir))
	}
}
