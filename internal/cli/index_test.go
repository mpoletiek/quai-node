package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/settings"
	"github.com/mpoletiek/quai-node/internal/snapshot"
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
	if err := checkIndex(s, nil); err != nil {
		t.Fatalf("empty data dir: %v", err)
	}
	if err := markIndex(s); err != nil || !home.IndexedFromStart(s.DataDir) {
		t.Fatalf("markIndex on an empty chain: %v, marked %v", err, home.IndexedFromStart(s.DataDir))
	}
	chainIn(t, s.DataDir)
	if err := checkIndex(s, nil); err != nil {
		t.Fatalf("a chain indexed from its start: %v", err)
	}

	// A snapshot that carries the index will do (or one not known yet: the
	// restore checks it); one that doesn't, won't.
	s.RestoreSnapshot = true
	indexed := &snapshot.Manifest{Indexed: true}
	old := &snapshot.Manifest{}
	for _, snap := range []*snapshot.Manifest{nil, indexed} {
		if err := checkIndex(s, snap); err != nil {
			t.Errorf("index node with an indexed snapshot: %v", err)
		}
	}
	if err := checkIndex(s, old); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("index node with an unindexed snapshot: %v", err)
	}
	s.RestoreSnapshot = false

	// Deleting the chain forgets the mark.
	if err := home.RemoveChain(s.DataDir); err != nil || home.IndexedFromStart(s.DataDir) {
		t.Fatalf("RemoveChain: %v, still marked %v", err, home.IndexedFromStart(s.DataDir))
	}

	// A chain made without indexing can't become an index node's, unless
	// it's deleted.
	chainIn(t, s.DataDir)
	if err := checkIndex(s, nil); err == nil || !strings.Contains(err.Error(), "--from-scratch") || !strings.Contains(err.Error(), "--snapshot") {
		t.Errorf("unindexed chain: %v", err)
	}
	s.RestoreSnapshot = true
	if err := checkIndex(s, indexed); err != nil {
		t.Errorf("unindexed chain, replaced by an indexed snapshot: %v", err)
	}
	s.RestoreSnapshot, s.WipeChain = false, true
	if err := checkIndex(s, nil); err != nil {
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
