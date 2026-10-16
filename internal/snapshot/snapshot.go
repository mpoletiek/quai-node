// Package snapshot restores a node's chain data from quikshot.dev, an
// independent community service (not affiliated with the Quai Network team)
// that publishes verified mainnet snapshots.
//
// A snapshot is a zstd-compressed tar of go-quai's Pebble databases for
// prime, region-0 and zone-0-0, laid out as go-quai's data directory is:
//
//	<snapshot name>/zone-0-0/go-quai/chaindata/…
//
// Restore downloads it over several connections at once and streams it, in
// order, through a SHA-256 hash and the zstd and tar decoders into a
// staging directory beside the chain data, so it needs the unpacked size
// free, not the download's on top. Only when the stream ends
// with the published checksum are the staged chains moved into place. The
// node must then run with the Pebble engine (--node.db-engine pebble).
package snapshot

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Site is quikshot.dev; Base serves its files (the site's app.js names
// it), whose latest.json describes the newest snapshot (a variable for
// tests).
const Site = "https://quikshot.dev"

var Base = "https://downloads.quikshot.dev"

func setBase(b string) { Base = b }

// Manifest is latest.json (schema 1).
type Manifest struct {
	SchemaVersion     int              `json:"schema_version"`
	Network           string           `json:"network"`
	Created           time.Time        `json:"created"`
	Archive           string           `json:"archive"`
	ArchivePath       string           `json:"archive_path"`
	ChecksumPath      string           `json:"checksum_path"`
	SizeBytes         int64            `json:"size_bytes"`
	ExpandedSizeBytes int64            `json:"expanded_size_bytes"`
	SHA256            string           `json:"sha256"`
	Compression       string           `json:"compression"`
	Database          string           `json:"database"`
	Verified          bool             `json:"verified"`
	ArchiveIntegrity  bool             `json:"archive_integrity"`
	RestoreTested     bool             `json:"restore_tested"`
	BlockHeights      map[string]int64 `json:"block_heights"`
	Contexts          []string         `json:"contexts"`
}

// URL is the archive's download URL.
func (m Manifest) URL() string { return Base + "/" + m.ArchivePath }

// Age is how old the snapshot is, roughly: "3 days", "7 hours".
func (m Manifest) Age() string {
	d := time.Since(m.Created)
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// ZoneHeight is the snapshot's zone-0-0 height.
func (m Manifest) ZoneHeight() int64 { return m.BlockHeights["zone-0-0"] }

var client = &http.Client{Timeout: 20 * time.Second}

// Latest fetches and checks the newest snapshot's manifest. It is for
// Quai mainnet (colosseum) nodes running cyprus1 (zone 0-0).
func Latest(ctx context.Context) (Manifest, error) {
	var m Manifest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Base+"/latest.json", nil)
	if err != nil {
		return m, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("latest.json: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("latest.json: %w", err)
	}
	return m, m.check()
}

func (m Manifest) check() error {
	switch {
	case m.SchemaVersion != 1:
		return fmt.Errorf("snapshot manifest schema %d isn't one quai-node reads", m.SchemaVersion)
	case m.Network != "quai-mainnet":
		return fmt.Errorf("snapshot is for %q, not quai-mainnet", m.Network)
	case m.Database != "pebble" || m.Compression != "zstd":
		return fmt.Errorf("snapshot is %s/%s, quai-node restores pebble/zstd", m.Database, m.Compression)
	case !m.Verified || !m.ArchiveIntegrity:
		return errors.New("quikshot.dev hasn't verified the latest snapshot")
	case len(m.SHA256) != 64 || m.ArchivePath == "" || m.ExpandedSizeBytes <= 0:
		return errors.New("snapshot manifest is incomplete")
	}
	for _, c := range m.Contexts {
		if !validContext(c) {
			return fmt.Errorf("snapshot has an unexpected chain %q", c)
		}
	}
	if len(m.Contexts) == 0 {
		return errors.New("snapshot manifest lists no chains")
	}
	return nil
}

// validContext is a chain directory name go-quai uses: prime, region-N,
// zone-N-M.
func validContext(c string) bool {
	if c == "prime" {
		return true
	}
	var a, b int
	if n, _ := fmt.Sscanf(c, "region-%d", &a); n == 1 && c == fmt.Sprintf("region-%d", a) {
		return true
	}
	n, _ := fmt.Sscanf(c, "zone-%d-%d", &a, &b)
	return n == 2 && c == fmt.Sprintf("zone-%d-%d", a, b)
}

// Progress is reported as the download proceeds.
type Progress struct {
	Downloaded, Total int64
	Written           int64 // bytes unpacked
	Started           time.Time
}

// staging is where Restore unpacks a download until it checks out.
func staging(dataDir string) string { return filepath.Join(dataDir, ".snapshot-partial") }

// Leftover is the size of an unfinished restore left in dataDir (the
// process was killed, or the machine went down, mid-download), or 0. A
// restore always starts over, so a leftover is of no use; one written to
// in the last few minutes may be a restore still running, and isn't one.
func Leftover(dataDir string) int64 {
	var size int64
	var newest time.Time
	err := filepath.WalkDir(staging(dataDir), func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if fi, err := e.Info(); err == nil {
			if e.Type().IsRegular() {
				size += fi.Size()
			}
			if fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
		return nil
	})
	if err != nil || time.Since(newest) < 5*time.Minute {
		return 0
	}
	return max(size, 1)
}

// RemoveLeftover deletes an unfinished restore from dataDir.
func RemoveLeftover(dataDir string) error { return os.RemoveAll(staging(dataDir)) }

// Restore downloads m into dataDir's chain directories, replacing them.
// The node must be stopped. progress is called about once a second.
func Restore(ctx context.Context, m Manifest, dataDir string, progress func(Progress)) error {
	staging := staging(dataDir)
	if err := os.RemoveAll(staging); err != nil { // an interrupted earlier try
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(staging)
		}
	}()

	var downloaded, written atomic.Int64
	started := time.Now()
	report := func() {
		progress(Progress{Downloaded: downloaded.Load(), Total: m.SizeBytes, Written: written.Load(), Started: started})
	}
	body := download(ctx, m.URL(), m.SizeBytes, &downloaded)
	defer body.Close()
	hash := sha256.New()
	tee := io.TeeReader(body, hash)

	dec, err := zstd.NewReader(tee, zstd.WithDecoderConcurrency(0))
	if err != nil {
		return err
	}
	defer dec.Close()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				report()
			}
		}
	}()

	if err := untar(dec, staging, m.Contexts, &written); err != nil {
		return err
	}
	// Read to the end of the download, so the hash covers it all: the rest
	// of the zstd stream (the tar's padding), then, once the decoder's
	// goroutine has stopped reading it, anything after.
	if _, err := io.Copy(io.Discard, dec); err != nil {
		return fmt.Errorf("snapshot archive: %w", err)
	}
	dec.Close()
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != strings.ToLower(m.SHA256) {
		return fmt.Errorf("the download's SHA-256 is %s, not the published %s; nothing was replaced", got, m.SHA256)
	}
	report()

	// Verified: replace each chain directory with its staged copy.
	for _, c := range m.Contexts {
		src := filepath.Join(staging, c)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("the snapshot lacks %s", c)
		}
	}
	for _, c := range m.Contexts {
		dst := filepath.Join(dataDir, c)
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(staging, c), dst); err != nil {
			return err
		}
	}
	ok = true
	return os.RemoveAll(staging)
}

// untar writes the archive's chain directories under dir, dropping the
// archive's top-level directory. It writes only regular files and
// directories inside the listed chains, and refuses paths that would leave
// dir.
func untar(r io.Reader, dir string, contexts []string, written *atomic.Int64) error {
	allowed := map[string]bool{}
	for _, c := range contexts {
		allowed[c] = true
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("snapshot archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		parts := strings.SplitN(name, "/", 2) // <snapshot name>/<rest>
		if len(parts) < 2 || strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
			continue // the top directory itself, or nothing of ours
		}
		rel := parts[1]
		if !allowed[strings.SplitN(rel, "/", 2)[0]] {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if !strings.HasPrefix(dst, filepath.Clean(dir)+string(filepath.Separator)) {
			return fmt.Errorf("snapshot archive: %q leaves the data directory", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, tr)
			written.Add(n)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

// Need is the free space a restore takes: the unpacked size, plus 5% for
// the chain's growth. The chains it replaces are removed only once the new
// ones are staged and verified, so their space doesn't count.
func (m Manifest) Need() int64 { return m.ExpandedSizeBytes + m.ExpandedSizeBytes/20 }
