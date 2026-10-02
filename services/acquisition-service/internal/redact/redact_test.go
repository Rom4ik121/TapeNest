package redact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestStringAndURL(t *testing.T) {
	in := "https://user:pass@host/x?apikey=SECRET&token=abc&ok=1"
	got := String(in)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "token=abc") {
		t.Fatalf("string not scrubbed: %s", got)
	}
	if URL(in) != "https://host/x" {
		t.Fatalf("url: %s", URL(in))
	}
	if URL("not a url") != "invalid-url" {
		t.Fatal(URL("not a url"))
	}
	if Error(nil) != nil {
		t.Fatal("nil")
	}
	if strings.Contains(Error(fmt.Errorf("download failed apikey=SECRET")).Error(), "SECRET") {
		t.Fatal("error leak")
	}
	base := errors.New("boom")
	se := Error(&url.Error{Op: "Get", URL: "http://h/p?password=SECRET", Err: base})
	if strings.Contains(se.Error(), "SECRET") || !errors.Is(se, base) {
		t.Fatalf("unwrap: %v", se)
	}
	if !strings.Contains(Error(&url.Error{Op: "Get", URL: "http://h/p", Err: nil}).Error(), "failed") {
		t.Fatal("inner nil")
	}
}
