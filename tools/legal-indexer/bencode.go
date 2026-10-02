package main

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
)

var errBencode = errors.New("bencode: invalid")

// skipValue returns the end offset of the bencoded value starting at i.
func skipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, errBencode
	}
	switch c := b[i]; {
	case c == 'i':
		j := bytes.IndexByte(b[i:], 'e')
		if j < 0 {
			return 0, errBencode
		}
		return i + j + 1, nil
	case c == 'l' || c == 'd':
		i++
		for i < len(b) && b[i] != 'e' {
			var err error
			if i, err = skipValue(b, i); err != nil {
				return 0, err
			}
		}
		if i >= len(b) {
			return 0, errBencode
		}
		return i + 1, nil
	case c >= '0' && c <= '9':
		j := bytes.IndexByte(b[i:], ':')
		if j < 0 {
			return 0, errBencode
		}
		n, err := strconv.Atoi(string(b[i : i+j]))
		if err != nil || n < 0 || i+j+1+n > len(b) {
			return 0, errBencode
		}
		return i + j + 1 + n, nil
	}
	return 0, errBencode
}

func bstr(s string) []byte { return []byte(strconv.Itoa(len(s)) + ":" + s) }

// SetURLList replaces the top-level "url-list" (BEP 19 web seeds) of a
// torrent. Every other value — notably "info" — is copied byte for byte, so
// the infohash does not change.
func SetURLList(torrent []byte, urls []string) ([]byte, error) {
	if len(torrent) == 0 || torrent[0] != 'd' {
		return nil, errBencode
	}
	raw := map[string][]byte{}
	i := 1
	for i < len(torrent) && torrent[i] != 'e' {
		kEnd, err := skipValue(torrent, i)
		if err != nil || torrent[i] < '0' || torrent[i] > '9' {
			return nil, errBencode
		}
		key := string(torrent[bytes.IndexByte(torrent[i:], ':')+i+1 : kEnd])
		vEnd, err := skipValue(torrent, kEnd)
		if err != nil {
			return nil, err
		}
		raw[key] = torrent[kEnd:vEnd]
		i = vEnd
	}
	if _, ok := raw["info"]; !ok {
		return nil, errBencode
	}
	l := []byte{'l'}
	for _, u := range urls {
		l = append(l, bstr(u)...)
	}
	raw["url-list"] = append(l, 'e')
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []byte{'d'}
	for _, k := range keys {
		out = append(out, bstr(k)...)
		out = append(out, raw[k]...)
	}
	return append(out, 'e'), nil
}
