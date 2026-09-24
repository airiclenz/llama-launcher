package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeCLI writes a small shell script that emits the given stdout/stderr and
// exits with the given code, then returns its path for use as llamaLauncherBin.
func fakeCLI(t *testing.T, stdout, stderr string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI script is POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-llama-launcher")
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s' " + shellQuote(stdout) + "\n"
	}
	if stderr != "" {
		script += "printf '%s' " + shellQuote(stderr) + " 1>&2\n"
	}
	script += "exit " + itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return path
}

func shellQuote(s string) string { return "'" + s + "'" }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", r.Content[0])
	}
	return tc.Text
}

func TestRunSuccessReturnsStdout(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, `[{"name":"qwen"}]`, "", 0)}
	res := cfg.run(context.Background(), "list", "--json")
	if res.IsError {
		t.Fatal("success should not be flagged as error")
	}
	if got := resultText(t, res); got != `[{"name":"qwen"}]` {
		t.Errorf("text = %q", got)
	}
}

// A non-zero exit that still prints JSON (e.g. `status` when nothing is
// running) must be returned as normal content, not an error.
func TestRunNonZeroWithStdoutIsNotError(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, `[]`, "", 1)}
	res := cfg.run(context.Background(), "status", "--json")
	if res.IsError {
		t.Error("non-zero exit with stdout should not be an error")
	}
	if got := resultText(t, res); got != `[]` {
		t.Errorf("text = %q", got)
	}
}

// A mutating subcommand prints progress to stdout before it can fail, so a
// real failure (exit >= 2) must be flagged as a tool error even when stdout is
// non-empty — keyed off the exit code, not stdout emptiness.
func TestRunFailureWithProgressOnStdoutIsError(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, "  Loading qwen\n", "Error: failed to load model", 3)}
	res := cfg.run(context.Background(), "load", "qwen")
	if !res.IsError {
		t.Fatal("exit 3 with progress on stdout must be flagged as error")
	}
	got := resultText(t, res)
	if !strings.Contains(got, "Error: failed to load model") {
		t.Errorf("text = %q, want it to contain the stderr message", got)
	}
	if !strings.Contains(got, "Loading qwen") {
		t.Errorf("text = %q, want it to carry stdout for context", got)
	}
}

// Exit 1 is an informational negative per the CLI's exit-code contract, even
// when the message lands on stderr rather than stdout.
func TestRunExitOneWithStderrOnlyIsNotError(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, "", "no server running", 1)}
	res := cfg.run(context.Background(), "status")
	if res.IsError {
		t.Error("exit 1 should not be flagged as error")
	}
	if len(res.Content) != 1 {
		t.Fatalf("content items = %d, want exactly 1 (the stderr text)", len(res.Content))
	}
	if got := resultText(t, res); got != "no server running" {
		t.Errorf("text = %q", got)
	}
}

// A warning on stderr (e.g. the plaintext-key notice) must not be fused into
// stdout: the JSON payload stays alone in Content[0] so it still parses, and
// the warning follows as Content[1].
func TestRunStderrWarningFollowsStdoutAsSecondItem(t *testing.T) {
	const warning = "warning: plaintext api_key in config"
	cfg := &config{llamaLauncherBin: fakeCLI(t, `[{"name":"qwen"}]`, warning+"\n", 0)}

	res := cfg.run(context.Background(), "list", "--json")

	if res.IsError {
		t.Fatal("exit 0 with a stderr warning should not be flagged as error")
	}
	if len(res.Content) != 2 {
		t.Fatalf("content items = %d, want 2 (stdout, then stderr)", len(res.Content))
	}
	var profiles []map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &profiles); err != nil {
		t.Errorf("Content[0] does not parse as JSON: %v", err)
	}
	second, ok := res.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[1] is %T, want *mcp.TextContent", res.Content[1])
	}
	if second.Text != warning {
		t.Errorf("Content[1] = %q, want %q", second.Text, warning)
	}
}

// A successful run that writes nothing to either stream still returns content:
// the single "(no output)" item.
func TestRunNoOutputReturnsPlaceholder(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, "", "", 0)}

	res := cfg.run(context.Background(), "unload")

	if res.IsError {
		t.Fatal("exit 0 with no output should not be flagged as error")
	}
	if len(res.Content) != 1 {
		t.Fatalf("content items = %d, want 1", len(res.Content))
	}
	if got := resultText(t, res); got != noOutputText {
		t.Errorf("text = %q, want %q", got, noOutputText)
	}
}

