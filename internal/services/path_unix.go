//go:build !windows

package services

import "strings"

// containsPath reports whether def mentions the absolute path p as a whole
// path, not as the start of a longer one (/a/quai-node vs /a/quai-node2).
func containsPath(def, p string) bool {
	for i := 0; ; {
		j := strings.Index(def[i:], p)
		if j < 0 {
			return false
		}
		end := i + j + len(p)
		if end == len(def) || strings.ContainsRune(" \t\n\"'<", rune(def[end])) {
			return true
		}
		i = end
	}
}
