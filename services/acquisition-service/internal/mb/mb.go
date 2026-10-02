// Package mb is a small MusicBrainz WS/2 client (search artists / release groups /
// recordings, release-group tracklists, artist discographies) with a shared Redis
// rate limiter (MusicBrainz allows ~1 req/s per application; GCRA with a small
// burst so one search can run its three queries in parallel) and a Redis cache.
// Cover art comes from the Cover Art Archive by release-group MBID.
package mb

import (
	"context"
	"crypto/sha1" //nolint:gosec // cache key only
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/redact"
)

// Errors.
var (
	ErrUnavailable = errors.New("musicbrainz unavailable")
	ErrNotFound    = errors.New("musicbrainz: not found")
)

// Artist is a search/lookup result.
type Artist struct {
	MBID           uuid.UUID `json:"mbid"`
	Name           string    `json:"name"`
	Disambiguation string    `json:"disambiguation,omitempty"`
	Score          int       `json:"score"`
}

// ReleaseGroup is an album / EP / single.
type ReleaseGroup struct {
	MBID       uuid.UUID `json:"mbid"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Year       int       `json:"year"`
	ArtistMBID uuid.UUID `json:"artistMbid"`
	Artist     string    `json:"artist"`
	Score      int       `json:"score"`
}

// Recording is a track search result resolved to the album it will be acquired with.
type Recording struct {
	MBID             uuid.UUID `json:"mbid"`
	Title            string    `json:"title"`
	LengthMS         int       `json:"lengthMs"`
	ArtistMBID       uuid.UUID `json:"artistMbid"`
	Artist           string    `json:"artist"`
	ReleaseGroupMBID uuid.UUID `json:"releaseGroupMbid"`
	Album            string    `json:"album"`
	Year             int       `json:"year"`
	Score            int       `json:"score"`
}

// Track is one entry of a release tracklist.
type Track struct {
	RecordingMBID uuid.UUID `json:"recordingMbid"`
	Title         string    `json:"title"`
	Disc          int       `json:"disc"`
	Position      int       `json:"position"`
	LengthMS      int       `json:"lengthMs"`
	Artist        string    `json:"artist"`
}

// Album is a release group with the tracklist of its representative release.
type Album struct {
	ReleaseGroup
	ReleaseMBID uuid.UUID `json:"releaseMbid"`
	Tracks      []Track   `json:"tracks"`
}

// Limiter paces outgoing requests.
type Limiter interface {
	Wait(ctx context.Context) error
}

// Client talks to MusicBrainz.
type Client struct {
	Base      string // https://musicbrainz.org
	UserAgent string
	HTTP      *http.Client
	Redis     *redis.Client // cache (nil = no cache)
	Limiter   Limiter
	SearchTTL time.Duration
	LookupTTL time.Duration
}

// New builds a client with sane defaults.
func New(base, ua string, rdb *redis.Client) *Client {
	return &Client{
		Base: strings.TrimRight(base, "/"), UserAgent: ua, Redis: rdb,
		HTTP:      &http.Client{Timeout: 8 * time.Second},
		Limiter:   &RedisLimiter{Redis: rdb, Key: "acq:mb:rate", Interval: time.Second, Burst: 3},
		SearchTTL: 24 * time.Hour, LookupTTL: 7 * 24 * time.Hour,
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, ttl time.Duration, out any) error {
	q.Set("fmt", "json")
	u := c.Base + "/ws/2/" + path + "?" + q.Encode()
	sum := sha1.Sum([]byte(u)) //nolint:gosec // cache key
	key := "acq:mb:c:" + hex.EncodeToString(sum[:])
	if c.Redis != nil {
		if b, err := c.Redis.Get(ctx, key).Bytes(); err == nil {
			return json.Unmarshal(b, out)
		}
	}
	var body []byte
	for attempt := 0; attempt < 3; attempt++ {
		if c.Limiter != nil {
			if err := c.Limiter.Wait(ctx); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, redact.Error(err))
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%w: read body", ErrUnavailable)
		}
		switch resp.StatusCode {
		case http.StatusOK:
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("%w: bad json", ErrUnavailable)
			}
			if c.Redis != nil {
				_ = c.Redis.Set(ctx, key, body, ttl).Err()
			}
			return nil
		case http.StatusNotFound, http.StatusBadRequest:
			return ErrNotFound
		case http.StatusServiceUnavailable, http.StatusTooManyRequests:
			select { // throttled: back off and retry
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 1200 * time.Millisecond):
			}
		default:
			return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
		}
	}
	return fmt.Errorf("%w: throttled", ErrUnavailable)
}

// ── wire types ────────────────────────────────────────────────────────────────

type credit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

func creditName(cs []credit) (string, uuid.UUID) {
	var b strings.Builder
	var id uuid.UUID
	for i, c := range cs {
		if i == 0 {
			id, _ = uuid.Parse(c.Artist.ID)
		}
		b.WriteString(c.Name + c.JoinPhrase)
	}
	return strings.TrimSpace(b.String()), id
}

type wireRG struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
	Score            int      `json:"score"`
	Credit           []credit `json:"artist-credit"`
}

func year(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

func (w wireRG) toRG() (ReleaseGroup, bool) {
	id, err := uuid.Parse(w.ID)
	if err != nil {
		return ReleaseGroup{}, false
	}
	name, aid := creditName(w.Credit)
	t := w.PrimaryType
	if t == "" {
		t = "Other"
	}
	return ReleaseGroup{MBID: id, Title: w.Title, Type: t, Year: year(w.FirstReleaseDate), ArtistMBID: aid, Artist: name, Score: w.Score}, true
}

// acceptableRG: studio albums / EPs / singles; no bootlegs, compilations of others, live dupes first.
func secondaryPenalty(types []string) int {
	p := 0
	for _, t := range types {
		switch t {
		case "Compilation", "Live", "Remix", "DJ-mix", "Mixtape/Street", "Demo":
			p += 20
		default:
			p += 5
		}
	}
	return p
}

// ── search ────────────────────────────────────────────────────────────────────

// luceneEscape quotes user input for the MusicBrainz Lucene query syntax.
func luceneEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(strings.TrimSpace(q))
}

// queryTokens splits user input into Lucene-safe words (letters/digits only).
func queryTokens(q string) []string {
	ws := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(ws) > 6 {
		ws = ws[:6]
	}
	return ws
}

// fieldQuery builds "(phrase boosts) OR (every word in any of the fields)" so
// mixed input like "goldberg ishizaka" (title + artist) matches.
func fieldQuery(q string, fields ...string) string {
	e := luceneEscape(q)
	var alts []string
	for i, f := range fields {
		alts = append(alts, fmt.Sprintf(`%s:"%s"^%d`, f, e, len(fields)-i+1))
	}
	if ws := queryTokens(q); len(ws) > 0 {
		var all []string
		for _, w := range ws {
			var ors []string
			for _, f := range fields {
				ors = append(ors, f+":"+w)
			}
			all = append(all, "("+strings.Join(ors, " OR ")+")")
		}
		alts = append(alts, "("+strings.Join(all, " AND ")+")")
	}
	return "(" + strings.Join(alts, " OR ") + ")"
}

// SearchArtists finds artists by name.
func (c *Client) SearchArtists(ctx context.Context, q string, limit int) ([]Artist, error) {
	var resp struct {
		Artists []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Disambiguation string `json:"disambiguation"`
			Score          int    `json:"score"`
		} `json:"artists"`
	}
	v := url.Values{"query": {fieldQuery(q, "artist", "alias")}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "artist", v, c.SearchTTL, &resp); err != nil {
		return nil, err
	}
	out := make([]Artist, 0, len(resp.Artists))
	for _, a := range resp.Artists {
		if id, err := uuid.Parse(a.ID); err == nil {
			out = append(out, Artist{MBID: id, Name: a.Name, Disambiguation: a.Disambiguation, Score: a.Score})
		}
	}
	return out, nil
}

// SearchReleaseGroups finds albums/EPs by title or artist.
func (c *Client) SearchReleaseGroups(ctx context.Context, q string, limit int) ([]ReleaseGroup, error) {
	var resp struct {
		RGs []wireRG `json:"release-groups"`
	}
	v := url.Values{"query": {fieldQuery(q, "releasegroup", "artist") + ` AND status:official`}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "release-group", v, c.SearchTTL, &resp); err != nil {
		return nil, err
	}
	out := make([]ReleaseGroup, 0, len(resp.RGs))
	for _, w := range resp.RGs {
		if rg, ok := w.toRG(); ok {
			rg.Score -= secondaryPenalty(w.SecondaryTypes)
			out = append(out, rg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// SearchRecordings finds tracks by title or artist, each resolved to the official
// album (preferred) / EP / single it appears on — the unit Lidarr acquires.
func (c *Client) SearchRecordings(ctx context.Context, q string, limit int) ([]Recording, error) {
	var resp struct {
		Recordings []struct {
			ID       string   `json:"id"`
			Title    string   `json:"title"`
			Length   int      `json:"length"`
			Score    int      `json:"score"`
			Credit   []credit `json:"artist-credit"`
			Releases []struct {
				Title  string `json:"title"`
				Status string `json:"status"`
				Date   string `json:"date"`
				RG     wireRG `json:"release-group"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	v := url.Values{"query": {fieldQuery(q, "recording", "artist", "release") + ` AND status:official AND video:false`}, "limit": {strconv.Itoa(limit * 2)}}
	if err := c.get(ctx, "recording", v, c.SearchTTL, &resp); err != nil {
		return nil, err
	}
	out := make([]Recording, 0, limit)
	seen := map[string]bool{} // same song on many releases → first (best) only
	for _, r := range resp.Recordings {
		id, err := uuid.Parse(r.ID)
		if err != nil {
			continue
		}
		name, aid := creditName(r.Credit)
		best, bestScore := -1, -1<<30
		for i, rel := range r.Releases {
			if rel.Status != "" && rel.Status != "Official" {
				continue
			}
			s := 0
			switch rel.RG.PrimaryType {
			case "Album":
				s = 30
			case "EP":
				s = 20
			case "Single":
				s = 10
			}
			s -= secondaryPenalty(rel.RG.SecondaryTypes)
			if y := year(rel.Date); y > 0 {
				s -= (y - 1900) / 20 // earlier original releases slightly preferred
			}
			if s > bestScore {
				best, bestScore = i, s
			}
		}
		if best < 0 {
			continue
		}
		rel := r.Releases[best]
		rgID, err := uuid.Parse(rel.RG.ID)
		if err != nil {
			continue
		}
		key := strings.ToLower(name + "\x00" + r.Title)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Recording{
			MBID: id, Title: r.Title, LengthMS: r.Length, ArtistMBID: aid, Artist: name,
			ReleaseGroupMBID: rgID, Album: rel.RG.Title, Year: year(rel.RG.FirstReleaseDate), Score: r.Score,
		})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// ── lookups ───────────────────────────────────────────────────────────────────

// Album returns the release group with the tracklist of its most representative
// official release (the one most releases agree on by track count, earliest first).
func (c *Client) Album(ctx context.Context, rg uuid.UUID) (Album, error) {
	var resp struct {
		Releases []struct {
			ID     string   `json:"id"`
			Title  string   `json:"title"`
			Status string   `json:"status"`
			Date   string   `json:"date"`
			Credit []credit `json:"artist-credit"`
			RG     wireRG   `json:"release-group"`
			Media  []struct {
				Position int    `json:"position"`
				Format   string `json:"format"`
				Tracks   []struct {
					Title     string   `json:"title"`
					Position  int      `json:"position"`
					Length    int      `json:"length"`
					Credit    []credit `json:"artist-credit"`
					Recording struct {
						ID     string `json:"id"`
						Title  string `json:"title"`
						Length int    `json:"length"`
					} `json:"recording"`
				} `json:"tracks"`
			} `json:"media"`
		} `json:"releases"`
	}
	v := url.Values{"release-group": {rg.String()}, "inc": {"recordings+artist-credits+release-groups"}, "limit": {"50"}}
	if err := c.get(ctx, "release", v, c.LookupTTL, &resp); err != nil {
		return Album{}, err
	}
	if len(resp.Releases) == 0 {
		return Album{}, ErrNotFound
	}
	count := map[int]int{}
	for _, r := range resp.Releases {
		n := 0
		for _, m := range r.Media {
			n += len(m.Tracks)
		}
		count[n]++
	}
	best, bestKey := -1, ""
	for i, r := range resp.Releases {
		n := 0
		for _, m := range r.Media {
			n += len(m.Tracks)
		}
		off := 1
		if r.Status == "Official" {
			off = 0
		}
		date := r.Date
		if date == "" {
			date = "9999"
		}
		key := fmt.Sprintf("%d|%04d|%s", off, 9999-count[n], date)
		if best < 0 || key < bestKey {
			best, bestKey = i, key
		}
	}
	r := resp.Releases[best]
	g, _ := r.RG.toRG()
	g.MBID = rg
	name, aid := creditName(r.Credit)
	if g.Artist == "" {
		g.Artist, g.ArtistMBID = name, aid
	}
	if g.Title == "" {
		g.Title = r.Title
	}
	relID, _ := uuid.Parse(r.ID)
	a := Album{ReleaseGroup: g, ReleaseMBID: relID}
	for _, m := range r.Media {
		disc := m.Position
		if disc == 0 {
			disc = 1
		}
		for _, t := range m.Tracks {
			rid, err := uuid.Parse(t.Recording.ID)
			if err != nil {
				continue
			}
			l := t.Length
			if l == 0 {
				l = t.Recording.Length
			}
			artist, _ := creditName(t.Credit)
			if artist == "" {
				artist = g.Artist
			}
			a.Tracks = append(a.Tracks, Track{RecordingMBID: rid, Title: t.Title, Disc: disc, Position: t.Position, LengthMS: l, Artist: artist})
		}
	}
	if len(a.Tracks) == 0 {
		return Album{}, ErrNotFound
	}
	return a, nil
}

// Discography lists an artist's official albums and EPs (newest first).
func (c *Client) Discography(ctx context.Context, artist uuid.UUID, limit int) (Artist, []ReleaseGroup, error) {
	var a struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Disambiguation string `json:"disambiguation"`
	}
	if err := c.get(ctx, "artist/"+artist.String(), url.Values{}, c.LookupTTL, &a); err != nil {
		return Artist{}, nil, err
	}
	var resp struct {
		RGs []wireRG `json:"release-groups"`
	}
	v := url.Values{"artist": {artist.String()}, "type": {"album|ep"}, "release-group-status": {"website-default"}, "limit": {"100"}, "inc": {"artist-credits"}}
	if err := c.get(ctx, "release-group", v, c.LookupTTL, &resp); err != nil {
		return Artist{}, nil, err
	}
	out := make([]ReleaseGroup, 0, len(resp.RGs))
	for _, w := range resp.RGs {
		if len(w.SecondaryTypes) > 0 {
			continue
		}
		if rg, ok := w.toRG(); ok {
			out = append(out, rg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	if len(out) > limit {
		out = out[:limit]
	}
	return Artist{MBID: artist, Name: a.Name, Disambiguation: a.Disambiguation, Score: 100}, out, nil
}

// CoverURL is the Cover Art Archive front image of a release group.
func CoverURL(base string, rg uuid.UUID, size int) string {
	return fmt.Sprintf("%s/release-group/%s/front-%d", strings.TrimRight(base, "/"), rg, size)
}

// ── rate limiter ──────────────────────────────────────────────────────────────

// gcra: theoretical arrival time in ms; allows Burst requests, then one per Interval.
var gcra = redis.NewScript(`
local now = tonumber(ARGV[1])
local interval = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local tat = tonumber(redis.call('GET', KEYS[1]) or now)
if tat < now then tat = now end
local allow_at = tat - interval * (burst - 1)
if now < allow_at then return allow_at - now end
redis.call('SET', KEYS[1], tat + interval, 'PX', interval * (burst + 1))
return 0`)

// RedisLimiter is a GCRA limiter shared by all processes (API + worker).
type RedisLimiter struct {
	Redis    *redis.Client
	Key      string
	Interval time.Duration
	Burst    int
}

// Wait blocks until a request may be sent.
func (l *RedisLimiter) Wait(ctx context.Context) error {
	if l.Redis == nil {
		return nil
	}
	for {
		wait, err := gcra.Run(ctx, l.Redis, []string{l.Key}, time.Now().UnixMilli(), l.Interval.Milliseconds(), l.Burst).Int64()
		if err != nil {
			return fmt.Errorf("rate limiter: %w", err)
		}
		if wait <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(wait) * time.Millisecond):
		}
	}
}
