package keepers

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Waits between AutoClaim's looks at the Keepers; variables for tests.
var (
	claimFirst   = time.Minute      // go-quai logs in a little after it starts
	claimPending = 2 * time.Minute  // not logged in yet, or just claimed
	claimRetry   = 10 * time.Minute // a refused claim, or the Keepers unreachable
	claimHeld    = 30 * time.Minute // claimed by this node: look again now and then
)

// AutoClaim keeps tag claimed by the node whose libp2p key is keyFile, for
// as long as ctx lasts. The Keepers put a tag on the roll only once it's
// claimed, so this is what gets a quai-node on the roll without a step by
// hand: once the tag has logged in, it signs and sends the claim, and
// does again whenever the claim isn't this node's (after a move, the
// Keepers accept it once the old node has been off the roll for 7 days).
// logf gets a line when a claim lands and for each new kind of refusal.
func AutoClaim(ctx context.Context, keyFile, tag, secret string, logf func(format string, a ...any)) {
	var said string
	say := func(s string) {
		if s != said {
			logf("%s", s)
			said = s
		}
	}
	wait := claimFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		st, err := Lookup(ctx, tag, secret)
		if errors.Is(err, ErrUnknown) {
			wait = claimPending
			continue
		}
		if err != nil {
			wait = claimRetry
			continue
		}
		priv, err := readKey(keyFile)
		if err != nil {
			say(fmt.Sprintf("keepers: can't claim %s yet: %v", tag, err))
			wait = claimRetry
			continue
		}
		if st.ClaimNode == PeerID(priv.Public().(ed25519.PublicKey)) {
			wait = claimHeld
			continue
		}
		text, err := Claim(ctx, keyFile, tag)
		if err != nil {
			say(fmt.Sprintf("keepers: claim for %s refused: %v (trying again in %s)", tag, err, claimRetry))
			wait = claimRetry
			continue
		}
		say("keepers: " + text)
		wait = claimPending
	}
}

// PeerID is the libp2p peer ID of an Ed25519 public key: the base58 of
// an identity multihash (00 24) over the protobuf-encoded key (08 01 12 20).
func PeerID(pub ed25519.PublicKey) string {
	b := append([]byte{0x00, 0x24, 0x08, 0x01, 0x12, 0x20}, pub...)
	return base58(b)
}

const b58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58(b []byte) string {
	n := new(big.Int).SetBytes(b)
	var out []byte
	for mod, radix := new(big.Int), big.NewInt(58); n.Sign() > 0; {
		n.DivMod(n, radix, mod)
		out = append(out, b58[mod.Int64()])
	}
	for _, c := range b {
		if c != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
