// Package quaiaddr checks Quai and Qi addresses and names zones.
//
// A Quai address encodes its shard in its first byte (high nibble region,
// low nibble zone) and its ledger in the top bit of its second byte: clear
// for Quai, set for Qi. This mirrors go-quai's common.LocationFromAddressBytes
// and Address.IsInQiLedgerScope.
package quaiaddr

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Ledger is Quai or Qi.
type Ledger int

const (
	Quai Ledger = iota
	Qi
)

func (l Ledger) String() string {
	if l == Qi {
		return "Qi"
	}
	return "Quai"
}

var regions = []string{"cyprus", "paxos", "hydra"}

// Zone is a region and zone index, e.g. {0, 0} for cyprus1.
type Zone struct{ Region, Zone int }

// Name is go-quai's name for the zone, e.g. "cyprus1".
func (z Zone) Name() string {
	if z.Region < 0 || z.Region >= len(regions) {
		return fmt.Sprintf("zone-%d-%d", z.Region, z.Zone)
	}
	return fmt.Sprintf("%s%d", regions[z.Region], z.Zone+1)
}

// Slices is the value of go-quai's --node.slices for this zone alone.
func (z Zone) Slices() string { return fmt.Sprintf("[%d %d]", z.Region, z.Zone) }

// RPCPorts are go-quai's HTTP RPC ports for prime, this zone's region and
// this zone (cmd/utils.GetHttpPort with the default start port 9001).
func (z Zone) RPCPorts() (prime, region, zone int) {
	return 9001, 9002 + z.Region, 9200 + 20*z.Region + z.Zone
}

// Zones lists the nine zones.
func Zones() []Zone {
	var out []Zone
	for r := range 3 {
		for zz := range 3 {
			out = append(out, Zone{r, zz})
		}
	}
	return out
}

// Check parses addr and checks it belongs to ledger and zone.
func Check(addr string, ledger Ledger, zone Zone) error {
	s := strings.TrimPrefix(strings.TrimSpace(addr), "0x")
	if len(s) != 40 {
		return fmt.Errorf("an address is 0x followed by 40 hex digits")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("an address is 0x followed by 40 hex digits")
	}
	got := Zone{int(b[0] >> 4), int(b[0] & 0x0f)}
	isQi := b[1] > 127
	switch {
	case ledger == Qi && !isQi:
		return fmt.Errorf("this is a Quai address; a Qi address is needed here")
	case ledger == Quai && isQi:
		return fmt.Errorf("this is a Qi address; a Quai address is needed here")
	case got != zone:
		return fmt.Errorf("this address is in %s, but the node runs %s", got.Name(), zone.Name())
	}
	return nil
}
