// Package bundle holds the files embedded into quai-node at build time for
// its OS/arch (go-quai, its VERSION and params, and optionally quai-dash),
// and unpacks them into a home directory.
//
// The payload is a gzipped tar written by scripts/build-payload.sh:
//
//	manifest.json
//	go-quai[.exe]
//	VERSION
//	params/genesis_alloc.json
//	params/forfeiture_addresses.json
//	quai-dash[.exe]          (only where a quai-dash build exists)
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mpoletiek/quai-node/internal/home"
)

//go:embed all:payload
var payloadFS embed.FS

const payloadName = "payload/payload.tar.gz"

// Manifest describes what a payload carries.
type Manifest struct {
	GOOS          string   `json:"goos"`
	GOARCH        string   `json:"goarch"`
	GoQuaiVersion string   `json:"go_quai_version"`
	GoQuaiCommit  string   `json:"go_quai_commit"`
	GoQuaiPatches []string `json:"go_quai_patches,omitempty"` // quai-node's patches/go-quai it was built with
	DashVersion   string   `json:"dash_version,omitempty"`    // empty: no quai-dash for this platform
}

// ErrNoPayload means this quai-node was built without a payload.
var ErrNoPayload = errors.New("this quai-node build carries no go-quai (built without scripts/build-payload.sh)")

func raw() ([]byte, error) {
	b, err := payloadFS.ReadFile(payloadName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoPayload
	}
	return b, err
}

// Digest identifies the embedded payload; a home records the digest it was
// unpacked from, so an unchanged payload isn't unpacked again.
func Digest() (string, error) {
	b, err := raw()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Read returns the embedded payload's manifest.
func Read() (Manifest, error) {
	var m Manifest
	err := walk(func(name string, _ *tar.Header, r io.Reader) error {
		if name != "manifest.json" {
			return nil
		}
		return json.NewDecoder(r).Decode(&m)
	})
	if err != nil {
		return m, err
	}
	if m.GOOS != runtime.GOOS || m.GOARCH != runtime.GOARCH {
		return m, fmt.Errorf("payload is for %s/%s, this is %s/%s", m.GOOS, m.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	return m, nil
}

func walk(fn func(name string, h *tar.Header, r io.Reader) error) error {
	b, err := raw()
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("payload: %w", err)
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg || strings.HasPrefix(name, "..") || path.IsAbs(name) {
			continue
		}
		if err := fn(name, h, tr); err != nil {
			return err
		}
	}
}

// Install unpacks the payload into h: binaries into bin/, VERSION and params/
// at the top. Files are written to a temporary name and renamed, so a running
// node keeps its old binary until it restarts. It returns the manifest.
func Install(h home.Home) (Manifest, error) {
	m, err := Read()
	if err != nil {
		return m, err
	}
	if err := os.MkdirAll(h.BinDir(), 0o755); err != nil {
		return m, err
	}
	err = walk(func(name string, hdr *tar.Header, r io.Reader) error {
		var dst string
		switch {
		case name == "manifest.json":
			return nil
		case strings.HasPrefix(name, "params/") || name == "VERSION":
			dst = filepath.Join(h.Dir, filepath.FromSlash(name))
		default: // a binary
			dst = filepath.Join(h.BinDir(), name)
		}
		return writeFile(dst, r, fs.FileMode(hdr.Mode).Perm())
	})
	if err != nil {
		return m, err
	}
	d, err := Digest()
	if err != nil {
		return m, err
	}
	return m, os.WriteFile(h.BundleStamp(), []byte(d+"\n"), 0o644)
}

// Current reports whether h was unpacked from this exact payload.
func Current(h home.Home) bool {
	d, err := Digest()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(h.BundleStamp())
	return err == nil && strings.TrimSpace(string(b)) == d
}

func writeFile(dst string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return replace(tmp, dst)
}

// replace renames tmp over dst. Windows can't replace a running .exe, but it
// can rename one out of the way.
func replace(tmp, dst string) error {
	err := os.Rename(tmp, dst)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	old := dst + ".old"
	os.Remove(old)
	if err := os.Rename(dst, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s (is it running?): %w", dst, err)
	}
	return os.Rename(tmp, dst)
}

// CopySelf copies the running quai-node into h's bin/, so services point at
// a stable path rather than wherever the download landed.
func CopySelf(h home.Home) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if s, err := filepath.EvalSymlinks(self); err == nil {
		self = s
	}
	dst := h.QuaiNode()
	if same(self, dst) {
		return nil
	}
	f, err := os.Open(self)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeFile(dst, f, 0o755)
}

func same(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
