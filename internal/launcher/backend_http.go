package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Response-body read caps for HTTP calls to probed servers. The servers the
// launcher talks to are discovered by address, so a hostile process squatting
// on a configured port could stream data for the full client timeout on every
// probe — and discovery probes several addresses in parallel. Every body read
// therefore goes through io.LimitReader with one of these caps.
const (
	// maxStatusBodyBytes bounds small status-like payloads: health and
	// backend-discrimination probes, error-message bodies, and drained
	// load/unload responses.
	maxStatusBodyBytes = 8 << 10 // 8 KiB
	// maxJSONBodyBytes bounds structured payloads that scale with server
	// state: model lists and llama-server's /props (whose response can
	// carry a multi-KB chat template).
	maxJSONBodyBytes = 1 << 20 // 1 MiB
)

// readBodyLimited reads at most limit bytes from body and returns them.
// Content past the limit is left unread, so a body from a hostile server
// simply arrives truncated and fails whatever parse follows.
func readBodyLimited(body io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(body, limit))
}

// decodeJSONLimited decodes a single JSON value from body into v, reading at
// most limit bytes. A body larger than the limit yields a decode error
// instead of an unbounded read.
func decodeJSONLimited(body io.Reader, limit int64, v interface{}) error {
	return json.NewDecoder(io.LimitReader(body, limit)).Decode(v)
}

// authFailedErr returns an actionable error when an HTTP status indicates an
// authentication failure, or nil for any other status.
func authFailedErr(statusCode int) error {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (status %d) — check api_key in the servers section", statusCode)
	}
	return nil
}

// authedGet issues a GET request, adding "Authorization: Bearer <apiKey>"
// when apiKey is non-empty.
func authedGet(timeout time.Duration, url, apiKey string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return (&http.Client{Timeout: timeout}).Do(req)
}

// authedPostJSON issues a POST request with a JSON body, adding
// "Authorization: Bearer <apiKey>" when apiKey is non-empty.
func authedPostJSON(timeout time.Duration, url, apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return (&http.Client{Timeout: timeout}).Do(req)
}

// redactAPIKeyArgs returns a copy of args with the value following any
// --api-key flag replaced by "***", for display surfaces such as the
// profile config popup. The input slice is not modified.
func redactAPIKeyArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "--api-key" {
			out[i+1] = "***"
		}
	}
	return out
}