// A non-zero exit with no stdout is a real failure: flag it and surface stderr.
func TestRunFailureSurfacesStderr(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, "", "Error: no such profile", 2)}
	res := cfg.run(context.Background(), "load", "ghost")
	if !res.IsError {
		t.Fatal("failure with no stdout should be flagged as error")
	}
	if got := resultText(t, res); got != "Error: no such profile" {
		t.Errorf("text = %q", got)
	}
}

func TestRunForwardsConfigPath(t *testing.T) {
	// The fake CLI echoes its own args so we can assert --config is forwarded.
	dir := t.TempDir()
	path := filepath.Join(dir, "echo-args")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config{llamaLauncherBin: path, configPath: "/tmp/cfg.yaml"}
	got := resultText(t, cfg.run(context.Background(), "status", "--json"))
	want := "--config /tmp/cfg.yaml status --json"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// A fake CLI that floods stdout with more than the cap must come back as the
// capped prefix plus a truncation notice, not the full unbounded output.
func TestRunCapsOversizedOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flood")
	script := "#!/bin/sh\nyes flood | head -c " + itoa(2*maxCapturedOutput) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config{llamaLauncherBin: path}
	res := cfg.run(context.Background(), "logs")
	if res.IsError {
		t.Fatal("truncated output should still be a normal result, not an error")
	}
	got := resultText(t, res)
	if maxLen := maxCapturedOutput + len(truncationNotice) + 1; len(got) > maxLen {
		t.Errorf("output length = %d, want <= %d", len(got), maxLen)
	}
	if !strings.HasSuffix(got, truncationNotice) {
		t.Errorf("output does not end with truncation notice %q; tail = %q", truncationNotice, got[len(got)-80:])
	}
}

func TestLimitedWriter(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		writes    []string
		want      string
		truncated bool
	}{
		{"under cap", 10, []string{"ab", "cd"}, "abcd", false},
		{"exactly cap is not truncation", 5, []string{"abcde"}, "abcde", false},
		{"over cap in one write", 5, []string{"abcdefgh"}, "abcde", true},
		{"over cap across writes", 6, []string{"abcd", "efgh"}, "abcdef", true},
		{"writes after cap are discarded", 5, []string{"abcde", "fgh"}, "abcde", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &limitedWriter{limit: tt.limit}
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil {
					t.Fatalf("Write(%q) error: %v", s, err)
				}
				if n != len(s) {
					t.Errorf("Write(%q) = %d, want %d (must report full consumption)", s, n, len(s))
				}
			}
			if got := w.b.String(); got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
			if w.truncated != tt.truncated {
				t.Errorf("truncated = %v, want %v", w.truncated, tt.truncated)
			}
		})
	}
}

// TestRunErrorPathCapsBothStreams composes the exit-code discriminator with
// the output cap: a failing command that floods stdout and stderr must come
// back as a tool error whose text stays bounded, with a truncation notice for
// each capped stream — the path a failing model load with a runaway log hits.
func TestRunErrorPathCapsBothStreams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flood-fail")
	script := "#!/bin/sh\n" +
		"yes out | head -c " + itoa(2*maxCapturedOutput) + "\n" +
		"yes err | head -c " + itoa(2*maxCapturedOutput) + " 1>&2\n" +
		"exit 3\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config{llamaLauncherBin: path}

	res := cfg.run(context.Background(), "load", "x")

	if !res.IsError {
		t.Fatal("exit 3 must be a tool error regardless of output volume")
	}
	got := resultText(t, res)
	if maxLen := 2*(maxCapturedOutput+len(truncationNotice)) + 2; len(got) > maxLen {
		t.Errorf("error text length = %d, want <= %d", len(got), maxLen)
	}
	if n := strings.Count(got, truncationNotice); n != 2 {
		t.Errorf("truncation notices = %d, want 2 (one per capped stream)", n)
	}
}

