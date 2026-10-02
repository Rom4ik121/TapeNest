package ytm

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// StableID is the catalog UUID for a YouTube Music video or browse id.
// Same key always maps to the same row, so likes survive a restart.
func StableID(kind, key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://music.youtube.com/"+kind+"/"+key))
}

var (
	reVideo  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	reBrowse = regexp.MustCompile(`^(UC[A-Za-z0-9_-]{10,}|MPRE[A-Za-z0-9_-]{6,})$`)
)

// ValidVideoID reports whether id is a YouTube video id (not a URL).
func ValidVideoID(id string) bool { return reVideo.MatchString(id) }

// ValidBrowseID reports whether id is a YouTube Music album (MPRE) or artist (UC) id.
func ValidBrowseID(id string) bool { return reBrowse.MatchString(id) }

// NameKey is a local stand-in browse id when YouTube did not give one.
// It is never sent to YouTube.
func NameKey(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return "name:" + hex.EncodeToString(sum[:8])
}
