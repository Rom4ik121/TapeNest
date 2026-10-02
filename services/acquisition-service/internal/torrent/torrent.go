// Package torrent parses just enough of a .torrent file (bencode) to get the
// v1 info-hash and the file list, and extracts the btih from magnet links.
package torrent

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 info-hash is SHA-1 by protocol
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalid is returned for malformed torrents / magnets.
var ErrInvalid = errors.New("torrent: invalid")

// File is one file of the torrent in torrent order (index = qBittorrent file id).
type File struct {
	Path string
	Size int64
}

// Meta is the parsed torrent.
type Meta struct {
	InfoHash    string // lowercase hex
	Name        string
	PieceLength int64
	Files       []File
}

// Parse decodes a .torrent file.
func Parse(b []byte) (*Meta, error) {
	d := &decoder{b: b, infoStart: -1}
	v, err := d.value()
	if err != nil {
		return nil, err
	}
	root, ok := v.(map[string]any)
	if !ok || d.infoStart < 0 {
		return nil, ErrInvalid
	}
	info, ok := root["info"].(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	sum := sha1.Sum(b[d.infoStart:d.infoEnd]) //nolint:gosec // protocol
	m := &Meta{InfoHash: hex.EncodeToString(sum[:])}
	m.Name, _ = info["name"].(string)
	m.PieceLength, _ = info["piece length"].(int64)
	if files, ok := info["files"].([]any); ok {
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				return nil, ErrInvalid
			}
			var parts []string
			if ps, ok := fm["path"].([]any); ok {
				for _, p := range ps {
					s, _ := p.(string)
					parts = append(parts, s)
				}
			}
			size, _ := fm["length"].(int64)
			m.Files = append(m.Files, File{Path: strings.Join(parts, "/"), Size: size})
		}
	} else {
		size, _ := info["length"].(int64)
		m.Files = []File{{Path: m.Name, Size: size}}
	}
	if m.Name == "" || m.PieceLength <= 0 || len(m.Files) == 0 {
		return nil, ErrInvalid
	}
	return m, nil
}

// MagnetHash returns the lowercase hex v1 info-hash of a magnet link.
func MagnetHash(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return "", ErrInvalid
	}
	for _, xt := range u.Query()["xt"] {
		if h, ok := strings.CutPrefix(strings.ToLower(xt), "urn:btih:"); ok {
			if len(h) == 40 {
				if _, err := hex.DecodeString(h); err == nil {
					return h, nil
				}
			}
			if len(h) == 32 { // base32
				return base32Hex(strings.ToUpper(h))
			}
		}
	}
	return "", ErrInvalid
}

func base32Hex(s string) (string, error) {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var out []byte
	var buf, bits uint64
	for _, c := range s {
		i := strings.IndexRune(alpha, c)
		if i < 0 {
			return "", ErrInvalid
		}
		buf = buf<<5 | uint64(i)
		bits += 5
		if bits >= 8 {
			out = append(out, byte(buf>>(bits-8))) //nolint:gosec // base32 decode emits one byte
			bits -= 8
		}
	}
	if len(out) != 20 {
		return "", ErrInvalid
	}
	return hex.EncodeToString(out), nil
}

type decoder struct {
	b                  []byte
	i                  int
	depth              int
	infoStart, infoEnd int
}

func (d *decoder) value() (any, error) {
	if d.i >= len(d.b) {
		return nil, ErrInvalid
	}
	d.depth++
	defer func() { d.depth-- }()
	if d.depth > 64 {
		return nil, ErrInvalid
	}
	switch c := d.b[d.i]; {
	case c == 'i':
		end := bytes.IndexByte(d.b[d.i:], 'e')
		if end < 0 {
			return nil, ErrInvalid
		}
		n, err := strconv.ParseInt(string(d.b[d.i+1:d.i+end]), 10, 64)
		if err != nil {
			return nil, ErrInvalid
		}
		d.i += end + 1
		return n, nil
	case c == 'l':
		d.i++
		var out []any
		for d.i < len(d.b) && d.b[d.i] != 'e' {
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if d.i >= len(d.b) {
			return nil, ErrInvalid
		}
		d.i++
		return out, nil
	case c == 'd':
		d.i++
		out := map[string]any{}
		for d.i < len(d.b) && d.b[d.i] != 'e' {
			k, err := d.str()
			if err != nil {
				return nil, err
			}
			start := d.i
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			if k == "info" && d.depth == 1 {
				d.infoStart, d.infoEnd = start, d.i
			}
			out[k] = v
		}
		if d.i >= len(d.b) {
			return nil, ErrInvalid
		}
		d.i++
		return out, nil
	case c >= '0' && c <= '9':
		return d.str()
	}
	return nil, ErrInvalid
}

func (d *decoder) str() (string, error) {
	colon := bytes.IndexByte(d.b[d.i:], ':')
	if colon < 0 {
		return "", ErrInvalid
	}
	n, err := strconv.Atoi(string(d.b[d.i : d.i+colon]))
	if err != nil || n < 0 || d.i+colon+1+n > len(d.b) {
		return "", ErrInvalid
	}
	s := string(d.b[d.i+colon+1 : d.i+colon+1+n])
	d.i += colon + 1 + n
	return s, nil
}
