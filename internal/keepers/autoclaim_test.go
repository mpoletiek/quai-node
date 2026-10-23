package keepers

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPeerID(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	// computed with go-libp2p's peer.IDFromPublicKey
	if got, want := PeerID(pub), "12D3KooWA4Xop1JaT3MHxwYMkCepYsv4iPVopMXwCz5iHYdBfeSB"; got != want {
		t.Fatalf("PeerID = %s, want %s", got, want)
	}
}

// A fake Keepers: the tag is unknown until loggedIn, a claim is refused while refuse is set, and an
// accepted claim records the signer as the tag's claim_node.
type fakeKeepers struct {
	mu        sync.Mutex
	loggedIn  bool
	refuse    string
	claimNode string
	claims    int
}

func (f *fakeKeepers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/v1/status":
		if !f.loggedIn {
			w.Write([]byte(`{"ok": false}`))
			return
		}
		fmt.Fprintf(w, `{"ok": true, "node": {"tag": "MyNode", "status": "pending", "claim_node": %q}}`, f.claimNode)
	case "/v1/claim":
		f.claims++
		if f.refuse != "" {
			http.Error(w, "✗ "+f.refuse, http.StatusConflict)
			return
		}
		k, _ := hex.DecodeString(r.FormValue("k"))
		f.claimNode = PeerID(k)
		w.Write([]byte("✓ claimed MyNode"))
	}
}

func TestAutoClaim(t *testing.T) {
	key, pub := writeKey(t)
	f := &fakeKeepers{refuse: "We don't see node … at the address MyNode reports from"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	base = srv.URL + "/v1"
	defer func() { base = API }()
	defer func(a, b, c, d time.Duration) { claimFirst, claimPending, claimRetry, claimHeld = a, b, c, d }(
		claimFirst, claimPending, claimRetry, claimHeld)
	claimFirst, claimPending, claimRetry, claimHeld = time.Millisecond, time.Millisecond, 5*time.Millisecond, time.Hour

	var mu sync.Mutex
	var lines []string
	logf := func(format string, a ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, a...))
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		AutoClaim(ctx, key, "MyNode", "s3cret", logf)
		close(done)
	}()
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
			f.mu.Lock()
			ok := cond()
			f.mu.Unlock()
			if ok {
				return
			}
		}
		t.Fatal("timed out waiting for " + what)
	}

	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	if f.claims != 0 {
		t.Error("claimed before the tag logged in")
	}
	f.loggedIn = true
	f.mu.Unlock()
	waitFor("a refused claim, retried", func() bool { return f.claims >= 2 })
	f.mu.Lock()
	f.refuse = ""
	f.mu.Unlock()
	waitFor("the claim", func() bool { return f.claimNode == PeerID(pub) })
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	n := f.claims
	f.mu.Unlock()
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	refusals := 0
	for _, l := range lines {
		if strings.Contains(l, "refused") {
			refusals++
		}
	}
	if refusals != 1 || !strings.Contains(lines[len(lines)-1], "✓ claimed MyNode") {
		t.Errorf("log: %q", lines)
	}
	time.Sleep(10 * time.Millisecond)
	if f.claims != n {
		t.Error("kept claiming after the claim landed")
	}
}
