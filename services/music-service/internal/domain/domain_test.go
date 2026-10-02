package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/music-service/internal/domain"
)

func TestCursor(t *testing.T) {
	s, at := 1.5, time.Now().UTC().Truncate(time.Millisecond)
	id := uuid.New()
	c, err := domain.DecodeCursor(domain.Cursor{Score: &s, At: &at, ID: id}.Encode())
	if err != nil || *c.Score != 1.5 || !c.At.Equal(at) || c.ID != id {
		t.Fatalf("roundtrip: %+v %v", c, err)
	}
	if c, err := domain.DecodeCursor(""); c != nil || err != nil {
		t.Fatal("empty cursor")
	}
	for _, bad := range []string{"!!", "e30", domain.Cursor{ID: id}.Encode(), strings.Repeat("A", 400)} {
		var inv *domain.InvalidError
		if _, err := domain.DecodeCursor(bad); !errors.As(err, &inv) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestValidators(t *testing.T) {
	if domain.Limit(0) != 20 || domain.Limit(500) != 100 || domain.Limit(7) != 7 {
		t.Fatal("limit")
	}
	if s, err := domain.PlaylistTitle("  a  b "); err != nil || s != "a  b" {
		t.Fatalf("title: %q %v", s, err)
	}
	if _, err := domain.PlaylistTitle(" "); err == nil {
		t.Fatal("empty title")
	}
	if _, err := domain.PlaylistTitle(strings.Repeat("ж", 101)); err == nil {
		t.Fatal("long title")
	}
	q, p, err := domain.SearchQuery("  50%   Off_\\ ")
	if err != nil || q != "50% off_\\" || p != `%50\% off\_\\%` {
		t.Fatalf("search: %q %q %v", q, p, err)
	}
	if _, _, err := domain.SearchQuery(strings.Repeat("x", 101)); err == nil {
		t.Fatal("long query")
	}
	if _, err := domain.ParseID("nope", "id"); err == nil {
		t.Fatal("bad id")
	}
	if id, err := domain.ParseID(uuid.Nil.String(), "id"); err == nil && id == uuid.Nil {
		t.Log("nil uuid accepted by ParseID")
	}
	if domain.Invalid("x %d", 1).Error() != "x 1" {
		t.Fatal("invalid msg")
	}
}
