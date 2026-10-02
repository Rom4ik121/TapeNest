package preview

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSegmentRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	if _, err := l.OpenSegment("../etc/passwd"); err == nil {
		t.Fatal("expected reject")
	}
	if _, err := l.OpenSegment("seg00.m3u8"); err == nil {
		t.Fatal("expected reject")
	}
}

func TestEnsureUsesExisting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	if err := l.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
}
