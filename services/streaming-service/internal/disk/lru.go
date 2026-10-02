// Package disk evicts the oldest files in a cache directory until it fits a byte budget.
package disk

import (
	"os"
	"path/filepath"
	"sort"
)

// Evict removes regular files (oldest first) until the directory is within maxBytes.
// maxBytes <= 0 disables eviction. Missing directories are ignored.
func Evict(dir string, maxBytes int64) (int, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	type item struct {
		path string
		size int64
		mod  int64
	}
	var files []item
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, item{path: filepath.Join(dir, e.Name()), size: info.Size(), mod: info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= maxBytes {
		return 0, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	n := 0
	for _, f := range files {
		if total <= maxBytes {
			break
		}
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return n, err
		}
		total -= f.size
		n++
	}
	return n, nil
}
