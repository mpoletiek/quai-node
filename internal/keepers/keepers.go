// Package keepers reports the node to the Keepers on quainode.org
// (https://quainode.org/nodes) and claims it there.
//
// Reporting is go-quai's own quaistats client: --node.quaistats
// "TAG:SECRET@keepers.quainode.org". The first login reserves TAG for
// SECRET. The Keepers accept the stats only after checking the node through
// its Cyprus-1 RPC (a quai_getProof at its head), so that RPC must be
// reachable from the internet on TCP 9200.
//
// Claiming signs "quainode.org keeper claim v1 tag=TAG ts=UNIX" with the
// node's libp2p key (go-quai's private.key), which ties TAG to the node.
package keepers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	Host       = "keepers.quainode.org" // where go-quai's quaistats client logs in
	Page       = "https://quainode.org/nodes"
	StatusPage = "https://quainode.org/nodes/status"
	API        = "https://keepers.quainode.org/v1"
)

// TagRE is a valid tag (the tag in the Keepers' claim message).
var TagRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,23}$`)

// CheckTag explains an invalid tag.
func CheckTag(tag string) error {
	if !TagRE.MatchString(tag) {
		return errors.New("3 to 24 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return nil
}

// NewSecret is a random secret for the tag: 26 base32 characters (130 bits).
func NewSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// StatsFlag is go-quai's node.quaistats value. A zone RPC port other than
// the default 9200 goes in the path, as the Keepers ask.
func StatsFlag(tag, secret string, rpcPort int) string {
	host := Host
	if rpcPort != 9200 {
		host += fmt.Sprintf("/%d", rpcPort)
	}
	return tag + ":" + secret + "@" + host
}

var client = &http.Client{Timeout: 15 * time.Second}

// base is the read API Claim and Lookup call (API; tests point it elsewhere).
var base = API

// Claim signs the claim for tag with the libp2p key in keyFile and posts
// it. It returns the Keepers' answer, a line or two for the terminal.
func Claim(ctx context.Context, keyFile, tag string) (string, error) {
	if err := CheckTag(tag); err != nil {
		return "", fmt.Errorf("tag %q: %w", tag, err)
	}
	priv, err := readKey(keyFile)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("quainode.org keeper claim v1 tag=%s ts=%d", tag, time.Now().Unix())
	sig := ed25519.Sign(priv, []byte(msg))
	form := url.Values{
		"m": {msg},
		"k": {hex.EncodeToString(priv.Public().(ed25519.PublicKey))},
		"s": {base64.StdEncoding.EncodeToString(sig)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/claim", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(body))
	if resp.StatusCode/100 != 2 {
		return "", errors.New(strings.TrimPrefix(text, "✗ "))
	}
	return text, nil
}

// readKey reads go-quai's private.key: libp2p's MarshalPrivateKey of an
// Ed25519 key, 08 01 12 40 | seed(32) | public(32).
func readKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != 68 || !bytes.Equal(b[:4], []byte{0x08, 0x01, 0x12, 0x40}) {
		return nil, fmt.Errorf("%s isn't a go-quai Ed25519 node key", path)
	}
	priv := ed25519.NewKeyFromSeed(b[4:36])
	if !bytes.Equal(priv.Public().(ed25519.PublicKey), b[36:68]) {
		return nil, fmt.Errorf("%s: its public half doesn't match its seed", path)
	}
	return priv, nil
}

// Status is what the Keepers say about the node: the fields of their
// operator status, POST /v1/status.
type Status struct {
	Tag          string  `json:"tag"`
	Status       string  `json:"status"` // pending, verified or lapsed
	StatusReason string  `json:"status_reason"`
	LastLoginAt  string  `json:"last_login_at"`
	LastVerified string  `json:"last_verified_at"`
	LastReportAt string  `json:"last_report_at"`
	RPCPort      int     `json:"rpc_port"`
	Version      string  `json:"version"`
	HeadNumber   float64 `json:"head_number"`
	Lag          float64 `json:"lag"`
	Node         string  `json:"node"`       // the peer ID the Keepers verify at the reporting IP
	Claim        string  `json:"claim"`      // none, claimed, claimed-paused or mismatch
	ClaimNode    string  `json:"claim_node"` // the peer ID the claim was signed by
	Checks       []Check `json:"checks"`     // newest first
}

// Check is one of the Keepers' checks of the node.
type Check struct {
	TS         string `json:"ts"`
	Trigger    string `json:"trigger"`
	Outcome    string `json:"outcome"`     // pass, fail or inconclusive
	StepFailed int    `json:"step_failed"` // 1 census, 2 rpc, 3 head, 4 report/p2p, 5 proof, 6 claim; 0 none
	Reason     string `json:"reason"`
}

// Step names a failed check step.
func (c Check) Step() string {
	switch c.StepFailed {
	case 1:
		return "census"
	case 2:
		return "rpc"
	case 3:
		return "head"
	case 4:
		return "report/p2p"
	case 5:
		return "proof"
	case 6:
		return "claim"
	}
	return ""
}

// ErrUnknown means the Keepers don't know the tag with that secret: the
// node hasn't logged in yet (or the secret is wrong).
var ErrUnknown = errors.New("the Keepers haven't seen this tag log in yet")

// Lookup asks the Keepers for the node's status.
func Lookup(ctx context.Context, tag, secret string) (Status, error) {
	body, _ := json.Marshal(map[string]string{"tag": tag, "secret": secret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/status", bytes.NewReader(body))
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	var out struct {
		OK   bool    `json:"ok"`
		Node *Status `json:"node"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Status{}, fmt.Errorf("HTTP %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return Status{}, errors.New("too many requests; try again in a minute")
	}
	if !out.OK || out.Node == nil {
		return Status{}, ErrUnknown
	}
	return *out.Node, nil
}
