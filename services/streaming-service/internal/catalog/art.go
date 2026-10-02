package catalog

import (
	"net/url"
	"strings"
)

// PosterDataURL is a 2:3 SVG poster on the TapeNest palette.
func PosterDataURL(id, title string) string {
	return svgData(id, title, 200, 300, true)
}

// BackdropDataURL is a 16:9 SVG backdrop.
func BackdropDataURL(id string) string {
	return svgData(id+"b", "", 400, 225, false)
}

func svgData(id, title string, w, h int, withText bool) string {
	acc := []string{"#EEAA11", "#4FB3B3", "#BB3381", "#E7E4DE"}[hash(id)%4]
	text := ""
	if withText {
		line := title
		if len([]rune(line)) > 18 {
			line = string([]rune(line)[:18])
		}
		text = `<text x="16" y="270" font-family="system-ui,sans-serif" font-size="18" font-weight="700" fill="#E7E4DE">` + xmlEsc(strings.ToUpper(line)) + `</text>`
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + itoa(w) + ` ` + itoa(h) + `">` +
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="` + acc + `"/><stop offset="1" stop-color="#3F1D50"/></linearGradient></defs>` +
		`<rect width="100%" height="100%" fill="url(#g)"/>` +
		`<circle cx="70%" cy="35%" r="18%" fill="#E7E4DE" opacity=".35"/>` + text + `</svg>`
	return "data:image/svg+xml," + url.PathEscape(svg)
}

func hash(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		return -h
	}
	return h
}

func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
