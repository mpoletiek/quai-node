// Package status reads a node's state over its JSON-RPC, and the network's
// height from Quai's public RPC to show how far the sync has come.
package status

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Node is what status shows.
type Node struct {
	Reachable bool
	Height    uint64
	Peers     uint64
	Network   uint64 // the network's zone height, 0 when unknown
}

// Progress is Height/Network in [0,1], or -1 when the network's is unknown.
func (n Node) Progress() float64 {
	if n.Network == 0 {
		return -1
	}
	p := float64(n.Height) / float64(n.Network)
	if p > 1 {
		p = 1
	}
	return p
}

// PublicRPC is Quai's public zone RPC for a network and zone name such as
// "cyprus1", or "" for networks without one.
func PublicRPC(network, zone string) string {
	switch network {
	case "colosseum":
		return "https://rpc.quai.network/" + zone
	case "orchard":
		return "https://orchard.rpc.quai.network/" + zone
	}
	return ""
}

// Read queries the node at rpc and, when public isn't empty, the network.
func Read(ctx context.Context, rpc, public string) Node {
	var n Node
	if h, err := hexCall(ctx, rpc, "quai_blockNumber"); err == nil {
		n.Reachable, n.Height = true, h
	}
	if p, err := hexCall(ctx, rpc, "net_peerCount"); err == nil {
		n.Peers = p
	}
	if public != "" {
		if h, err := hexCall(ctx, public, "quai_blockNumber"); err == nil {
			n.Network = h
		}
	}
	return n
}

var client = &http.Client{Timeout: 5 * time.Second}

func hexCall(ctx context.Context, url, method string) (uint64, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": []any{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.Error != nil {
		return 0, fmt.Errorf("%s: %s", method, out.Error.Message)
	}
	return strconv.ParseUint(strings.TrimPrefix(out.Result, "0x"), 16, 64)
}

// Answers reports whether url answers an HTTP GET with a 2xx status.
func Answers(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode/100 == 2
}
