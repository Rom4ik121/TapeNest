package links

import (
	"testing"
	"unicode/utf16"

	"github.com/tapenest/tapenest/services/bot-service/internal/telegram"
)

func TestClassify(t *testing.T) {
	cases := map[string]Source{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":   YouTube,
		"https://youtube.com/shorts/abc":                YouTube,
		"https://m.youtube.com/watch?v=x":               YouTube,
		"https://music.youtube.com/watch?v=x":           YouTube,
		"https://youtu.be/dQw4w9WgXcQ?si=abc":           YouTube,
		"http://youtu.be/x":                             YouTube,
		"https://www.youtube-nocookie.com/embed/x":      YouTube,
		"https://YOUTUBE.COM./watch?v=x":                YouTube,
		"https://vk.com/video-1_2":                      VK,
		"https://m.vk.com/video-1_2":                    VK,
		"https://vkvideo.ru/video-1_2":                  VK,
		"https://vk.ru/clip-1_2":                        VK,
		"https://rutube.ru/video/abc/":                  RuTube,
		"https://www.rutube.ru/video/abc/":              RuTube,
		"https://example.com/watch":                     "",
		"https://notyoutube.com/watch?v=x":              "",
		"https://youtube.com.evil.example/watch?v=x":    "",
		"https://evil.example/?u=https://youtube.com/x": "",
		"ftp://youtube.com/x":                           "",
		"javascript:alert(1)":                           "",
		"not a url":                                     "",
		"https://vk.company.example/x":                  "",
	}
	for u, want := range cases {
		if got := Classify(u); got != want {
			t.Errorf("Classify(%q)=%q want %q", u, got, want)
		}
	}
}

func entity(text, sub, typ string) telegram.MessageEntity {
	u16 := utf16.Encode([]rune(text))
	s16 := utf16.Encode([]rune(sub))
	for i := 0; i+len(s16) <= len(u16); i++ {
		match := true
		for j := range s16 {
			if u16[i+j] != s16[j] {
				match = false
				break
			}
		}
		if match {
			return telegram.MessageEntity{Type: typ, Offset: i, Length: len(s16)}
		}
	}
	panic("substring not found")
}

func TestFindWithEntitiesUTF16(t *testing.T) {
	text := "🎶🔥 глянь youtu.be/abc и https://example.com/x."
	ents := []telegram.MessageEntity{
		entity(text, "youtu.be/abc", "url"),
		entity(text, "https://example.com/x", "url"),
		{Type: "text_link", Offset: 0, Length: 2, URL: "https://rutube.ru/video/1/"},
		{Type: "bold", Offset: 0, Length: 2},
		{Type: "url", Offset: 500, Length: 3}, // out of range: ignored
	}
	got := Find(text, ents)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].URL != "https://youtu.be/abc" || got[0].Source != YouTube {
		t.Fatalf("schemeless url: %+v", got[0])
	}
	if got[1].Source != "" || got[2].Source != RuTube {
		t.Fatalf("got %+v", got)
	}
}

func TestFindRegexFallbackAndDedupe(t *testing.T) {
	got := Find("see https://vk.com/video1_2, and https://vk.com/video1_2! (https://rutube.ru/video/x/)", nil)
	if len(got) != 2 || got[0].URL != "https://vk.com/video1_2" || got[1].URL != "https://rutube.ru/video/x/" {
		t.Fatalf("got %+v", got)
	}
	if len(Find("no links here", nil)) != 0 {
		t.Fatal("no links expected")
	}
	if SourceNames() != "YouTube, VK, RuTube" {
		t.Fatal(SourceNames())
	}
}
