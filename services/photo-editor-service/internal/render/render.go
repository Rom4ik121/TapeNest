// Package render applies a photo recipe with the standard image library.
package render

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/tapenest/tapenest/services/photo-editor-service/internal/domain"
)

// Apply runs crop, rotate, straighten, exposure, color, filter and text.
func Apply(src image.Image, r domain.Recipe) (*image.NRGBA, error) {
	img := toNRGBA(src)
	img = crop(img, r.Crop)
	img = rotate(img, r.Rotate)
	img = straighten(img, r.Straighten)
	img = grade(img, r)
	if err := drawText(img, r.Text); err != nil {
		return nil, err
	}
	return img, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	if n, ok := src.(*image.NRGBA); ok {
		return n
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func crop(src *image.NRGBA, c domain.Crop) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	x0 := clampInt(int(float64(w)*c.X), 0, w-1)
	y0 := clampInt(int(float64(h)*c.Y), 0, h-1)
	x1 := clampInt(x0+int(float64(w)*c.W), x0+1, w)
	y1 := clampInt(y0+int(float64(h)*c.H), y0+1, h)
	dst := image.NewNRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	for y := y0; y < y1; y++ {
		copy(dst.Pix[(y-y0)*dst.Stride:(y-y0)*dst.Stride+(x1-x0)*4], src.Pix[y*src.Stride+x0*4:y*src.Stride+x1*4])
	}
	return dst
}

func rotate(src *image.NRGBA, deg int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	switch deg {
	case 90:
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(h-1-y, x, src.NRGBAAt(x, y))
			}
		}
		return dst
	case 180:
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(w-1-x, h-1-y, src.NRGBAAt(x, y))
			}
		}
		return dst
	case 270:
		dst := image.NewNRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(y, w-1-x, src.NRGBAAt(x, y))
			}
		}
		return dst
	default:
		return src
	}
}

func straighten(src *image.NRGBA, deg float64) *image.NRGBA {
	if math.Abs(deg) < 0.05 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	θ := deg * math.Pi / 180
	cos, sin := math.Cos(θ), math.Sin(θ)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			sx := cx + dx*cos + dy*sin - 0.5
			sy := cy - dx*sin + dy*cos - 0.5
			dst.SetNRGBA(x, y, sample(src, sx, sy))
		}
	}
	return dst
}

func sample(src *image.NRGBA, x, y float64) color.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if x < 0 || y < 0 || x >= float64(w-1) || y >= float64(h-1) {
		return color.NRGBA{A: 255}
	}
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	tx, ty := x-float64(x0), y-float64(y0)
	c00 := src.NRGBAAt(x0, y0)
	c10 := src.NRGBAAt(x0+1, y0)
	c01 := src.NRGBAAt(x0, y0+1)
	c11 := src.NRGBAAt(x0+1, y0+1)
	lerp := func(a, b, c, d uint8) uint8 {
		v := (1-tx)*(1-ty)*float64(a) + tx*(1-ty)*float64(b) + (1-tx)*ty*float64(c) + tx*ty*float64(d)
		return uint8(v + 0.5)
	}
	return color.NRGBA{R: lerp(c00.R, c10.R, c01.R, c11.R), G: lerp(c00.G, c10.G, c01.G, c11.G), B: lerp(c00.B, c10.B, c01.B, c11.B), A: 255}
}

func grade(src *image.NRGBA, r domain.Recipe) *image.NRGBA {
	exposure, contrast, sat, temp := r.Exposure, r.Contrast, r.Saturation, r.Temperature
	fade := false
	switch r.Filter {
	case "vivid":
		sat += 0.35
		contrast += 0.12
	case "mono":
		sat = -1
	case "warm":
		temp += 0.55
	case "cool":
		temp -= 0.55
	case "fade":
		contrast -= 0.15
		sat -= 0.2
		fade = true
	}
	exp := math.Pow(2, exposure)
	cst := 1 + contrast
	s := 1 + sat
	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := src.NRGBAAt(x, y)
			rf, gf, bf := float64(p.R)/255*exp, float64(p.G)/255*exp, float64(p.B)/255*exp
			rf += temp * 0.12
			bf -= temp * 0.12
			rf, gf, bf = (rf-0.5)*cst+0.5, (gf-0.5)*cst+0.5, (bf-0.5)*cst+0.5
			ycc := 0.2126*rf + 0.7152*gf + 0.0722*bf
			rf, gf, bf = ycc+(rf-ycc)*s, ycc+(gf-ycc)*s, ycc+(bf-ycc)*s
			if fade {
				rf, gf, bf = rf*0.88+0.08, gf*0.88+0.08, bf*0.88+0.08
			}
			src.SetNRGBA(x, y, color.NRGBA{R: clamp8(rf), G: clamp8(gf), B: clamp8(bf), A: 255})
		}
	}
	return src
}

func drawText(dst *image.NRGBA, t domain.Text) error {
	label := stringsTrim(t.Text)
	if label == "" {
		return nil
	}
	face, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return err
	}
	size := t.Size * float64(dst.Bounds().Dx())
	if size < 8 {
		size = 8
	}
	f, err := opentype.NewFace(face, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(parseHex(t.Color)), Face: f}
	width := d.MeasureString(label).Round()
	x := int(t.X*float64(dst.Bounds().Dx())) - width/2
	y := int(t.Y * float64(dst.Bounds().Dy()))
	d.Dot = fixed.P(x, y)
	d.DrawString(label)
	return nil
}

func parseHex(s string) color.NRGBA {
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	var v uint32
	for _, c := range s[1:] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v += uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v += uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v += uint32(c-'A') + 10
		}
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func stringsTrim(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}
