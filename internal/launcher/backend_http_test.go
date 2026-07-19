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
	if lc.apiKey() != "secret" {
		t.Errorf("apiKey = %q, want %q", lc.apiKey(), "secret")
	}

	// A reload without the key must clear it again.
	applyAPIKeys(&Config{Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true},
	}})
	if lc.apiKey() != "" {
		t.Errorf("apiKey = %q after reload without key, want empty", lc.apiKey())
	}
}

func TestSanitizeServerString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "/models/test-7b.gguf", "/models/test-7b.gguf"},
		{"empty", "", ""},
		{"unicode preserved", "modèle-7b ✦", "modèle-7b ✦"},
		{"OSC title spoof stripped", "\x1b]0;pwn\x07model.gguf", "]0;pwnmodel.gguf"},
		{"CSI sequence stripped", "\x1b[2Jmodel", "[2Jmodel"},
		{"C0 controls stripped", "a\x00b\tc\nd\re", "abcde"},
		{"DEL stripped", "a\x7fb", "ab"},
		{"C1 CSI stripped", "a\u009bb", "ab"},
		{"bidi override stripped", "user\u202egnp.exe", "usergnp.exe"},
		{"bidi isolates stripped", "a\u2066b\u2067c\u2068d\u2069e", "abcde"},
		{"bidi marks stripped", "a\u200eb\u200fc\u061cd", "abcd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeServerString(tc.in); got != tc.want {
				t.Errorf("sanitizeServerString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBoundedBody(t *testing.T) {
	t.Parallel()

	src := strings.NewReader(strings.Repeat("a", maxResponseBytes+1024))

	n, err := io.Copy(io.Discard, boundedBody(src))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != maxResponseBytes {
		t.Errorf("read %d bytes, want cap %d", n, maxResponseBytes)
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
