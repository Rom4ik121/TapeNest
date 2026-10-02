package domain

import "testing"

var lim = Limits{MaxHeight: 720, MinTGHeight: 360, TGLimit: 50_000_000}

func TestSelectFormatFitsTelegram(t *testing.T) {
	info := Info{Duration: 600, Formats: []Format{
		{ID: "140", Ext: "m4a", VCodec: "none", ACodec: "mp4a.40.2", TBR: 128, Filesize: 9_600_000},
		{ID: "140-drc", Ext: "m4a", VCodec: "none", ACodec: "mp4a.40.2", TBR: 129, Filesize: 9_700_000},
		{ID: "251", Ext: "webm", VCodec: "none", ACodec: "opus", TBR: 140, Filesize: 10_000_000},
		{ID: "137", Ext: "mp4", VCodec: "avc1.640028", ACodec: "none", Height: 1080, Filesize: 200_000_000},
		{ID: "136", Ext: "mp4", VCodec: "avc1.4d401f", ACodec: "none", Height: 720, Filesize: 90_000_000},
		{ID: "398", Ext: "mp4", VCodec: "av01.0.05M.08", ACodec: "none", Height: 720, Filesize: 60_000_000},
		{ID: "135", Ext: "mp4", VCodec: "avc1.4d401e", ACodec: "none", Height: 480, Filesize: 35_000_000},
		{ID: "18", Ext: "mp4", VCodec: "avc1.42001E", ACodec: "mp4a.40.2", Height: 360, Filesize: 30_000_000, Protocol: "https"},
		{ID: "hls-720", Ext: "mp4", VCodec: "avc1", ACodec: "mp4a", Height: 720, Protocol: "m3u8_native"},
	}}
	c, ok := SelectFormat(info, lim)
	// 720p variants are too big (90+9.6 MB, 60+9.6 MB); 480p H.264 + m4a (non-DRC) fits.
	if !ok || c.Spec != "135+140" || c.Single || c.Height != 480 || c.EstSize != 44_600_000 {
		t.Fatalf("%+v", c)
	}
	// Without the Telegram limit: best 720p; H.264 beats AV1, and at the same
	// height/codec a progressive format (here HLS, so not pipeable) beats a merge.
	c, _ = SelectFormat(info, Limits{MaxHeight: 720})
	if c.Height != 720 || c.Spec != "hls-720" || c.Single {
		t.Fatalf("no limit: %+v", c)
	}
}

func TestSelectFormatFallbacks(t *testing.T) {
	// nothing fits under 50 MB at ≥360p → best ≤720p (delivered as a link)
	big := Info{Duration: 7200, Formats: []Format{
		{ID: "a", Ext: "m4a", VCodec: "none", ACodec: "aac", TBR: 128},
		{ID: "v720", Ext: "mp4", VCodec: "avc1", ACodec: "none", Height: 720, TBR: 2500},
		{ID: "v240", Ext: "mp4", VCodec: "avc1", ACodec: "none", Height: 240, TBR: 200},
	}}
	c, ok := SelectFormat(big, lim)
	if !ok || c.Spec != "v720+a" || c.EstSize != int64((2500+128)*1000/8*7200) {
		t.Fatalf("%+v", c)
	}
	// progressive https single file → pipe mode
	prog := Info{Duration: 30, Formats: []Format{{ID: "sd", Ext: "mp4", VCodec: "avc1", ACodec: "aac", Height: 360, TBR: 800, Protocol: "https"}}}
	c, _ = SelectFormat(prog, lim)
	if !c.Single || c.Spec != "sd" || c.EstSize != 3_000_000 {
		t.Fatalf("%+v", c)
	}
	// no audio for video-only formats / only DRM / only >720p → none
	if _, ok := SelectFormat(Info{Formats: []Format{{ID: "v", VCodec: "avc1", ACodec: "none", Height: 480}}}, lim); ok {
		t.Fatal("video-only without audio must not be chosen")
	}
	if _, ok := SelectFormat(Info{Formats: []Format{{ID: "v", VCodec: "avc1", ACodec: "aac", Height: 1080}}}, lim); ok {
		t.Fatal(">720p only")
	}
}

func TestInfoFlags(t *testing.T) {
	if !(Info{LiveStatus: "is_upcoming"}).Live() || (Info{LiveStatus: "was_live"}).Live() {
		t.Fatal("live")
	}
	if !(Info{HasDRM: true}).DRM() || (Info{}).DRM() {
		t.Fatal("drm flag")
	}
	if !(Info{Formats: []Format{{HasDRM: true}}}).DRM() || (Info{Formats: []Format{{HasDRM: true}, {}}}).DRM() {
		t.Fatal("drm formats")
	}
}
