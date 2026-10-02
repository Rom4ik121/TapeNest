// Package hls rewrites playlist segment URIs onto our domain (spec §5.5).
package hls

import (
	"path"
	"strings"
)

// Rewrite turns relative segment names into absolute paths with the signed query.
func Rewrite(playlist, sessionID, query string) string {
	var b strings.Builder
	for _, line := range strings.Split(playlist, "\n") {
		trim := strings.TrimRight(line, "\r")
		if trim == "" || strings.HasPrefix(strings.TrimSpace(trim), "#") {
			b.WriteString(trim)
			b.WriteByte('\n')
			continue
		}
		name := path.Base(strings.TrimSpace(trim))
		b.WriteString("/hls/")
		b.WriteString(sessionID)
		b.WriteByte('/')
		b.WriteString(name)
		if query != "" {
			b.WriteByte('?')
			b.WriteString(query)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
