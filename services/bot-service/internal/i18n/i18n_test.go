package i18n

import (
	"sort"
	"strings"
	"testing"
)

func TestDictionariesParity(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	ru, en := b.Keys(RU), b.Keys(EN)
	sort.Strings(ru)
	sort.Strings(en)
	if strings.Join(ru, ",") != strings.Join(en, ",") || len(ru) == 0 {
		t.Fatalf("ru/en keys differ:\nru=%v\nen=%v", ru, en)
	}
	for _, l := range Langs {
		for _, k := range b.Keys(l) {
			if strings.TrimSpace(b.T(l, k)) == "" {
				t.Errorf("%s:%s is empty", l, k)
			}
		}
	}
}

func TestTAndDetect(t *testing.T) {
	b, _ := Load()
	if got := b.T(EN, "start.greeting", "name", "Roma"); !strings.HasPrefix(got, "Hi, Roma!") {
		t.Fatal(got)
	}
	if got := b.T(RU, "start.greeting", "name", "Рома"); !strings.HasPrefix(got, "Привет, Рома!") {
		t.Fatal(got)
	}
	if b.T(Lang("de"), "commands.help") != b.T(RU, "commands.help") {
		t.Fatal("unknown lang falls back to ru")
	}
	if b.T(EN, "no.such.key") != "no.such.key" {
		t.Fatal("missing key returns key")
	}
	for code, want := range map[string]Lang{"en": EN, "en-US": EN, "EN": EN, "ru": RU, "uk": RU, "": RU, "de": RU} {
		if got := Detect(code); got != want {
			t.Errorf("Detect(%q)=%s want %s", code, got, want)
		}
	}
}
