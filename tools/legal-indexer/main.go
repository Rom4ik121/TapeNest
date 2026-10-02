// Command legal-indexer is a tiny Torznab indexer for development and e2e
// tests that only ever serves Internet Archive items whose licence is verified
// at startup to be CC0, Public Domain Mark or CC BY. It lets the acquisition
// pipeline be exercised end to end without touching any piracy source.
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

// Allowed licence URL prefixes (host+path, scheme-agnostic).
var allowedLicences = []string{
	"creativecommons.org/publicdomain/zero/",
	"creativecommons.org/publicdomain/mark/",
	"creativecommons.org/licenses/by/",
}

// LicenceAllowed reports whether an Internet Archive licenseurl is free to redistribute.
func LicenceAllowed(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	u = strings.TrimPrefix(u, "www.")
	for _, p := range allowedLicences {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// Item is one verified torrent.
type Item struct {
	ID      string
	Title   string
	Licence string
	Size    int64
	Torrent []byte
	Added   time.Time
}

type iaMeta struct {
	Server   string `json:"server"`
	D1       string `json:"d1"`
	D2       string `json:"d2"`
	Dir      string `json:"dir"`
	Metadata struct {
		Title      any    `json:"title"`
		Creator    any    `json:"creator"`
		Date       string `json:"date"`
		LicenseURL string `json:"licenseurl"`
	} `json:"metadata"`
	Files []struct {
		Name   string `json:"name"`
		Size   string `json:"size"`
		Format string `json:"format"`
	} `json:"files"`
}

func first(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			s, _ := t[0].(string)
			return s
		}
	}
	return ""
}

// Loader fetches and verifies items.
type Loader struct {
	Base string // https://archive.org
	HTTP *http.Client
}

func (l Loader) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TapeNest-legal-indexer/1.0")
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Load verifies the licence and downloads the item's torrent.
func (l Loader) Load(ctx context.Context, id string) (Item, error) {
	b, err := l.get(ctx, l.Base+"/metadata/"+id, 16<<20)
	if err != nil {
		return Item{}, err
	}
	var m iaMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return Item{}, fmt.Errorf("metadata: %w", err)
	}
	if !LicenceAllowed(m.Metadata.LicenseURL) {
		return Item{}, fmt.Errorf("item %s: licence %q is not CC0/PDM/CC-BY, refusing to serve", id, m.Metadata.LicenseURL)
	}
	t, err := l.get(ctx, l.Base+"/download/"+id+"/"+id+"_archive.torrent", 8<<20)
	if err != nil {
		return Item{}, err
	}
	// Pin web seeds to the item's storage nodes: archive.org/download/… answers
	// with a 302 per file, which libtorrent's web seed handling does not follow
	// reliably for multi-file torrents (it then stalls on boundary pieces).
	if m.Dir != "" {
		var seeds []string
		for _, h := range []string{m.D1, m.D2, m.Server} {
			if h != "" && !contains(seeds, "https://"+h+path.Dir(m.Dir)+"/") {
				seeds = append(seeds, "https://"+h+path.Dir(m.Dir)+"/")
			}
		}
		if fixed, err := SetURLList(t, seeds); err == nil {
			t = fixed
		}
	}
	var size int64
	for _, f := range m.Files {
		var n int64
		_, _ = fmt.Sscan(f.Size, &n)
		size += n
	}
	title := strings.TrimSpace(first(m.Metadata.Creator) + " - " + first(m.Metadata.Title))
	if len(m.Metadata.Date) >= 4 {
		title += " (" + m.Metadata.Date[:4] + ")"
	}
	title += " [MP3 VBR, CC0]"
	return Item{ID: id, Title: title, Licence: m.Metadata.LicenseURL, Size: size, Torrent: t, Added: time.Now()}, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Matches: at least half of the query tokens occur in the title (empty query matches all).
func Matches(query, title string) bool {
	q := tokens(query)
	if len(q) == 0 {
		return true
	}
	have := map[string]bool{}
	for _, t := range tokens(title) {
		have[t] = true
	}
	n := 0
	for _, t := range q {
		if have[t] {
			n++
		}
	}
	return n*2 >= len(q)
}

// Server is the Torznab endpoint.
type Server struct {
	Public string // how clients reach us, e.g. http://127.0.0.1:8093
	mu     sync.RWMutex
	items  []Item
}

