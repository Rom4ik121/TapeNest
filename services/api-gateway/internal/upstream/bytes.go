package upstream

import (
	"bytes"
	"io"
	"net/http"
)

func bytesReader(b []byte) io.Reader {
	if b == nil {
		return http.NoBody
	}
	return bytes.NewReader(b)
}
