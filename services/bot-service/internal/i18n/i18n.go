// Package i18n holds bot texts in ru/en dictionaries (spec §15).
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed ru.json en.json
var files embed.FS

// Lang is a supported language.
type Lang string

// Supported languages; RU is the fallback (spec §15).
const (
	RU Lang = "ru"
	EN Lang = "en"
)

// Langs lists all supported languages.
var Langs = []Lang{RU, EN}

// Bundle holds loaded dictionaries.
type Bundle struct {
	dicts map[Lang]map[string]string
}

// Load parses the embedded dictionaries.
func Load() (*Bundle, error) {
	b := &Bundle{dicts: map[Lang]map[string]string{}}
	for _, l := range Langs {
		raw, err := files.ReadFile(string(l) + ".json")
		if err != nil {
			return nil, fmt.Errorf("i18n: %w", err)
		}
		d := map[string]string{}
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("i18n %s: %w", l, err)
		}
		b.dicts[l] = d
	}
	return b, nil
}

// Keys returns the keys of lang (for parity tests).
func (b *Bundle) Keys(l Lang) []string {
	out := make([]string, 0, len(b.dicts[l]))
	for k := range b.dicts[l] {
		out = append(out, k)
	}
	return out
}

// Detect maps Telegram language_code to a supported language: en* → EN, else RU.
func Detect(languageCode string) Lang {
	if strings.HasPrefix(strings.ToLower(languageCode), "en") {
		return EN
	}
	return RU
}

// T returns the text for key with {placeholders} replaced; falls back to RU, then to the key.
func (b *Bundle) T(l Lang, key string, args ...string) string {
	s, ok := b.dicts[l][key]
	if !ok {
		if s, ok = b.dicts[RU][key]; !ok {
			return key
		}
	}
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}