// SetItems replaces the served items.
func (s *Server) SetItems(items []Item) {
	s.mu.Lock()
	s.items = items
	s.mu.Unlock()
}

const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps><server title="TapeNest Legal Test (CC0)"/><limits max="100" default="50"/>
<searching><search available="yes" supportedParams="q"/><music-search available="yes" supportedParams="q,artist,album"/>
<tv-search available="no" supportedParams="q"/><movie-search available="no" supportedParams="q"/></searching>
<categories><category id="3000" name="Audio"><subcat id="3010" name="Audio/MP3"/><subcat id="3040" name="Audio/Lossless"/></category></categories>
</caps>`

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Torznab string   `xml:"xmlns:torznab,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type attr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type rssItem struct {
	Title     string    `xml:"title"`
	GUID      string    `xml:"guid"`
	Link      string    `xml:"link"`
	Comments  string    `xml:"comments"`
	PubDate   string    `xml:"pubDate"`
	Size      int64     `xml:"size"`
	Category  int       `xml:"category"`
	Enclosure enclosure `xml:"enclosure"`
	Attrs     []attr    `xml:"torznab:attr"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		_, _ = w.Write([]byte("ok"))
	case strings.HasPrefix(r.URL.Path, "/torrent/"):
		s.torrent(w, r)
	case r.URL.Path == "/api":
		s.api(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch q.Get("t") {
	case "caps":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, capsXML)
		return
	case "search", "music":
	default:
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><error code="202" description="No such function"/>`)
		return
	}
	query := strings.TrimSpace(q.Get("q") + " " + q.Get("artist") + " " + q.Get("album"))
	out := rss{Version: "2.0", Torznab: "http://torznab.com/schemas/2015/feed", Channel: channel{Title: "TapeNest Legal Test (CC0)"}}
	s.mu.RLock()
	for _, it := range s.items {
		if !Matches(query, it.Title) {
			continue
		}
		link := s.Public + "/torrent/" + it.ID + ".torrent"
		out.Channel.Items = append(out.Channel.Items, rssItem{
			Title: it.Title, GUID: "https://archive.org/details/" + it.ID, Link: link, Comments: "https://archive.org/details/" + it.ID,
			PubDate: it.Added.UTC().Format(time.RFC1123Z), Size: it.Size, Category: 3000,
			Enclosure: enclosure{URL: link, Length: it.Size, Type: "application/x-bittorrent"},
			// Internet Archive always seeds its torrents (web seeds + its own peers)
			Attrs: []attr{{"category", "3000"}, {"seeders", "1"}, {"peers", "1"}, {"downloadvolumefactor", "0"}, {"uploadvolumefactor", "1"}},
		})
	}
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/rss+xml")
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(out)
}

func (s *Server) torrent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/torrent/"), ".torrent")
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.items {
		if it.ID == id {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.torrent"`)
			_, _ = w.Write(it.Torrent)
			return
		}
	}
	http.NotFound(w, r)
}

func main() {
	addr := flag.String("addr", envOr("LEGAL_INDEXER_ADDR", "127.0.0.1:8093"), "listen address")
	public := flag.String("public", envOr("LEGAL_INDEXER_PUBLIC", "http://127.0.0.1:8093"), "URL clients use to reach this server")
	items := flag.String("items", envOr("LEGAL_INDEXER_ITEMS", "The_Open_Goldberg_Variations-11823"), "comma-separated Internet Archive identifiers")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "legal-indexer")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &Server{Public: strings.TrimRight(*public, "/")}
	loader := Loader{Base: "https://archive.org", HTTP: &http.Client{Timeout: 30 * time.Second}}
	var loaded []Item
	for _, id := range strings.Split(*items, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		it, err := loader.Load(ctx, id)
		if err != nil {
			log.Error("item rejected", "id", id, "err", err)
			continue
		}
		log.Info("item verified", "id", id, "title", it.Title, "licence", it.Licence, "bytes", it.Size)
		loaded = append(loaded, it)
	}
	if len(loaded) == 0 {
		log.Error("no verified items; refusing to start")
		os.Exit(1)
	}
	srv.SetItems(loaded)
	hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = hs.Close() }()
	log.Info("listening", "addr", *addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
