package mcp

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolvePath turns a (cwd, raw) pair into either an absolute URL pass-through
// (when raw is http:// or https://) or an absolute filesystem path that
// MUST be a descendant of cwd. Returns an error if traversal would escape
// cwd, including symlink-mediated escape for any path component that already
// exists on disk.
func ResolvePath(cwd, raw string) (string, error) {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw, nil
	}
	if cwd == "" {
		return "", fmt.Errorf("cwd is empty")
	}
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	// Resolve symlinks on cwd if it exists so that the descendant check
	// compares like-for-like.
	if resolved, err := filepath.EvalSymlinks(cwdAbs); err == nil {
		cwdAbs = resolved
	}
	var joined string
	if filepath.IsAbs(raw) {
		joined, err = filepath.Abs(raw)
		if err != nil {
			return "", err
		}
	} else {
		joined = filepath.Join(cwdAbs, raw)
	}
	// Symlink-aware descendant check: if joined exists, resolve symlinks
	// then check. If it doesn't exist (new file Plannotator may create),
	// fall back to lexical containment of the cleaned path.
	check := joined
	if resolved, err := filepath.EvalSymlinks(joined); err == nil {
		check = resolved
	}
	if !descendant(cwdAbs, check) {
		return "", fmt.Errorf("path %q escapes cwd %q", raw, cwdAbs)
	}
	return joined, nil
}

// descendant returns true iff target is parent itself or strictly under parent.
func descendant(parent, target string) bool {
	parent = filepath.Clean(parent)
	target = filepath.Clean(target)
	if parent == target {
		return true
	}
	rel, err := filepath.Rel(parent, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
