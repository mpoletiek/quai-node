package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// archive is a snapshot laid out as quikshot.dev's: a top directory, then
// go-quai's chain directories. It also carries entries Restore must ignore.
func archive(t *testing.T) []byte {
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	add := func(name, body string) {
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(name, "/") {
			h = &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	add("snap/", "")
	add("snap/zone-0-0/go-quai/chaindata/000001.sst", strings.Repeat("z", 100000))
	add("snap/prime/go-quai/chaindata/000001.sst", "prime")
	add("snap/notachain/x", "ignored")
	add("snap/../escape", "never")
	tw.Close()
	var zb bytes.Buffer
	zw, _ := zstd.NewWriter(&zb)
	zw.Write(tb.Bytes())
	zw.Close()
	return zb.Bytes()
}

// serve serves body with Range support; the first response is cut short
// after cut bytes when cut > 0, as a dropped connection would be.
func serve(t *testing.T, body []byte, cut int) *httptest.Server {
	var first atomic.Bool
	first.Store(true)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if rg := r.Header.Get("Range"); rg != "" {
			start, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		part := body[start:]
		if cut > 0 && first.CompareAndSwap(true, false) {
			w.Write(part[:cut])
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				c.Close()
			}
			return
		}
		w.Write(part)
	}))
}

func manifestFor(body []byte, url string) Manifest {
	sum := sha256.Sum256(body)
	return Manifest{SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), ArchivePath: "a.tar.zst",
		Contexts: []string{"prime", "zone-0-0"}, ExpandedSizeBytes: 1}
}

func restore(t *testing.T, srv *httptest.Server, m Manifest, dir string) error {
	t.Helper()
	old := Base
	defer func() { setBase(old) }()
	setBase(srv.URL)
	return Restore(context.Background(), m, dir, func(Progress) {})
}

func TestRestore(t *testing.T) {
	body := archive(t)
	for _, cut := range []int{0, len(body) / 3} {
		srv := serve(t, body, cut)
		dir := t.TempDir()
		// Old leveldb chain data, which the snapshot replaces.
		os.MkdirAll(filepath.Join(dir, "zone-0-0", "go-quai", "chaindata"), 0o755)
		os.WriteFile(filepath.Join(dir, "zone-0-0", "go-quai", "chaindata", "000039.ldb"), []byte("old"), 0o644)
		os.MkdirAll(filepath.Join(dir, "0xff2b"), 0o755) // go-quai's p2p state: kept

		if err := restore(t, srv, manifestFor(body, srv.URL), dir); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "zone-0-0", "go-quai", "chaindata", "000001.sst"))
		if err != nil || len(b) != 100000 {
			t.Errorf("cut %d: zone sst: %d bytes, %v", cut, len(b), err)
		}
		for _, gone := range []string{"zone-0-0/go-quai/chaindata/000039.ldb", "notachain", ".snapshot-partial", "../escape"} {
			if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
				t.Errorf("cut %d: %s exists", cut, gone)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "0xff2b")); err != nil {
			t.Errorf("cut %d: removed go-quai's p2p state", cut)
		}
		srv.Close()
	}
}

func TestRestoreBadChecksumKeepsOldData(t *testing.T) {
	body := archive(t)
	srv := serve(t, body, 0)
	defer srv.Close()
	dir := t.TempDir()
	old := filepath.Join(dir, "zone-0-0", "go-quai", "chaindata", "000039.ldb")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte("old"), 0o644)
	m := manifestFor(body, srv.URL)
	m.SHA256 = strings.Repeat("0", 64)
	if err := restore(t, srv, m, dir); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Restore = %v, want a checksum error", err)
	}
	if b, _ := os.ReadFile(old); string(b) != "old" {
		t.Error("old chain data was touched")
	}
	if _, err := os.Stat(filepath.Join(dir, ".snapshot-partial")); err == nil {
		t.Error("staging left behind")
	}
}

func TestManifestCheck(t *testing.T) {
	good := Manifest{SchemaVersion: 1, Network: "quai-mainnet", Database: "pebble", Compression: "zstd", Verified: true,
		ArchiveIntegrity: true, SHA256: strings.Repeat("a", 64), ArchivePath: "x", ExpandedSizeBytes: 1, Contexts: []string{"prime", "region-0", "zone-0-0"}}
	if err := good.check(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []func(*Manifest){
		func(m *Manifest) { m.Verified = false },
		func(m *Manifest) { m.Database = "leveldb" },
		func(m *Manifest) { m.Contexts = []string{"../etc"} },
		func(m *Manifest) { m.Contexts = nil },
	} {
		m := good
		bad(&m)
		if m.check() == nil {
			t.Errorf("accepted %+v", m)
		}
	}
}

func TestLeftover(t *testing.T) {
	dir := t.TempDir()
	if n := Leftover(dir); n != 0 {
		t.Fatalf("nothing there: %d", n)
	}
	f := filepath.Join(staging(dir), "zone-0-0", "go-quai", "chaindata", "000001.sst")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := Leftover(dir); n != 0 {
		t.Fatalf("written just now, maybe a restore running: %d", n)
	}
	old := time.Now().Add(-time.Hour)
	filepath.WalkDir(staging(dir), func(p string, _ fs.DirEntry, _ error) error { return os.Chtimes(p, old, old) })
	if n := Leftover(dir); n != 1000 {
		t.Fatalf("an hour old: %d, want 1000", n)
	}
	if err := RemoveLeftover(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staging(dir)); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
}
