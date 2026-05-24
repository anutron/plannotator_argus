package mcp

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolvePath turns a (cwd, raw) pair into either an absolute URL pass-through
// (when raw is http:// or https://) or an absolute filesystem path that
// MUST be a descendant of cwd. Returns an error if traversal would escape
// cwd.
func ResolvePath(cwd, raw string) (string, error) {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw, nil
	}
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(raw) {
		// Absolute path is allowed only if it's a descendant of cwd or
		// equal to cwd itself.
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", err
		}
		cwdAbs, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		if !descendant(cwdAbs, abs) {
			return "", fmt.Errorf("absolute path %q escapes cwd %q", abs, cwdAbs)
		}
		return abs, nil
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(cwdAbs, raw)
	if !descendant(cwdAbs, joined) {
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
