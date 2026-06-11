package launcher

import (
	"bytes"
	"fmt"
	"net/http"
	"time"
)

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
