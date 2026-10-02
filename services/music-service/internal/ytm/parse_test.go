package ytm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const songJSON = `{
  "contents": {"musicResponsiveListItemRenderer": {
    "thumbnail": {"musicThumbnailRenderer": {"thumbnail": {"thumbnails": [
      {"url": "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg", "width": 120}
    ]}}},
    "flexColumns": [
      {"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [
        {"text": "Aria", "navigationEndpoint": {"watchEndpoint": {"videoId": "abcdefghijk"}}}
      ]}}},
      {"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [
        {"text": "Kimiko Ishizaka"}, {"text": " • "}, {"text": "Goldberg"}
      ]}}}
    ],
    "fixedColumns": [{"musicResponsiveListItemFixedColumnRenderer": {"text": {"runs": [{"text": "5:00"}]}}}]
  }}
}`

const albumJSON = `{
  "contents": {"musicResponsiveListItemRenderer": {
    "flexColumns": [
      {"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [
        {"text": "Well-Tempered", "navigationEndpoint": {"browseEndpoint": {"browseId": "MPREb_testalbum"}}}
      ]}}},
      {"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [
        {"text": "Album"}, {"text": " • "}, {"text": "J. S. Bach"}, {"text": " • "}, {"text": "2015"}
      ]}}}
    ]
  }}
}`

func TestParseSongAndAlbum(t *testing.T) {
	songs, err := ParseItems([]byte(songJSON))
	if err != nil || len(songs) != 1 {
		t.Fatalf("songs %v %v", songs, err)
	}
	s := songs[0]
	if s.VideoID != "abcdefghijk" || s.Title != "Aria" || s.Artist != "Kimiko Ishizaka" || s.Album != "Goldberg" || s.DurationSec != 300 {
		t.Fatalf("%+v", s)
	}
	if s.Thumb == "" {
		t.Fatal("thumb")
	}
	albums, err := ParseItems([]byte(albumJSON))
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums %v %v", albums, err)
	}
	a := albums[0]
	if a.BrowseID != "MPREb_testalbum" || a.Title != "Well-Tempered" || a.Artist != "J. S. Bach" || a.Year != 2015 {
		t.Fatalf("%+v", a)
	}
}

func TestCoverAllowList(t *testing.T) {
	if VideoCover("abcdefghijk") != "ytimg-abcdefghijk" {
		t.Fatal("video cover")
	}
	if VideoCover("bad") != "" {
		t.Fatal("bad video")
	}
	id := EncodeThumb("https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg")
	if id == "" {
		t.Fatal("encode")
	}
	if EncodeThumb("http://i.ytimg.com/x.jpg") != "" || EncodeThumb("https://evil.example/x.jpg") != "" {
		t.Fatal("rejected host leaked")
	}
	u, ok := CoverURL("ytimg-abcdefghijk")
	if !ok || u != "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg" {
		t.Fatal(u, ok)
	}
	u, ok = CoverURL(id)
	if !ok || u == "" {
		t.Fatal("roundtrip")
	}
}

func TestClientSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`"INNERTUBE_API_KEY":"test-key","INNERTUBE_CLIENT_VERSION":"1.2.3"`))
			return
		}
		switch r.URL.Query().Get("prettyPrint") {
		case "false":
		default:
			t.Errorf("query %s", r.URL.RawQuery)
		}
		body := songJSON
		if r.URL.Path == "/youtubei/v1/browse" {
			body = songJSON
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL
	c.HTTP = srv.Client()
	cat, err := c.Search(context.Background(), "aria")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Tracks) != 1 || cat.Tracks[0].VideoID != "abcdefghijk" {
		t.Fatalf("%+v", cat)
	}
	tracks, err := c.Album(context.Background(), "MPREb_testalbum")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("%v %v", tracks, err)
	}
}

func TestStableID(t *testing.T) {
	video := StableID("video", "abcdefghijk")
	again := StableID("video", "abcdefghijk")
	if video != again || video == StableID("album", "abcdefghijk") {
		t.Fatal("unstable or colliding")
	}
	if NameKey("Bach") != NameKey(" bach ") {
		t.Fatal("name key")
	}
}
