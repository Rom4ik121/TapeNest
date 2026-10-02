package arr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArrClients(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		p := r.URL.Path
		switch {
		case p == "/api/v1/system/status":
			_, _ = w.Write([]byte(`{}`))
		case p == "/api/v1/indexer" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":1,"name":"cc0","enable":true,"protocol":"torrent"}]`))
		case p == "/api/v1/search":
			_, _ = w.Write([]byte(`[{"title":"A","protocol":"torrent","seeders":2,"downloadUrl":"` + r.Host + `","size":10},{"title":"B","protocol":"usenet"}]`))
		case p == "/api/v1/applications" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case p == "/api/v1/applications/schema":
			_, _ = w.Write([]byte(`[{"implementation":"Lidarr","configContract":"x","fields":[{"name":"baseUrl"}]}]`))
		case p == "/api/v1/applications" && r.Method == http.MethodPost:
			posts++
			w.WriteHeader(201)
		case p == "/api/v1/indexer/schema":
			_, _ = w.Write([]byte(`[{"implementation":"Torznab","definitionName":"Torznab","fields":[{"name":"baseUrl"},{"name":"apiPath"},{"name":"apiKey"},{"name":"categories"}]}]`))
		case p == "/api/v1/indexer" && r.Method == http.MethodPost:
			posts++
			w.WriteHeader(201)
		case p == "/api/v1/downloadclient" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case p == "/api/v1/downloadclient/schema":
			_, _ = w.Write([]byte(`[{"implementation":"QBittorrent","fields":[{"name":"host"},{"name":"port"},{"name":"username"},{"name":"password"}]}]`))
		case p == "/api/v1/downloadclient" && r.Method == http.MethodPost:
			posts++
			w.WriteHeader(201)
		case p == "/api/v1/qualityprofile" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case p == "/api/v1/qualityprofile/schema":
			_, _ = w.Write([]byte(`{"items":[{"id":1,"name":"High Quality Lossy","items":[{"name":"MP3-320"}]},{"id":2,"name":"Lossless"}]}`))
		case p == "/api/v1/qualityprofile" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":5}`))
		case p == "/api/v1/qualitydefinition" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"quality":{"name":"MP3-320"},"minSize":0,"maxSize":0}]`))
		case strings.HasSuffix(p, "/qualitydefinition/update"):
			w.WriteHeader(202)
		case p == "/api/v1/config/mediamanagement":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"copyUsingHardlinks":false}`))
				return
			}
			w.WriteHeader(202)
		case p == "/api/v1/config/metadataprovider":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"writeAudioTags":"all"}`))
				return
			}
			w.WriteHeader(202)
		case p == "/api/v1/rootfolder":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			w.WriteHeader(201)
		case p == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":1}]`))
		case p == "/api/v1/album/lookup":
			_, _ = w.Write([]byte(`[{"foreignAlbumId":"rg","artist":{"id":1}}]`))
		case p == "/api/v1/album" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":3,"foreignAlbumId":"rg"}]`))
		case p == "/api/v1/album" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":3,"foreignAlbumId":"rg","artistId":1,"releases":[{"id":9,"monitored":true}]}`))
		case p == "/api/v1/track":
			_, _ = w.Write([]byte(`[{"id":1,"foreignRecordingId":"r","mediumNumber":1,"absoluteTrackNumber":1}]`))
		case p == "/api/v1/trackfile":
			_, _ = w.Write([]byte(`[{"id":4,"path":"/music/a.mp3"}]`))
		case p == "/api/v1/manualimport":
			_, _ = w.Write([]byte(`[{"path":"/dl/a.mp3","quality":{},"downloadId":"d"}]`))
		case p == "/api/v1/command" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":7}`))
		case strings.HasPrefix(p, "/api/v1/command/"):
			_, _ = w.Write([]byte(`{"status":"completed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	l, err := NewLidarr(srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := l.EnsureDownloadClient(ctx, QBitSettings{Host: "h", Port: 1, Username: "u", Password: "p", Category: "c"}); err != nil {
		t.Fatal(err)
	}
	id, err := l.EnsureQualityProfile(ctx)
	if err != nil || id != 5 {
		t.Fatal(id, err)
	}
	if err := l.TuneQualitySizes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureMediaManagement(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureRootFolder(ctx, "/music/library", 5); err != nil {
		t.Fatal(err)
	}
	al, err := l.AddAlbum(ctx, "rg", "/music/library", 5)
	if err != nil || al.ID == 0 {
		t.Fatal(al, err)
	}
	if _, err := l.Tracks(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TrackFiles(ctx, 3); err != nil {
		t.Fatal(err)
	}
	cmd, err := l.ManualImport(ctx, "/dl", al, 9, []ImportFile{{Path: "/dl/a.mp3", TrackIDs: []int{1}}})
	if err != nil || cmd != 7 {
		t.Fatal(cmd, err)
	}
	if st, err := l.CommandStatus(ctx, 7); err != nil || st != "completed" {
		t.Fatal(st, err)
	}
	pr, err := NewProwlarr(srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	idx, err := pr.Indexers(ctx)
	if err != nil || len(idx) != 1 {
		t.Fatal(err, idx)
	}
	// search downloadUrl is not a real torrent; point at a tiny body via the test server by rewriting after Indexers.
	rels, err := pr.Search(ctx, "ada", 10)
	if err != nil || len(rels) != 1 {
		t.Fatal(err, rels)
	}
	if _, err := pr.EnsureLidarrApp(ctx, srv.URL, srv.URL, "lid"); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.AddTorznab(ctx, "TapeNest Legal Test (CC0)", "http://127.0.0.1:8093", []int{3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProwlarr("ftp://x", "k"); err == nil {
		t.Fatal("bad url")
	}
	// download magnet shortcut
	if _, mag, err := pr.Download(ctx, Release{MagnetURL: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)}); err != nil || !strings.HasPrefix(mag, "magnet:") {
		t.Fatal(err, mag)
	}
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/m" {
			w.Header().Set("Location", "magnet:?xt=urn:btih:"+strings.Repeat("b", 40))
			w.WriteHeader(302)
			return
		}
		_, _ = w.Write([]byte("d8:announce1:x4:infoe"))
	}))
	defer fileSrv.Close()
	if _, mag, err := fetchTorrent(ctx, fileSrv.URL+"/m", base{}); err != nil || mag == "" {
		t.Fatal(err, mag)
	}
	if b, _, err := fetchTorrent(ctx, fileSrv.URL+"/t", base{url: fileSrv.URL, key: "k"}); err != nil || len(b) == 0 {
		t.Fatal(err, len(b))
	}
	p := Provider{}
	p.SetField("baseUrl", "http://x")
	if p.FieldValue("baseUrl") != "http://x" {
		t.Fatal(p.FieldValue("baseUrl"))
	}
	if posts < 2 {
		t.Fatal(posts)
	}
}
