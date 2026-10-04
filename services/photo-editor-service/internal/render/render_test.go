package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
)

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestCropRotateStraighten(t *testing.T) {
	src := solid(8, 8, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src.SetNRGBA(3, 3, color.NRGBA{R: 255, A: 255})
	out, err := Apply(src, domain.Recipe{
		Crop: domain.Crop{X: 0, Y: 0, W: 0.5, H: 1}, Rotate: 90, Straighten: 0,
		Filter: "none", Format: "jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	// crop left half is 4x8, 90 CW makes 8x4. Red pixel was (0,0) → (7,0).
	if out.Bounds().Dx() != 8 || out.Bounds().Dy() != 4 {
		t.Fatalf("size %v", out.Bounds())
	}
	if out.NRGBAAt(7, 0).R < 200 {
		t.Fatalf("rotated pixel %+v", out.NRGBAAt(7, 0))
	}
	plain, err := Apply(src, domain.Recipe{
		Crop: domain.Crop{X: 0, Y: 0, W: 1, H: 1}, Rotate: 180, Straighten: 0, Filter: "none", Format: "jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.NRGBAAt(7, 7).R < 200 {
		t.Fatalf("180 pixel %+v", plain.NRGBAAt(7, 7))
	}
	turned, err := Apply(src, domain.Recipe{
		Crop: domain.Crop{X: 0, Y: 0, W: 1, H: 1}, Rotate: 180, Straighten: 8, Filter: "none", Format: "jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRed(turned) {
		t.Fatal("straighten dropped the marked pixel")
	}
}

func hasRed(img *image.NRGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).R > 200 {
				return true
			}
		}
	}
	return false
}

func TestExposureMonoText(t *testing.T) {
	src := solid(40, 30, color.NRGBA{R: 40, G: 80, B: 120, A: 255})
	base := domain.Identity()
	base.Exposure = 1
	bright, err := Apply(src, base)
	if err != nil {
		t.Fatal(err)
	}
	if bright.NRGBAAt(5, 5).R <= src.NRGBAAt(5, 5).R {
		t.Fatal("exposure did not lift")
	}
	mono := domain.Identity()
	mono.Filter = "mono"
	gray, err := Apply(src, mono)
	if err != nil {
		t.Fatal(err)
	}
	p := gray.NRGBAAt(5, 5)
	if abs(int(p.R)-int(p.G)) > 2 || abs(int(p.G)-int(p.B)) > 2 {
		t.Fatalf("not gray %+v", p)
	}
	with := domain.Identity()
	with.Text = domain.Text{Text: "Hi", X: 0.5, Y: 0.7, Size: 0.2, Color: "#FF0000"}
	labeled, err := Apply(src, with)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	b := labeled.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			px := labeled.NRGBAAt(x, y)
			if px.R > 180 && px.G < 80 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("text was not drawn")
	}
}

func TestFiltersShiftColor(t *testing.T) {
	src := solid(8, 8, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
	warmR := domain.Identity()
	warmR.Filter = "warm"
	warm, err := Apply(src, warmR)
	if err != nil {
		t.Fatal(err)
	}
	coolR := domain.Identity()
	coolR.Filter = "cool"
	cool, err := Apply(src, coolR)
	if err != nil {
		t.Fatal(err)
	}
	if warm.NRGBAAt(1, 1).R <= cool.NRGBAAt(1, 1).R {
		t.Fatal("warm should be redder than cool")
	}
	fadeR := domain.Identity()
	fadeR.Filter = "fade"
	fade, err := Apply(src, fadeR)
	if err != nil {
		t.Fatal(err)
	}
	if fade.NRGBAAt(1, 1).R == 0 {
		t.Fatal("fade")
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
