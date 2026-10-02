package hls

import (
	"strings"
	"testing"
)

func TestRewrite(t *testing.T) {
	in := "#EXTM3U\n#EXTINF:6.0,\nseg00.ts\n#EXT-X-ENDLIST\n"
	out := Rewrite(in, "abc", "exp=1&sig=ff")
	if !strings.Contains(out, "/hls/abc/seg00.ts?exp=1&sig=ff") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(out, "#EXTM3U") {
		t.Fatal(out)
	}
}
