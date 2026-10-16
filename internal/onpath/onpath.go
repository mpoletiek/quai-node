// Package onpath puts the quai-node command on the user's PATH, pointing at
// the copy setup keeps in the home's bin/, and takes it off again.
package onpath

import (
	"os"
	"path/filepath"
	"strings"
)

// inPath reports whether dir is one of PATH's entries.
func inPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" && filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// Result says what Add did, for setup to print.
type Result struct {
	Path string // where the command is now (the link, or the bin dir on Windows)
	Note string // what the user still has to do, if anything
}

func trim(s string) string { return strings.TrimSpace(s) }
