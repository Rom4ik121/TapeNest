package torrent

import (
	"strings"
	"testing"
)

func bencodeSingle(name string, length, piece int) []byte {
	pieces := make([]byte, 20*((length+piece-1)/piece))
	for i := range pieces {
		pieces[i] = byte(i)
	}
	s := "d8:announce11:http://t.me4:infod6:lengthi" + itoa(length) + "e4:name" + itoa(len(name)) + ":" + name + "12:piece lengthi" + itoa(piece) + "e6:pieces" + itoa(len(pieces)) + ":" + string(pieces) + "ee"
	return []byte(s)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestParseAndMagnet(t *testing.T) {
	b := bencodeSingle("aria.mp3", 800000, 16384)
	m, err := Parse(b)
	if err != nil || m.Name != "aria.mp3" || len(m.Files) != 1 || m.InfoHash == "" || m.PieceLength != 16384 {
		t.Fatalf("%v %v", m, err)
	}
	name := "01-Aria.mp3"
	multi := []byte("d8:announce0:4:infod5:filesld6:lengthi800000e4:pathl" + itoa(len(name)) + ":" + name + "eee4:name5:Album12:piece lengthi16384e6:pieces20:" + strings.Repeat("a", 20) + "ee")
	mm, err := Parse(multi)
	if err != nil || mm.Files[0].Path != "01-Aria.mp3" {
		t.Fatalf("%v %v", mm, err)
	}
	if _, err := Parse([]byte("i1e")); err == nil {
		t.Fatal("want invalid")
	}
	if _, err := Parse([]byte("de")); err == nil {
		t.Fatal("want invalid root")
	}
	h, err := MagnetHash("magnet:?xt=urn:btih:" + m.InfoHash + "&dn=x")
	if err != nil || h != m.InfoHash {
		t.Fatalf("magnet %s %v", h, err)
	}
	if _, err := MagnetHash("http://x"); err == nil {
		t.Fatal("scheme")
	}
	if _, err := MagnetHash("magnet:?xt=urn:btih:zzzz"); err == nil {
		t.Fatal("bad hash")
	}
}
