package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathRelative(t *testing.T) {
	got, err := ResolvePath("/tmp/foo", "design.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/foo/design.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePathTraversalRejected(t *testing.T) {
	_, err := ResolvePath("/tmp/foo", "../etc/passwd")
	if err == nil {
		t.Error("expected error for traversal")
	}
}

func TestResolvePathTraversalNestedRejected(t *testing.T) {
	_, err := ResolvePath("/tmp/foo", "bar/../../etc/passwd")
	if err == nil {
		t.Error("expected error for nested traversal")
	}
}

func TestResolvePathHTTPURLPassthrough(t *testing.T) {
	got, err := ResolvePath("/tmp/foo", "https://example.com/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/x.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePathHTTPURLPassthroughHTTP(t *testing.T) {
	got, err := ResolvePath("/tmp/foo", "http://example.com/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://example.com/x.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePathAbsoluteUnderCwd(t *testing.T) {
	got, err := ResolvePath("/tmp/foo", "/tmp/foo/sub/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/foo/sub/design.md" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePathAbsoluteOutsideCwd(t *testing.T) {
	_, err := ResolvePath("/tmp/foo", "/etc/passwd")
	if err == nil {
		t.Error("expected error for absolute path outside cwd")
	}
}

func TestResolvePathEmpty(t *testing.T) {
	_, err := ResolvePath("/tmp/foo", "")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want empty-path error", err)
	}
}

func TestResolvePathCwdItself(t *testing.T) {
	got, err := ResolvePath("/tmp/foo", "/tmp/foo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/foo" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePathEmptyCwd(t *testing.T) {
	_, err := ResolvePath("", "anything.md")
	if err == nil || !strings.Contains(err.Error(), "cwd is empty") {
		t.Errorf("err = %v, want empty-cwd error", err)
	}
}

func TestResolvePathSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	target, err := os.MkdirTemp("", "escape-target-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	if err := os.Symlink(target, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	_, err = ResolvePath(dir, "escape")
	if err == nil {
		t.Error("expected error for symlink escaping cwd")
	}
}

func TestResolvePathSymlinkInternal(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "real")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath(dir, "link")
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Error("got empty path")
	}
}
