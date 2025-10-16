package main

import (
	"os"
	"path/filepath"
	"strings"
)

func sanitizeNoExt(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "merged"
	}
	return b.String()
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, `'`), "\n", " ")
}

func urlPath(name string) string {
	return strings.ReplaceAll(name, " ", "_")
}

// small wrappers to make testing easier
func osMkdirAll(path string, perm os.FileMode) error  { return os.MkdirAll(path, perm) }
func osMkdirTemp(dir, pattern string) (string, error) { return os.MkdirTemp(dir, pattern) }
func osRemoveAll(path string) error                   { return os.RemoveAll(path) }

// join path helper
func join(elem ...string) string { return filepath.Join(elem...) }