// blockingCLI writes a fake llama-launcher that answers `stop` at once but
// otherwise marks itself inside (an in.<pid> file in dir) and blocks until a
// release file appears in dir, so a test can count the subprocesses running
// at once and decide when they finish.
func blockingCLI(t *testing.T) (bin, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI script is POSIX shell")
	}
	dir = t.TempDir()
	bin = filepath.Join(dir, "fake-llama-launcher")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = stop ]; then echo stopped; exit 0; fi\n" +
		"touch \"" + dir + "/in.$$\"\n" +
		"while [ ! -e \"" + dir + "/release\" ]; do sleep 0.02; done\n" +
		"rm -f \"" + dir + "/in.$$\"\n" +
		"echo done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return bin, dir
}

// insideCount reports how many blockingCLI subprocesses are currently inside.
func insideCount(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "in.*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

// waitInside polls until exactly want blockingCLI subprocesses are inside,
// failing the test if that does not happen within a few seconds.
func waitInside(t *testing.T, dir string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if insideCount(t, dir) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inside = %d, want %d", insideCount(t, dir), want)
}

// startBlockedRuns launches n concurrent cfg.run calls against a blockingCLI
// and returns a function that releases them and collects their results. The
// release also runs at cleanup, so a failing test never leaks subprocesses.
func startBlockedRuns(t *testing.T, cfg *config, dir string, n int) func() []*mcp.CallToolResult {
	t.Helper()
	results := make([]*mcp.CallToolResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = cfg.run(context.Background(), "load", "qwen")
		}(i)
	}
	var once sync.Once
	release := func() []*mcp.CallToolResult {
		once.Do(func() {
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o644); err != nil {
				t.Errorf("write release: %v", err)
			}
			wg.Wait()
		})
		return results
	}
	t.Cleanup(func() { release() })
	return release
}

func TestRunBoundsInFlightSubprocesses(t *testing.T) {
	bin, dir := blockingCLI(t)
	cfg := &config{llamaLauncherBin: bin, slots: make(chan struct{}, maxInFlight)}
	release := startBlockedRuns(t, cfg, dir, maxInFlight+2)

	waitInside(t, dir, maxInFlight)
	time.Sleep(200 * time.Millisecond) // give a fifth run the chance to slip in
	if got := insideCount(t, dir); got != maxInFlight {
		t.Fatalf("inside = %d while slots are held, want %d", got, maxInFlight)
	}

	for i, res := range release() {
		if res.IsError {
			t.Errorf("run %d: IsError, text %q", i, resultText(t, res))
		}
	}
}

func TestRunCanceledWhileWaitingForSlotIsError(t *testing.T) {
	bin, dir := blockingCLI(t)
	cfg := &config{llamaLauncherBin: bin, slots: make(chan struct{}, maxInFlight)}
	startBlockedRuns(t, cfg, dir, maxInFlight)
	waitInside(t, dir, maxInFlight)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := cfg.run(ctx, "load", "qwen")
	if !res.IsError {
		t.Fatal("a run canceled while waiting for a slot should be flagged as error")
	}
	if got := resultText(t, res); got != slotWaitCanceledText {
		t.Errorf("text = %q, want %q", got, slotWaitCanceledText)
	}
	if got := insideCount(t, dir); got != maxInFlight {
		t.Errorf("inside = %d, want %d: the canceled call must not run the CLI", got, maxInFlight)
	}
}

func TestStopServerBypassesInFlightCap(t *testing.T) {
	bin, dir := blockingCLI(t)
	cfg := &config{llamaLauncherBin: bin}
	s := startAdapter(t, cfg, loopbackAllow(t)) // newServer sizes cfg.slots
	if cap(cfg.slots) != maxInFlight {
		t.Fatalf("slots cap = %d, want %d", cap(cfg.slots), maxInFlight)
	}
	startBlockedRuns(t, cfg, dir, maxInFlight)
	waitInside(t, dir, maxInFlight)

	if got := callText(t, s, "stop_server", map[string]any{}); got != "stopped" {
		t.Errorf("stop_server text = %q, want %q", got, "stopped")
	}
}

func TestRunWithoutSlotsIsUnbounded(t *testing.T) {
	bin, dir := blockingCLI(t)
	cfg := &config{llamaLauncherBin: bin}
	release := startBlockedRuns(t, cfg, dir, maxInFlight+2)

	waitInside(t, dir, maxInFlight+2)
	release()
}
