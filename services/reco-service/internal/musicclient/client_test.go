package musicclient_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/musicclient"
	"github.com/tapenest/tapenest/services/reco-service/internal/testutil"
)

func TestCatalogPagingAndAudio(t *testing.T) {
	m := testutil.NewMusic("tok")
	defer m.Close()
	for i := 0; i < 3; i++ {
		m.Tracks = append(m.Tracks, map[string]any{
			"id": uuid.New().String(), "title": "t", "artistId": uuid.New().String(),
			"artist": "a", "album": "al", "genre": "Jazz", "durationSec": 100, "popularity": 1.5, "createdAt": time.Now().UTC(),
		})
	}
	c := musicclient.New(m.URL+"/", "tok")
	page, next, err := c.CatalogPage(context.Background(), nil, 2)
	if err != nil || len(page) != 2 || next == nil || page[0].Genre != "Jazz" {
		t.Fatalf("page1 %v %v %v", page, next, err)
	}
	page, next, err = c.CatalogPage(context.Background(), next, 2)
	if err != nil || len(page) != 1 || next != nil {
		t.Fatalf("page2 %v %v %v", page, next, err)
	}
	body, err := c.Audio(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "RIFF" {
		t.Fatal("audio body")
	}
}

func TestErrorsAreScrubbed(t *testing.T) {
	m := testutil.NewMusic("tok")
	defer m.Close()
	bad := musicclient.New(m.URL, "wrong-token-value")
	_, _, err := bad.CatalogPage(context.Background(), nil, 2)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401, got %v", err)
	}
	m.Close()
	_, err = musicclient.New(m.URL, "tok").Audio(context.Background(), uuid.New())
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "tok") {
		t.Fatalf("transport error must not leak the URL: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := musicclient.New("http://127.0.0.1:1", "tok").Interactions(ctx, 1, nil); err == nil {
		t.Fatal("cancelled request must fail")
	}
}
