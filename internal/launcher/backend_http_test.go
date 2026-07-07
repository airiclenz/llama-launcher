package launcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAuthedGet(t *testing.T) {
	t.Parallel()

	t.Run("sends bearer header when key is set", func(t *testing.T) {
		t.Parallel()
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("Authorization")
		}))
		defer srv.Close()

		resp, err := authedGet(healthCheckTimeout, srv.URL, "secret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if got != "Bearer secret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
		}
	})

	t.Run("omits header when key is empty", func(t *testing.T) {
		t.Parallel()
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("Authorization")
		}))
		defer srv.Close()

		resp, err := authedGet(healthCheckTimeout, srv.URL, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
	})
}

func TestAuthedPostJSON(t *testing.T) {
	t.Parallel()

	var auth, contentType, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		body = string(buf)
	}))
	defer srv.Close()

	resp, err := authedPostJSON(healthCheckTimeout, srv.URL, "secret", []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret")
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if body != `{"a":1}` {
		t.Errorf("body = %q, want %q", body, `{"a":1}`)
	}
}

func TestAuthFailedErr(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := authFailedErr(code)
		if err == nil {
			t.Errorf("authFailedErr(%d) = nil, want error", code)
			continue
		}
		if !strings.Contains(err.Error(), "api_key") {
			t.Errorf("authFailedErr(%d) = %q, want mention of api_key", code, err)
		}
	}
	for _, code := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		if err := authFailedErr(code); err != nil {
			t.Errorf("authFailedErr(%d) = %v, want nil", code, err)
		}
	}
}

// countingReader counts the bytes read through it from the wrapped reader,
// so tests can assert how much of a source a bounded read consumed.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestReadBodyLimited(t *testing.T) {
	t.Parallel()

	t.Run("returns a short body in full", func(t *testing.T) {
		t.Parallel()
		got, err := readBodyLimited(strings.NewReader("ok"), 16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "ok" {
			t.Errorf("body = %q, want %q", got, "ok")
		}
	})

	t.Run("stops reading at the limit", func(t *testing.T) {
		t.Parallel()
		src := &countingReader{r: strings.NewReader(strings.Repeat("a", 64))}

		got, err := readBodyLimited(src, 16)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 16 {
			t.Errorf("read %d bytes, want %d", len(got), 16)
		}
		if src.n > 16 {
			t.Errorf("consumed %d bytes from the source, want at most 16", src.n)
		}
	})
}

func TestDecodeJSONLimited(t *testing.T) {
	t.Parallel()

	t.Run("decodes a body within the limit", func(t *testing.T) {
		t.Parallel()
		var v struct {
			Status string `json:"status"`
		}
		if err := decodeJSONLimited(strings.NewReader(`{"status":"ok"}`), 64, &v); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Status != "ok" {
			t.Errorf("Status = %q, want %q", v.Status, "ok")
		}
	})

	t.Run("fails on an oversized body without reading past the limit", func(t *testing.T) {
		t.Parallel()
		oversized := `{"status":"` + strings.Repeat("a", 256) + `"}`
		src := &countingReader{r: strings.NewReader(oversized)}
		var v struct {
			Status string `json:"status"`
		}

		err := decodeJSONLimited(src, 64, &v)

		if err == nil {
			t.Fatal("expected a decode error for a body larger than the limit")
		}
		if src.n > 64 {
			t.Errorf("consumed %d bytes from the source, want at most 64", src.n)
		}
	})
}

func TestRedactAPIKeyArgs(t *testing.T) {
	t.Parallel()

	in := []string{"--no-warmup", "--api-key", "secret", "-c", "4096"}
	out := redactAPIKeyArgs(in)

	want := []string{"--no-warmup", "--api-key", "***", "-c", "4096"}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("out[%d] = %q, want %q", i, out[i], want[i])
		}
	}
	if in[2] != "secret" {
		t.Error("input slice was modified")
	}
}

func TestApplyAPIKeys(t *testing.T) {
	b, err := GetLLMServer("llamacpp")
	if err != nil {
		t.Fatalf("getting llamacpp backend: %v", err)
	}
	lc := b.(*LlamaCpp)
	t.Cleanup(func() { lc.setAPIKey("") })

	cfg := &Config{Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true, APIKey: "secret"},
	}}
	applyAPIKeys(cfg)
	if got := lc.getAPIKey(); got != "secret" {
		t.Errorf("apiKey = %q, want %q", got, "secret")
	}

	// A reload without the key must clear it again.
	applyAPIKeys(&Config{Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true},
	}})
	if got := lc.getAPIKey(); got != "" {
		t.Errorf("apiKey = %q after reload without key, want empty", got)
	}
}

// recordingServer returns an httptest server that records the Authorization
// header it saw per request path.
func recordingServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func(path string) string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) string {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}
