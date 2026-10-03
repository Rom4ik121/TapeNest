package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	const vkc = "https://vk.com/video-22822305_456239018"
	const rt = "https://rutube.ru/video/0f4e8b3d9e2a4c6b8d1e3f5a7c9b2d4e/"
	cases := []struct {
		in     string
		want   string
		source Source
		err    error
	}{
		// YouTube (spec: utm_*, si, feature, t dropped; youtu.be and m. unified)
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", yt, SourceYouTube, nil},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&utm_source=tg&utm_medium=share", yt, SourceYouTube, nil},
		{"https://youtu.be/dQw4w9WgXcQ?si=AbCdEf123", yt, SourceYouTube, nil},
		{"https://youtu.be/dQw4w9WgXcQ?t=42", yt, SourceYouTube, nil},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ&feature=share", yt, SourceYouTube, nil},
		{"youtube.com/watch?v=dQw4w9WgXcQ", yt, SourceYouTube, nil},
		{"  https://WWW.YouTube.COM/watch?feature=youtu.be&v=dQw4w9WgXcQ#comments  ", yt, SourceYouTube, nil},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ?feature=share", yt, SourceYouTube, nil},
		{"https://www.youtube.com/live/dQw4w9WgXcQ?si=x", yt, SourceYouTube, nil},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", yt, SourceYouTube, nil},
		{"https://music.youtube.com/watch?v=dQw4w9WgXcQ&list=RDAMVM", yt, SourceYouTube, nil},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG&index=3", yt, SourceYouTube, nil},
		{"http://youtube.com:443/watch?v=dQw4w9WgXcQ", yt, SourceYouTube, nil},
		{"https://www.youtube.com/playlist?list=PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG", "", "", ErrPlaylist},
		{"https://www.youtube.com/watch?list=PLx0sYbCqOb8TBPRdmBHs5Iftvv9TPboYG", "", "", ErrPlaylist},
		{"https://www.youtube.com/watch?v=short", "", "", ErrInvalidURL},
		{"https://www.youtube.com/@channel", "", "", ErrInvalidURL},
		// VK
		{"https://vk.com/video-22822305_456239018", vkc, SourceVK, nil},
		{"https://m.vk.com/video-22822305_456239018?list=ln-abc&from=feed", vkc, SourceVK, nil},
		{"https://vkvideo.ru/video-22822305_456239018", vkc, SourceVK, nil},
		{"https://vk.com/wall-1_2?z=video-22822305_456239018%2Fpl_wall_-1", vkc, SourceVK, nil},
		{"https://vk.com/clip-22822305_456239018", vkc, SourceVK, nil},
		{"https://vk.com/video123_456", "https://vk.com/video123_456", SourceVK, nil},
		{"https://vk.com/video/playlist/-1_2", "", "", ErrPlaylist},
		{"https://vk.com/durov", "", "", ErrInvalidURL},
		// RuTube
		{"https://rutube.ru/video/0f4e8b3d9e2a4c6b8d1e3f5a7c9b2d4e/", rt, SourceRuTube, nil},
		{"https://rutube.ru/video/0F4E8B3D9E2A4C6B8D1E3F5A7C9B2D4E?utm_campaign=x&r=wd", rt, SourceRuTube, nil},
		{"https://rutube.ru/shorts/0f4e8b3d9e2a4c6b8d1e3f5a7c9b2d4e/", rt, SourceRuTube, nil},
		{"https://rutube.ru/play/embed/0f4e8b3d9e2a4c6b8d1e3f5a7c9b2d4e", rt, SourceRuTube, nil},
		{"https://rutube.ru/video/private/0f4e8b3d9e2a4c6b8d1e3f5a7c9b2d4e/?p=KeY_1&utm_source=x", rt + "?p=KeY_1", SourceRuTube, nil},
		{"https://rutube.ru/plst/12345/", "", "", ErrPlaylist},
		{"https://rutube.ru/video/nothex/", "", "", ErrInvalidURL},
		// other public hosts yt-dlp already supports
		{"https://www.tiktok.com/@user/video/1234567890123456789?is_from_webapp=1&utm_source=share", "https://tiktok.com/@user/video/1234567890123456789", SourceTikTok, nil},
		{"https://vm.tiktok.com/ZMabc123/", "https://vm.tiktok.com/ZMabc123", SourceTikTok, nil},
		{"https://vimeo.com/123456789", "https://vimeo.com/123456789", SourceVimeo, nil},
		{"https://dai.ly/x7abc", "https://dai.ly/x7abc", SourceDailymotion, nil},
		{"https://www.instagram.com/reel/AbCdEf123/?utm_source=ig", "https://instagram.com/reel/AbCdEf123", SourceInstagram, nil},
		{"https://x.com/user/status/123", "https://x.com/user/status/123", SourceTwitter, nil},
		{"https://www.twitch.tv/videos/123456", "https://twitch.tv/videos/123456", SourceTwitch, nil},
		{"https://ok.ru/video/999", "https://ok.ru/video/999", SourceOK, nil},
		{"https://www.reddit.com/r/videos/comments/abc/title", "https://reddit.com/r/videos/comments/abc/title", SourceReddit, nil},
		{"https://t.me/channel/42", "https://t.me/channel/42", SourceTelegram, nil},
		{"https://vimeo.com/", "", "", ErrInvalidURL},
		{"https://vimeo.com/search?q=cats", "", "", ErrInvalidURL},
		{"https://tiktok.com/playlist/1", "", "", ErrPlaylist},
		// rejected: random sites, torrent indexes, cinema catalogs
		{"https://rutracker.org/forum/viewtopic.php?t=1", "", "", ErrUnsupported},
		{"https://www.kinopoisk.ru/film/301/", "", "", ErrUnsupported},
		{"magnet:?xt=urn:btih:0123456789abcdef", "", "", ErrUnsupported},
		// rejected
		{"", "", "", ErrInvalidURL},
		{"ftp://youtube.com/watch?v=dQw4w9WgXcQ", "", "", ErrInvalidURL},
		{"https://user:pw@youtube.com/watch?v=dQw4w9WgXcQ", "", "", ErrInvalidURL},
		{"https://youtube.com:8443/watch?v=dQw4w9WgXcQ", "", "", ErrInvalidURL},
		{"https://127.0.0.1/watch?v=dQw4w9WgXcQ", "", "", ErrUnsupported},
		{"https://example.com/video.mp4", "", "", ErrUnsupported},
		{"https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ", "", "", ErrUnsupported},
		{"https://" + strings.Repeat("a", 2100), "", "", ErrInvalidURL},
		{"http://[::1", "", "", ErrInvalidURL},
	}
	for _, c := range cases {
		got, err := NormalizeURL(c.in)
		if !errors.Is(err, c.err) {
			t.Errorf("%q: err = %v, want %v", c.in, err, c.err)
			continue
		}
		if c.err == nil && (got.URL != c.want || got.Source != c.source || got.ExternalID == "") {
			t.Errorf("%q: got %+v, want %s (%s)", c.in, got, c.want, c.source)
		}
	}
}

func TestHashStableAcrossVariants(t *testing.T) {
	a, _ := NormalizeURL("https://youtu.be/dQw4w9WgXcQ?si=1")
	b, _ := NormalizeURL("https://m.youtube.com/watch?v=dQw4w9WgXcQ&utm_source=x")
	if a.Hash() != b.Hash() || len(a.Hash()) != 64 {
		t.Fatalf("%s != %s", a.Hash(), b.Hash())
	}
	c, _ := NormalizeURL("https://youtu.be/aaaaaaaaaaa")
	if c.Hash() == a.Hash() {
		t.Fatal("different videos must hash differently")
	}
}
