package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendFileMultipart(t *testing.T) {
	type got struct {
		path, ct string
		length   int64
		fields   map[string]string
		file     string
		fileName string
		fileCT   string
	}
	var g got
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g = got{path: strings.TrimPrefix(r.URL.Path, "/bot"+token), ct: r.Header.Get("Content-Type"), length: r.ContentLength, fields: map[string]string{}}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				g.file, g.fileName, g.fileCT = string(b), p.FileName(), p.Header.Get("Content-Type")
			} else {
				g.fields[p.FormName()] = string(b)
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9,"chat":{"id":5,"type":"private"}}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, token, nil)
	body := "FAKE-MP4-BYTES"
	err := c.SendFile(context.Background(), Upload{
		ChatID: 5, ReplyToMessageID: 3, Caption: "🎬 Zoo", FileName: `Me "at" the zoo.mp4`,
		MimeType: "video/mp4", Size: int64(len(body)), Body: strings.NewReader(body + "EXTRA"), DurationSec: 19, Width: 320, Height: 240,
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.path != "/sendVideo" || !strings.HasPrefix(g.ct, "multipart/form-data; boundary=") || g.length <= int64(len(body)) {
		t.Fatalf("request: %+v", g)
	}
	if g.file != body || g.fileName != "Me 'at' the zoo.mp4" || g.fileCT != "video/mp4" {
		t.Fatalf("file part: %q %q %q", g.file, g.fileName, g.fileCT)
	}
	f := g.fields
	if f["chat_id"] != "5" || f["caption"] != "🎬 Zoo" || f["supports_streaming"] != "true" || f["duration"] != "19" || f["width"] != "320" ||
		f["height"] != "240" || !strings.Contains(f["reply_parameters"], `"message_id":3`) {
		t.Fatalf("fields: %v", f)
	}
	// non-mp4 → sendDocument without video fields
	if err := c.SendFile(context.Background(), Upload{ChatID: 5, FileName: "", Size: 3, Body: strings.NewReader("abc"), AsDocument: true}); err != nil {
		t.Fatal(err)
	}
	if g.path != "/sendDocument" || g.fields["supports_streaming"] != "" || g.fileName != "video.mp4" || g.fileCT != "application/octet-stream" {
		t.Fatalf("document: %+v", g)
	}
}

func TestSendFileTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte("<html>413</html>"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, token, nil)
	err := c.SendFile(context.Background(), Upload{ChatID: 1, Size: 1, Body: strings.NewReader("x")})
	if !IsTooLarge(err) || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v", err)
	}
	if IsTooLarge(errors.New("x")) || IsNotModified(errors.New("x")) {
		t.Fatal("plain errors")
	}
	if !IsTooLarge(&APIError{Code: 400, Description: "Bad Request: file is too big"}) {
		t.Fatal("description match")
	}
	dead := NewClient("http://127.0.0.1:1", token, nil)
	if err := dead.SendFile(context.Background(), Upload{ChatID: 1, Size: 1, Body: strings.NewReader("x")}); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("network error must be redacted: %v", err)
	}
}

func TestIsUnreachable(t *testing.T) {
	cases := map[error]bool{
		&APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}: true,
		&APIError{Code: 400, Description: "Bad Request: chat not found"}:            true,
		&APIError{Code: 400, Description: "Bad Request: message to edit not found"}: false,
		errors.New("network"): false,
	}
	for err, want := range cases {
		if got := IsUnreachable(err); got != want {
			t.Errorf("%v: got %v", err, got)
		}
	}
}
