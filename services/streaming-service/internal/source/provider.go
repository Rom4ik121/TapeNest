// Package source is the release-search seam (spec §5.5, CONTENT_SOURCES).
package source

import "context"

// Release is a torrent the operator already has a right to add. This package never searches the public web.
type Release struct {
	Name    string
	Magnet  string
	Quality string
}

// Provider searches one content source.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string) ([]Release, error)
}

// Licensed is the default source: the local fictional catalog, no magnets.
type Licensed struct{}

// Name implements Provider.
func (Licensed) Name() string { return "licensed" }

// Search implements Provider. There is nothing to fetch.
func (Licensed) Search(context.Context, string) ([]Release, error) { return nil, nil }

// P2P reports that tracker search is not configured.
// Magnets are added only by an admin who already has one; this service does not look them up.
type P2P struct{}

// Name implements Provider.
func (P2P) Name() string { return "p2p" }

// Search implements Provider.
func (P2P) Search(context.Context, string) ([]Release, error) { return nil, nil }

// Enabled reports whether name is in the comma list.
func Enabled(list, name string) bool {
	for _, p := range split(list) {
		if p == name {
			return true
		}
	}
	return false
}

func split(list string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(list); i++ {
		if i == len(list) || list[i] == ',' {
			p := trim(list[start:i])
			if p != "" {
				out = append(out, p)
			}
			start = i + 1
		}
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
