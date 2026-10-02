package qbt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	var start404 bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "x"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/app/version":
			_, _ = w.Write([]byte(`"v5.0.0"`))
		case "/api/v2/app/setPreferences":
			w.WriteHeader(200)
		case "/api/v2/app/preferences":
			_, _ = w.Write([]byte(`{"save_path":"/d"}`))
		case "/api/v2/torrents/categories":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v2/torrents/createCategory":
			w.WriteHeader(200)
		case "/api/v2/torrents/add":
			w.WriteHeader(200)
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(`[{"hash":"abc","state":"downloading","seq_dl":false,"f_l_piece_prio":false,"save_path":"/d"}]`))
		case "/api/v2/torrents/files":
			_, _ = w.Write([]byte(`[{"index":0,"name":"a.mp3","size":10,"progress":1,"priority":1}]`))
		case "/api/v2/torrents/properties":
			_, _ = w.Write([]byte(`{"piece_size":16384}`))
		case "/api/v2/torrents/pieceStates":
			_, _ = w.Write([]byte(`[2,2]`))
		case "/api/v2/torrents/filePrio", "/api/v2/torrents/toggleSequentialDownload", "/api/v2/torrents/toggleFirstLastPiecePrio", "/api/v2/torrents/delete", "/api/v2/torrents/reannounce", "/api/v2/torrents/resume":
			w.WriteHeader(200)
		case "/api/v2/torrents/start":
			if !start404 {
				start404 = true
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(200)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v, err := c.Version(ctx); err != nil || v == "" {
		t.Fatal(v, err)
	}
	if err := c.SetPreferences(ctx, map[string]any{"save_path": "/d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Preferences(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureCategory(ctx, "tapenest", "/d/tapenest"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTorrent(ctx, []byte("d8:announce1:x4:infoe"), "", AddOptions{Category: "tapenest", Tags: []string{"t"}, Stopped: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTorrent(ctx, nil, "magnet:?xt=urn:btih:abc", AddOptions{}); err != nil {
		t.Fatal(err)
	}
	tor, err := c.Torrent(ctx, "abc")
	if err != nil || tor.Hash != "abc" {
		t.Fatal(tor, err)
	}
	if _, err := c.Files(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if n, err := c.PieceSize(ctx, "abc"); err != nil || n != 16384 {
		t.Fatal(n, err)
	}
	if st, err := c.PieceStates(ctx, "abc"); err != nil || len(st) != 2 {
		t.Fatal(st, err)
	}
	if err := c.SetFilePriority(ctx, "abc", []int{0}, PrioMaximal); err != nil {
		t.Fatal(err)
	}
	if err := c.SetFilePriority(ctx, "abc", nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSequential(ctx, "abc", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "abc", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Reannounce(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := New("notaurl", "u", "p"); err == nil {
		t.Fatal("url")
	}
}
