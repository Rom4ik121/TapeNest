package domain

import "testing"

func TestDefaultAndValidate(t *testing.T) {
	r := DefaultRecipe(10)
	if err := r.Validate(10); err != nil {
		t.Fatal(err)
	}
	if d := r.OutputDuration(); d < 9.9 || d > 10.1 {
		t.Fatalf("duration %v", d)
	}
	r.Clips = append(r.Clips, Clip{
		StartSec: 2, EndSec: 6, Speed: 2, Volume: 0.5, Crop: FullCrop(),
		Rotate: 90, Transition: "fade", TransitionSec: 0.5,
	})
	if err := r.Validate(10); err != nil {
		t.Fatal(err)
	}
	// 10s + (4/2) - 0.5 transition
	if d := r.OutputDuration(); d < 11.4 || d > 11.6 {
		t.Fatalf("duration %v", d)
	}
	bad := r
	bad.Clips = append([]Clip(nil), r.Clips...)
	bad.Clips[0].Speed = 9
	if err := bad.Validate(10); err == nil {
		t.Fatal("expected speed error")
	}
	bad = r
	bad.Texts = []Text{{Text: "hi", StartSec: 0, EndSec: 1, X: 0.5, Y: 0.2, Size: 32, Color: "red"}}
	if err := bad.Validate(10); err == nil {
		t.Fatal("expected color error")
	}
	bad.Texts[0].Color = "#FFFFFF"
	bad.Texts[0].Text = "ok"
	if err := bad.Validate(10); err != nil {
		t.Fatal(err)
	}
	empty := Recipe{}
	if err := empty.Validate(0); err == nil {
		t.Fatal("expected clips error")
	}
}

func TestClipEdges(t *testing.T) {
	base := DefaultRecipe(5).Clips[0]
	cases := []Clip{
		{StartSec: 2, EndSec: 2, Speed: 1, Volume: 1, Crop: FullCrop(), Transition: "none"},
		{StartSec: 0, EndSec: 0.1, Speed: 1, Volume: 1, Crop: FullCrop(), Transition: "none"},
		{StartSec: 0, EndSec: 9, Speed: 1, Volume: 1, Crop: FullCrop(), Transition: "none"},
		{StartSec: 0, EndSec: 1, Speed: 1, Volume: 3, Crop: FullCrop(), Transition: "none"},
		{StartSec: 0, EndSec: 1, Speed: 1, Volume: 1, Crop: FullCrop(), Rotate: 45, Transition: "none"},
		{StartSec: 0, EndSec: 1, Speed: 1, Volume: 1, Crop: FullCrop(), Transition: "spin"},
		{StartSec: 0, EndSec: 1, Speed: 1, Volume: 1, Crop: Crop{X: 0.9, Y: 0, W: 0.2, H: 1}, Transition: "none"},
	}
	for i, c := range cases {
		r := Recipe{Clips: []Clip{base, c}, MusicVolume: 0}
		c.TransitionSec = 0.4
		r.Clips[1] = c
		if err := r.Validate(5); err == nil {
			t.Fatalf("case %d accepted %+v", i, c)
		}
	}
}
