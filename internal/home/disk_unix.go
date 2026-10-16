//go:build !windows

package home

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// FreeBytes is the space free to this user on the filesystem holding path,
// or on its nearest existing parent.
func FreeBytes(path string) (uint64, error) {
	p := nearestExisting(path)
	var st unix.Statfs_t
	if err := unix.Statfs(p, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func nearestExisting(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}
