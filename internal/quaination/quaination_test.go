package quaination

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
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
	if got := StatsFlag("tag", "sec", 9200); got != "tag:sec@quaination.xyz/keepers" {
		t.Errorf("StatsFlag = %s", got)
	}
	if got := StatsFlag("tag", "sec", 9201); got != "tag:sec@quaination.xyz/keepers/9201" {
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
