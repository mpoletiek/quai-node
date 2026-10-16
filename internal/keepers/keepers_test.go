package keepers

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestTagAndFlag(t *testing.T) {
	for _, ok := range []string{"abc", "Node_1.x-y", "a23456789012345678901234"} {
		if CheckTag(ok) != nil {
			t.Errorf("rejected %q", ok)
		}
	}
	for _, bad := range []string{"ab", "-abc", "has space", "a234567890123456789012345", "émile"} {
		if CheckTag(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got := StatsFlag("tag", "sec", 9200); got != "tag:sec@keepers.quainode.org" {
		t.Errorf("StatsFlag = %s", got)
	}
	if got := StatsFlag("tag", "sec", 9201); got != "tag:sec@keepers.quainode.org/9201" {
		t.Errorf("StatsFlag = %s", got)
	}
	if s := NewSecret(); len(s) != 26 || s == NewSecret() {
		t.Errorf("NewSecret = %q", s)
	}
}

func TestReadKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	file := append([]byte{0x08, 0x01, 0x12, 0x40}, priv.Seed()...)
	file = append(file, pub...)
	p := filepath.Join(t.TempDir(), "private.key")
	os.WriteFile(p, file, 0o600)
	got, err := readKey(p)
	if err != nil || !got.Equal(priv) {
		t.Fatalf("readKey = %v, %v", got, err)
	}
	file[40] ^= 1 // corrupt the public half
	os.WriteFile(p, file, 0o600)
	if _, err := readKey(p); err == nil {
		t.Error("accepted a key whose halves don't match")
	}
	os.WriteFile(p, file[:60], 0o600)
	if _, err := readKey(p); err == nil {
		t.Error("accepted a short key")
	}
}

func writeKey(t *testing.T) (string, ed25519.PublicKey) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	file := append([]byte{0x08, 0x01, 0x12, 0x40}, priv.Seed()...)
	file = append(file, pub...)
	p := filepath.Join(t.TempDir(), "private.key")
	if err := os.WriteFile(p, file, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, pub
}

func TestClaim(t *testing.T) {
	key, pub := writeKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/claim" {
			http.Error(w, "✗ wrong endpoint", http.StatusNotFound)
			return
		}
		m, k, s := r.FormValue("m"), r.FormValue("k"), r.FormValue("s")
		if !regexp.MustCompile(`^quainode\.org keeper claim v1 tag=MyNode ts=\d+$`).MatchString(m) {
			http.Error(w, "✗ bad message "+m, http.StatusBadRequest)
			return
		}
		kb, _ := hex.DecodeString(k)
		sb, _ := base64.StdEncoding.DecodeString(s)
		if hex.EncodeToString(pub) != k || !ed25519.Verify(kb, []byte(m), sb) {
			http.Error(w, "✗ bad signature", http.StatusBadRequest)
			return
		}
		w.Write([]byte("✓ claimed MyNode\n"))
	}))
	defer srv.Close()
	base = srv.URL + "/v1"
	defer func() { base = API }()

	got, err := Claim(context.Background(), key, "MyNode")
	if err != nil || got != "✓ claimed MyNode" {
		t.Fatalf("Claim = %q, %v", got, err)
	}
	if _, err := Claim(context.Background(), key, "x"); err == nil {
		t.Error("claimed an invalid tag")
	}
	base = srv.URL + "/nope"
	if _, err := Claim(context.Background(), key, "MyNode"); err == nil || err.Error() != "wrong endpoint" {
		t.Errorf("failed claim: %v", err)
	}
}

func TestLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Tag, Secret string }
		json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/v1/status" || in.Tag != "MyNode" || in.Secret != "s3cret" {
			w.Write([]byte(`{"ok": false}`))
			return
		}
		w.Write([]byte(`{"ok": true, "node": {
			"tag": "MyNode", "status": "verified", "status_reason": null, "status_at": "2026-10-16T20:00:00Z",
			"first_verified_at": "2026-10-16T20:00:00Z", "last_verified_at": "2026-10-16T20:00:00Z",
			"last_login_at": "2026-10-16T19:00:00Z", "last_report_at": "2026-10-16T20:00:00Z",
			"rpc_port": 9200, "version": "go-quai v0.56.1", "head_number": 10600000, "lag": 2,
			"node": "12D3KooWa", "claim": "claimed", "claim_node": "12D3KooWa",
			"checks": [
				{"ts": "2026-10-16T20:00:00Z", "trigger": "login", "outcome": "fail", "step_failed": 2, "reason": "rpc timeout",
				 "block_number": null, "rpc_ms": null, "proof_nodes": null},
				{"ts": "2026-10-16T19:45:00Z", "trigger": "report", "outcome": "pass", "step_failed": null, "reason": null,
				 "block_number": 10600000, "rpc_ms": 41, "proof_nodes": 14}
			]}}`))
	}))
	defer srv.Close()
	base = srv.URL + "/v1"
	defer func() { base = API }()

	st, err := Lookup(context.Background(), "MyNode", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "verified" || st.Claim != "claimed" || st.HeadNumber != 10600000 || st.Lag != 2 ||
		st.Node != "12D3KooWa" || st.RPCPort != 9200 || st.Version != "go-quai v0.56.1" || len(st.Checks) != 2 {
		t.Fatalf("Lookup = %+v", st)
	}
	if c := st.Checks[0]; c.Outcome != "fail" || c.Step() != "rpc" || c.Reason != "rpc timeout" {
		t.Errorf("check = %+v", c)
	}
	if c := st.Checks[1]; c.Outcome != "pass" || c.Step() != "" {
		t.Errorf("check = %+v", c)
	}
	if _, err := Lookup(context.Background(), "MyNode", "wrong"); !errors.Is(err, ErrUnknown) {
		t.Errorf("wrong secret: %v", err)
	}
}
