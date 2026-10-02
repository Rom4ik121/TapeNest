package match

import "testing"

func TestQualityAndRank(t *testing.T) {
	if Quality("Album FLAC") != QFLAC || Quality("Album 320 kbps") != QMP3320 || Quality("Album V0") != QMP3V0 || Quality("Album MP3") != QMP3 || Quality("Album AAC") != QLossy || Quality("Album") != QUnk {
		t.Fatal(Quality("Album"))
	}
	want := Want{Artist: "Kimiko Ishizaka", Album: "Goldberg Variations, BWV 988", TrackCount: 1, MaxBytes: 2 << 30}
	rs := []Release{
		{Title: "junk", Size: 2 << 20, Seeders: 5},
		{Title: "Kimiko Ishizaka - Goldberg Variations MP3", Size: 100_000, Seeders: 10}, // too small per track
		{Title: "Kimiko Ishizaka - Goldberg Variations [MP3 320]", Size: 2 << 20, Seeders: 0},
		{Title: "Kimiko Ishizaka - Goldberg Variations discography FLAC", Size: 2 << 20, Seeders: 8},
		{Title: "Kimiko Ishizaka - Goldberg Variations [MP3]", Size: 2 << 20, Seeders: 3},
		{Title: "Kimiko Ishizaka - Goldberg Variations [FLAC]", Size: 8 << 20, Seeders: 4},
	}
	ranked := Rank(want, rs)
	if len(ranked) != 2 {
		t.Fatalf("ranked %d", len(ranked))
	}
	if ranked[0].Quality != QFLAC && ranked[0].Quality != QMP3 {
		t.Fatal(ranked[0].Quality)
	}
	if Rank(Want{Artist: "Various Artists", Album: "Hits", TrackCount: 1, MaxBytes: 1 << 30}, []Release{{Title: "Hits MP3", Size: 2 << 20, Seeders: 2}}) == nil {
		t.Fatal("various")
	}
}

func TestMapFiles(t *testing.T) {
	tracks := []Track{{Key: "a", Disc: 1, Position: 1, Title: "Aria"}, {Key: "b", Disc: 1, Position: 2, Title: "Variation 1"}}
	files := []File{
		{Index: 0, Name: "Album/01 - Aria.mp3", Size: 1 << 20},
		{Index: 1, Name: "Album/02 - Variation 1.flac", Size: 4 << 20},
		{Index: 2, Name: "Album/01 - Aria.flac", Size: 4 << 20},
		{Index: 3, Name: "Album/02 - Variation 1.flac", Size: 4 << 20},
		{Index: 4, Name: "Album/cover.jpg", Size: 1000},
		{Index: 5, Name: "Album/notes.txt", Size: 10},
		{Index: 6, Name: "Album/album.cue", Size: 20},
	}
	m := MapFiles(tracks, files, ".mp3")
	if m.Ext != ".flac" && m.Ext != ".mp3" {
		t.Fatal(m.Ext)
	}
	if len(m.ByTrack) < 1 {
		t.Fatal(m)
	}
	if AudioExt("x.mp3") != ".mp3" || AudioExt("x.txt") != "" {
		t.Fatal("ext")
	}
	if ContentType("a.flac") != "audio/flac" || ContentType("a.bin") != "application/octet-stream" || ContentType("a.wav") != "audio/wav" || ContentType("a.m4a") != "audio/mp4" || ContentType("a.ogg") != "audio/ogg" {
		t.Fatal("ctype")
	}
	if Similarity("Aria", "01 Aria") < 0.5 || Similarity("", "x") != 0 {
		t.Fatal(Similarity("Aria", "01 Aria"))
	}
	if len(Tokens("Étude")) == 0 {
		t.Fatal("tokens")
	}
}
