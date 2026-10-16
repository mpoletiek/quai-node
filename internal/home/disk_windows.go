//go:build windows

package home

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeBytes is the space free to this user on the volume holding path, or on
// its nearest existing parent.
func FreeBytes(path string) (uint64, error) {
	p := path
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(u, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
