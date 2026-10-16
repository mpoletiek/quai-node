//go:build windows

package services

import "strings"

// containsPath reports whether def mentions the path p as a whole path;
// Windows paths compare without regard to case.
func containsPath(def, p string) bool {
	def, p = strings.ToLower(def), strings.ToLower(p)
	for i := 0; ; {
		j := strings.Index(def[i:], p)
		if j < 0 {
			return false
		}
		end := i + j + len(p)
		if end == len(def) || strings.ContainsRune(" \t\n\"'", rune(def[end])) {
			return true
		}
		i = end
	}
}
