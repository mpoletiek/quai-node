package cli

import (
	"errors"
	"fmt"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/settings"
)

// An index node's index of Qi UTXOs must cover the chain from its first
// block: go-quai indexes only the blocks it processes while indexing is
// on, and quai-node doesn't reindex. A marker in the data directory
// (home.IndexedFromStart) says the chain there was made that way.

var errIndexSnapshot = errors.New("an index node indexes the chain from its first block, so it syncs from the network: it can't start from quikshot.dev's snapshot")

// checkIndex turns away settings that would leave an index node with an
// incomplete index.
func checkIndex(s settings.Settings) error {
	switch {
	case !s.IndexUTXOs:
		return nil
	case s.RestoreSnapshot:
		return errIndexSnapshot
	case !s.WipeChain && home.ChainUse(s.DataDir) > 0 && !home.IndexedFromStart(s.DataDir):
		return fmt.Errorf("the chain in %s wasn't indexed from its first block, and an index node's must be: "+
			"to make this an index node, delete it and sync from scratch (quai-node setup --index-utxos --from-scratch)", s.DataDir)
	}
	return nil
}

// markIndex records, before the node starts, whether its chain is indexed
// from the first block: an index node starting an empty chain is; turning
// indexing off ends that.
func markIndex(s settings.Settings) error {
	if !s.IndexUTXOs {
		return home.UnmarkIndexed(s.DataDir)
	}
	if home.ChainUse(s.DataDir) == 0 {
		return home.MarkIndexed(s.DataDir)
	}
	return nil
}
