// Package httpapi is the REST transport of api-gateway (chi).
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/tapenest/tapenest/services/api-gateway/internal/upstream"
)

// Error codes of the public contract (docs/api/gateway.openapi.yaml).
const (
	CodeInvalid            = "INVALID"
	CodeInvalidInitData    = "INVALID_INIT_DATA"
	CodeInitDataExpired    = "INIT_DATA_EXPIRED"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeTokenExpired       = "TOKEN_EXPIRED"
	CodeRefreshInvalid     = "REFRESH_INVALID"
	CodeRefreshReused      = "REFRESH_REUSED"
	CodeForbidden          = "FORBIDDEN"
	CodeNotFound           = "NOT_FOUND"
	CodeRateLimited        = "RATE_LIMITED"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeNotImplemented     = "NOT_IMPLEMENTED"
	CodeInternal           = "INTERNAL"
)

// ErrorBody is the JSON error shape: { message, code, service? }.
type ErrorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Service string `json:"service,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorBody{Message: msg, Code: code})
}

// UpstreamErrorWriter is the exported upstream error renderer for wiring in main.
func UpstreamErrorWriter(log *slog.Logger) upstream.ErrorWriter { return upstreamError(log) }

// upstreamError maps upstream failures: not deployed → 501, failing → 503 (spec §5.2).
func upstreamError(log *slog.Logger) upstream.ErrorWriter {
	return func(w http.ResponseWriter, r *http.Request, service string, err error) {
		if errors.Is(err, upstream.ErrNotConfigured) {
			writeJSON(w, http.StatusNotImplemented, ErrorBody{
				Message: notImplementedMessage(service), Code: CodeNotImplemented, Service: service,
			})
			return
		}
		log.WarnContext(r.Context(), "upstream unavailable", "service", service, "err", err, "request_id", RequestID(r.Context()))
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, ErrorBody{
			Message: "service temporarily unavailable", Code: CodeServiceUnavailable, Service: service,
		})
	}
}

func notImplementedMessage(service string) string {
	switch service {
	case "download":
		return "download-service is not deployed in this environment (set DOWNLOAD_SERVICE_URL)"
	case "music":
		return "music backend is not deployed yet (planned for stage 3)"
	default:
		return "service is not deployed"
	}
}
