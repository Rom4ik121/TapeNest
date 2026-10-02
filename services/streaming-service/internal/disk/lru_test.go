package disk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEvictOldest(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.bin")
	neu := filepath.Join(dir, "new.bin")
	if err := os.WriteFile(old, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neu, []byte("abcdefghij"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := Evict(dir, 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old should be gone")
	}
	if _, err := os.Stat(neu); err != nil {
		t.Fatal(err)
	}
}

func TestMissingDir(t *testing.T) {
	n, err := Evict(filepath.Join(t.TempDir(), "nope"), 10)
	if err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}
