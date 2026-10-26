package cli

import (
	"errors"
	"fmt"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/settings"
	"github.com/mpoletiek/quai-node/internal/snapshot"
)

// An index node's index of Qi UTXOs must cover the chain from its first
// block: go-quai indexes only the blocks it processes while indexing is
// on, and quai-node doesn't reindex. A marker in the data directory
// (home.IndexedFromStart) says the chain there was made that way: synced
// from scratch by an index node, or restored from a snapshot that carries
// the index.

var errIndexSnapshot = errors.New("quikshot.dev's latest snapshot doesn't carry the Qi UTXO index from the chain's first block, so an index node can't start from it: it syncs from the network instead")

// checkIndex turns away settings that would leave an index node with an
// incomplete index. snap is quikshot.dev's latest snapshot, if known; a
// restore checks the one it downloads again.
func checkIndex(s settings.Settings, snap *snapshot.Manifest) error {
	switch {
	case !s.IndexUTXOs:
		return nil
	case s.RestoreSnapshot:
		if snap != nil && !snap.Indexed {
			return errIndexSnapshot
		}
	case !s.WipeChain && home.ChainUse(s.DataDir) > 0 && !home.IndexedFromStart(s.DataDir):
		how := "delete it and sync from scratch (quai-node setup --index-utxos --from-scratch)"
		if snapshotFits(s) {
			how = "replace it with quikshot.dev's snapshot (quai-node setup --index-utxos --snapshot), or " + how
		}
		return fmt.Errorf("the chain in %s wasn't indexed from its first block, and an index node's must be: to make this an index node, %s", s.DataDir, how)
	}
	return nil
}

// markIndex records, before the node starts, whether its chain is indexed
// from the first block: an index node starting an empty chain is (a
// restore marks its own); turning indexing off ends that.
func markIndex(s settings.Settings) error {
	if !s.IndexUTXOs {
		return home.UnmarkIndexed(s.DataDir)
	}
	if home.ChainUse(s.DataDir) == 0 {
		return home.MarkIndexed(s.DataDir)
	}
	return nil
}
