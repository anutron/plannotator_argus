package mcp

import (
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
