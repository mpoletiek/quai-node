package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
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
		part := ranged(w, r, body)
		if cut > 0 && first.CompareAndSwap(true, false) {
			w.Write(part[:min(cut, len(part)-1)])
			hangUp(w)
			return
		}
		w.Write(part)
	}))
}

// ranged answers r's "bytes=a-b" or "bytes=a-" Range header, or its lack,
// with the headers for that part of body, and returns the part.
func ranged(w http.ResponseWriter, r *http.Request, body []byte) []byte {
	rg := r.Header.Get("Range")
	if rg == "" {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		return body
	}
	from, to, _ := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
	start, _ := strconv.Atoi(from)
	end := len(body) - 1
	if to != "" {
		end, _ = strconv.Atoi(to)
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	return body[start : end+1]
}

func hangUp(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		c, _, _ := hj.Hijack()
		c.Close()
	}
}

// small makes downloads use many small chunks and quick retries.
func small(t *testing.T) {
	oc, ow, os, or := chunkSize, window, stallTimeout, retryWait
	chunkSize, window, stallTimeout, retryWait = 1000, 4, 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { chunkSize, window, stallTimeout, retryWait = oc, ow, os, or })
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
	small(t)
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

func randomBody(n int) []byte {
	b := make([]byte, n)
	r := rand.NewChaCha8([32]byte{1})
	r.Read(b)
	return b
}

func fetchAll(t *testing.T, url string, total int) ([]byte, int64, error) {
	t.Helper()
	var count atomic.Int64
	body := download(context.Background(), url, int64(total), &count)
	defer body.Close()
	got, err := io.ReadAll(body)
	return got, count.Load(), err
}

// The chunks arrive out of order, some cut short, some stalled; the
// stream comes out whole and in order.
func TestDownloadInOrder(t *testing.T) {
	small(t)
	body := randomBody(50_000)
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		part := ranged(w, r, body)
		time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)
		switch i % 7 {
		case 3: // dropped partway
			w.Write(part[:len(part)/2])
			hangUp(w)
			return
		case 5: // stalled partway
			w.Write(part[:len(part)/3])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Write(part)
	}))
	defer srv.Close()
	got, count, err := fetchAll(t, srv.URL, len(body))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("got %d bytes, not the body", len(got))
	}
	if count != int64(len(body)) {
		t.Errorf("counted %d bytes received, want %d (a retry resumes, it doesn't start over)", count, len(body))
	}
}

func TestDownloadNeedsRanges(t *testing.T) {
	small(t)
	body := randomBody(5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body) // 200, the whole file, whatever was asked
	}))
	defer srv.Close()
	if _, _, err := fetchAll(t, srv.URL, len(body)); err == nil || !strings.Contains(err.Error(), "HTTP 200") {
		t.Fatalf("err = %v, want an HTTP 200 error", err)
	}
}

func TestDownloadCancel(t *testing.T) {
	small(t)
	body := randomBody(50_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ranged(w, r, body)[:10])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	b := download(ctx, srv.URL, int64(len(body)), &count)
	time.AfterFunc(50*time.Millisecond, cancel)
	done := make(chan error)
	go func() { _, err := io.ReadAll(b); done <- err }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read didn't return after cancel")
	}
	b.Close()
}
