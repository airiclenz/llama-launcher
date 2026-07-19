package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxResponseBytes caps how much of an HTTP response body the launcher reads.
// The backend endpoints it consumes (/health, /v1/models, /api/ps, /props)
// return at most a few KB; anything larger is a misbehaving or hostile
// process squatting a configured port. The probe timeout bounds duration,
// not size, so without a cap a local squatter could stream gigabytes over
// loopback and OOM the launcher — amplified by the menu re-probing every tick.
const maxResponseBytes = 512 * 1024

// boundedBody wraps an HTTP response body so at most maxResponseBytes are
// readable. An oversized body is truncated at the cap, which makes the JSON
// parse or content check that follows fail instead of allocating without
// bound.
func boundedBody(body io.Reader) io.Reader {
	return io.LimitReader(body, maxResponseBytes)
}

// sanitizeServerString strips control characters from a server-reported
// string before it can reach the terminal. Whatever answers on a configured
// local port is untrusted, and the display path passes escape sequences
// through unmodified, so a model name carrying ANSI/OSC escapes could spoof
// the screen or title, or write the clipboard via OSC 52, when printed.
// Removes the C0 range (including ESC), DEL, the C1 range (U+0080–U+009F,
// which some terminals also interpret as controls), and the Unicode
// directional-formatting characters (Trojan-Source class: U+061C,
// U+200E/U+200F, U+202A–U+202E, U+2066–U+2069), which can visually reorder
// or mask displayed text without any control byte; printable text passes
// through unchanged.
func sanitizeServerString(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return -1
		case r == 0x061c || r == 0x200e || r == 0x200f:
			return -1
		case r >= 0x202a && r <= 0x202e:
			return -1
		case r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, s)
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

// expectOK consumes and closes resp's body (reading at most maxResponseBytes)
// and maps the status to an error: nil for 200 OK, the actionable api_key
// error for an authentication failure, and statusErr(status, body) for any
// other status — body being the bounded prefix read, so the caller can
// extract a server-reported message from it.
func expectOK(resp *http.Response, statusErr func(statusCode int, body []byte) error) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(boundedBody(resp.Body))
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp.StatusCode, body)
	}
	return nil
}

// openAIModelList reads the OpenAI-style /v1/models endpoint at addr and
// returns one entry per model with a non-empty id, skipping the rest.
// llama-server and LM Studio expose the same response shape
// ({"data":[{"id":...}]}), so both adapters delegate here.
func openAIModelList(addr, apiKey string) ([]RunningModelInfo, error) {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/v1/models", apiKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/v1/models returned status %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(boundedBody(resp.Body)).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing /v1/models response: %w", err)
	}
	models := make([]RunningModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, RunningModelInfo{Name: m.ID})
	}
	return models, nil
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
