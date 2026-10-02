package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLicenceAllowed(t *testing.T) {
	for u, want := range map[string]bool{
		"http://creativecommons.org/publicdomain/zero/1.0/":  true,
		"https://creativecommons.org/publicdomain/mark/1.0/": true,
		"https://creativecommons.org/licenses/by/4.0/":       true,
		"https://creativecommons.org/licenses/by-nc/4.0/":    false,
		"": false,
		"https://example.com/all-rights-reserved": false,
	} {
		if got := LicenceAllowed(u); got != want {
			t.Errorf("%q: got %v", u, got)
		}
	}
}

func TestMatches(t *testing.T) {
	title := "Kimiko Ishizaka - The Open Goldberg Variations (2012) [MP3 VBR, CC0]"
	if !Matches("Kimiko Douglass-Ishizaka The Open Goldberg Variations", title) || !Matches("", title) {
		t.Fatal("expected match")
	}
	if Matches("Macan Kavkaz", title) {
		t.Fatal("unexpected match")
	}
}

func TestLoaderAndServer(t *testing.T) {
	ia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata/ok":
			_, _ = w.Write([]byte(`{"server":"s1","d1":"s1","d2":"s2","dir":"/0/items/ok","metadata":{"title":"Album","creator":["Artist"],"date":"2012-05-01","licenseurl":"http://creativecommons.org/publicdomain/zero/1.0/"},"files":[{"name":"a.mp3","size":"100"}]}`))
		case "/metadata/nc":
			_, _ = w.Write([]byte(`{"metadata":{"title":"X","licenseurl":"https://creativecommons.org/licenses/by-nc/4.0/"}}`))
		case "/download/ok/ok_archive.torrent":
			_, _ = w.Write([]byte("d4:infod4:name1:aee"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ia.Close()
	l := Loader{Base: ia.URL, HTTP: ia.Client()}
	if _, err := l.Load(context.Background(), "nc"); err == nil {
		t.Fatal("non-free licence must be rejected")
	}
	if _, err := l.Load(context.Background(), "missing"); err == nil {
		t.Fatal("missing item must fail")
	}
	it, err := l.Load(context.Background(), "ok")
	if err != nil || it.Size != 100 || !strings.Contains(it.Title, "Artist - Album (2012)") {
		t.Fatalf("load: %+v %v", it, err)
	}
	s := &Server{Public: "http://x"}
	s.SetItems([]Item{it})
	for path, want := range map[string]string{
		"/api?t=caps":                  "<caps>",
		"/api?t=music&q=artist+album":  "http://x/torrent/ok.torrent",
		"/api?t=search&q=nothing+here": "<channel>",
		"/torrent/ok.torrent":          "d4:info",
		"/healthz":                     "ok",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %q lacks %q", path, rec.Body.String(), want)
		}
	}
	for _, path := range []string{"/api?t=tvsearch", "/torrent/zzz.torrent", "/nope"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code < 400 {
			t.Errorf("%s: code %d", path, rec.Code)
		}
	}
}

func TestSetURLList(t *testing.T) {
	in := []byte("d8:announce3:abc4:infod4:name1:a6:lengthi5ee8:url-listl3:oldee")
	out, err := SetURLList(in, []string{"https://h/0/items/"})
	if err != nil {
		t.Fatal(err)
	}
	want := "d8:announce3:abc4:infod4:name1:a6:lengthi5ee8:url-listl18:https://h/0/items/ee"
	if string(out) != want {
		t.Fatalf("got %s", out)
	}
	if _, err := SetURLList([]byte("d3:foo3:bare"), nil); err == nil {
		t.Fatal("torrent without info must fail")
	}
	for _, bad := range []string{"", "l", "d3:fooi1", "d4:infod1:xi", "d99:x"} {
		if _, err := SetURLList([]byte(bad), nil); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
