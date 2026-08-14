package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The file every rewrite below starts from: comments, a blank line, several
// server entries in both forms, and an api_key whose value is aligned and
// annotated — the shapes a writer that reformats would flatten.
const keySourceFixture = `# llama-launcher configuration
models_dir: ~/models

servers:
  llamacpp:
    enabled: true
    api_key:   sk-plaintext    # the key llama-server expects
  lmstudio:
    enabled: false
    api_key: sk-other
  ollama: true

profiles:
  small:
    model: small.gguf
`

// writeConfigFixture writes text to a config file in a fresh temp directory and
// returns its path.
func writeConfigFixture(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// configFileText reads a config file back as text.
func configFileText(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// The whole contract in one assertion: the api_key line becomes an api_key_cmd
// line, keeping its indentation, its alignment gap and its end-of-line note,
// and every other byte of the user's file — comments, blank line, key order,
// the other two entries — comes back unchanged.
func TestSaveServerKeyCommand_RewritesOnlyTheKeyLine(t *testing.T) {
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)
	command := "security find-generic-password -w -s llama-launcher -a llamacpp"

	if err := SaveServerKeyCommand(path, "llamacpp", command); err != nil {
		t.Fatalf("SaveServerKeyCommand: %v", err)
	}

	want := strings.Replace(keySourceFixture,
		"    api_key:   sk-plaintext    # the key llama-server expects",
		"    api_key_cmd:   "+command+"    # the key llama-server expects", 1)
	if got := configFileText(t, path); got != want {
		t.Errorf("rewritten config =\n%s\nwant\n%s", got, want)
	}
}

// The marshaller owns the quoting, so a command carrying YAML's own punctuation
// lands as a value the parser hands back verbatim rather than as a syntax break.
func TestSaveServerKeyCommand_QuotesTheCommand(t *testing.T) {
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)
	command := `sh -c 'pass show llm/key: #1'`

	if err := SaveServerKeyCommand(path, "llamacpp", command); err != nil {
		t.Fatalf("SaveServerKeyCommand: %v", err)
	}

	cfg, err := parseConfig(path)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	entry := cfg.Servers["llamacpp"]
	if entry.APIKeyCmd != command {
		t.Errorf("api_key_cmd = %q, want %q", entry.APIKeyCmd, command)
	}
	if entry.APIKey != "" {
		t.Errorf("api_key = %q, want the literal to be gone", entry.APIKey)
	}
}

// The key the rewritten entry names is the key the launcher then sends: the
// written line survives a real load, command and all.
func TestSaveServerKeyCommand_SurvivesTheLoad(t *testing.T) {
	requirePOSIXShell(t)

	path := writeConfigFixture(t, keySourceFixture)

	if err := SaveServerKeyCommand(path, "llamacpp", "echo sk-from-store"); err != nil {
		t.Fatalf("SaveServerKeyCommand: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.APIKeyFor("llamacpp"); got != "sk-from-store" {
		t.Errorf("APIKeyFor = %q, want %q", got, "sk-from-store")
	}
}

// A re-offer must not churn the file: an entry already pointed at exactly this
// command is a confirmation, not a rewrite.
func TestSaveServerKeyCommand_AlreadyPointedAtTheCommandIsANoOp(t *testing.T) {
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)
	command := "echo sk-from-store"
	if err := SaveServerKeyCommand(path, "llamacpp", command); err != nil {
		t.Fatalf("first SaveServerKeyCommand: %v", err)
	}
	rewritten := configFileText(t, path)

	if err := SaveServerKeyCommand(path, "llamacpp", command); err != nil {
		t.Fatalf("second SaveServerKeyCommand: %v", err)
	}

	if got := configFileText(t, path); got != rewritten {
		t.Errorf("second write changed the file:\n%s\nwant\n%s", got, rewritten)
	}
}

// The "never for this entry" answer lands as a new last setting of the entry's
// own block, at its siblings' indentation, and touches nothing else.
func TestSaveServerPlaintextKeyOK_AppendsTheMarker(t *testing.T) {
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)

	if err := SaveServerPlaintextKeyOK(path, "lmstudio"); err != nil {
		t.Fatalf("SaveServerPlaintextKeyOK: %v", err)
	}

	want := strings.Replace(keySourceFixture,
		"    api_key: sk-other\n",
		"    api_key: sk-other\n    plaintext_key_ok: true\n", 1)
	if got := configFileText(t, path); got != want {
		t.Errorf("rewritten config =\n%s\nwant\n%s", got, want)
	}
}

// The marker still lands when the entry is the last thing in the file — the
// shape of a config whose servers section is written at the bottom, and the one
// case where the appended line has no following line to sit above. A file that
// ended without a newline gets one, which is the only byte a rewrite adds.
func TestSaveServerPlaintextKeyOK_AppendsAtTheEndOfTheFile(t *testing.T) {
	t.Parallel()

	config := "profiles:\n  small:\n    model: small.gguf\n\nservers:\n  llamacpp:\n    api_key: sk-plaintext"
	path := writeConfigFixture(t, config)

	if err := SaveServerPlaintextKeyOK(path, "llamacpp"); err != nil {
		t.Fatalf("SaveServerPlaintextKeyOK: %v", err)
	}

	want := config + "\n    plaintext_key_ok: true\n"
	if got := configFileText(t, path); got != want {
		t.Errorf("rewritten config =\n%s\nwant\n%s", got, want)
	}
}

// An entry that once said "ask me again" is answered on its own line, note and
// alignment kept, rather than by a second marker further down.
func TestSaveServerPlaintextKeyOK_RewritesAnExistingFalse(t *testing.T) {
	t.Parallel()

	fixture := strings.Replace(keySourceFixture,
		"    api_key: sk-other\n",
		"    api_key: sk-other\n    plaintext_key_ok:  false   # asked once already\n", 1)
	path := writeConfigFixture(t, fixture)

	if err := SaveServerPlaintextKeyOK(path, "lmstudio"); err != nil {
		t.Fatalf("SaveServerPlaintextKeyOK: %v", err)
	}

	want := strings.Replace(fixture,
		"    plaintext_key_ok:  false   # asked once already",
		"    plaintext_key_ok:  true   # asked once already", 1)
	if got := configFileText(t, path); got != want {
		t.Errorf("rewritten config =\n%s\nwant\n%s", got, want)
	}
}

// An entry already carrying the answer is left exactly as it is.
func TestSaveServerPlaintextKeyOK_AlreadyTrueIsANoOp(t *testing.T) {
	t.Parallel()

	fixture := strings.Replace(keySourceFixture,
		"    api_key: sk-other\n",
		"    api_key: sk-other\n    plaintext_key_ok: true\n", 1)
	path := writeConfigFixture(t, fixture)

	if err := SaveServerPlaintextKeyOK(path, "lmstudio"); err != nil {
		t.Fatalf("SaveServerPlaintextKeyOK: %v", err)
	}

	if got := configFileText(t, path); got != fixture {
		t.Errorf("config changed:\n%s\nwant\n%s", got, fixture)
	}
}

// Every shape these edits cannot make surgically is refused with a pointer at
// the file, and — the half that matters — leaves the file exactly as it was.
// Guessing at any of them risks writing over a key source the user still needs.
func TestSaveServerKeySource_RefusesWhatItCannotEditSurgically(t *testing.T) {
	t.Parallel()

	spanningValue := strings.Replace(keySourceFixture,
		"    api_key:   sk-plaintext    # the key llama-server expects",
		"    api_key: sk-plaintext\n      continued-on-the-next-line", 1)
	blockValue := strings.Replace(keySourceFixture,
		"    api_key:   sk-plaintext    # the key llama-server expects",
		"    api_key: |\n      sk-plaintext", 1)
	flowEntry := strings.Replace(keySourceFixture,
		"  llamacpp:\n    enabled: true\n    api_key:   sk-plaintext    # the key llama-server expects\n",
		"  llamacpp: {enabled: true, api_key: sk-plaintext}\n", 1)
	flowServers := "servers: {llamacpp: {enabled: true, api_key: sk-x}}\nprofiles:\n  small:\n    model: small.gguf\n"

	tests := []struct {
		name    string
		config  string
		save    func(path string) error
		wantErr string
	}{
		{
			name:    "no entry of that name",
			config:  keySourceFixture,
			save:    func(path string) error { return SaveServerKeyCommand(path, "mystery", "echo sk") },
			wantErr: `no servers.mystery entry — it configures "llamacpp", "lmstudio", "ollama"`,
		},
		{
			name:    "entry written in the plain bool form",
			config:  keySourceFixture,
			save:    func(path string) error { return SaveServerKeyCommand(path, "ollama", "echo sk") },
			wantErr: "written as the plain ollama: true form",
		},
		{
			name:    "entry written in flow style",
			config:  flowEntry,
			save:    func(path string) error { return SaveServerKeyCommand(path, "llamacpp", "echo sk") },
			wantErr: "written in flow style ({...})",
		},
		{
			name:    "servers section written in flow style",
			config:  flowServers,
			save:    func(path string) error { return SaveServerKeyCommand(path, "llamacpp", "echo sk") },
			wantErr: "servers: section is written in flow style",
		},
		{
			name:    "api_key value continued on the next line",
			config:  spanningValue,
			save:    func(path string) error { return SaveServerKeyCommand(path, "llamacpp", "echo sk") },
			wantErr: "does not fit on its own line",
		},
		{
			name:    "api_key written as a block scalar",
			config:  blockValue,
			save:    func(path string) error { return SaveServerKeyCommand(path, "llamacpp", "echo sk") },
			wantErr: "does not fit on its own line",
		},
		{
			name:    "more than one YAML document",
			config:  keySourceFixture + "---\nservers:\n  ollama: true\n",
			save:    func(path string) error { return SaveServerPlaintextKeyOK(path, "llamacpp") },
			wantErr: "more than one YAML document",
		},
		{
			name:    "plain bool entry cannot carry the marker either",
			config:  keySourceFixture,
			save:    func(path string) error { return SaveServerPlaintextKeyOK(path, "ollama") },
			wantErr: "written as the plain ollama: true form",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfigFixture(t, tc.config)

			err := tc.save(path)

			if err == nil {
				t.Fatal("expected the rewrite to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name %s", err, path)
			}
			if got := configFileText(t, path); got != tc.config {
				t.Errorf("refused rewrite still changed the file:\n%s", got)
			}
		})
	}
}

// An entry naming no key source has no api_key line to replace, so the refusal
// says which entry and sends the user to the file rather than inventing a line.
func TestSaveServerKeyCommand_RefusesAnEntryWithoutAnAPIKeyLine(t *testing.T) {
	t.Parallel()

	config := "servers:\n  llamacpp:\n    enabled: true\nprofiles:\n  small:\n    model: small.gguf\n"
	path := writeConfigFixture(t, config)

	err := SaveServerKeyCommand(path, "llamacpp", "echo sk")

	if err == nil {
		t.Fatal("expected the rewrite to be refused")
	}
	if !strings.Contains(err.Error(), "servers.llamacpp entry has no api_key: line to replace") {
		t.Errorf("error = %q, want it to name the missing api_key line", err)
	}
	if got := configFileText(t, path); got != config {
		t.Errorf("refused rewrite still changed the file:\n%s", got)
	}
}

// A command with nothing in it names no program, so it is refused before the
// file is even read.
func TestSaveServerKeyCommand_RefusesAnEmptyCommand(t *testing.T) {
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)

	err := SaveServerKeyCommand(path, "llamacpp", "   ")

	if err == nil {
		t.Fatal("expected the rewrite to be refused")
	}
	if !strings.Contains(err.Error(), "the command is empty") {
		t.Errorf("error = %q, want it to say the command is empty", err)
	}
	if got := configFileText(t, path); got != keySourceFixture {
		t.Errorf("refused rewrite still changed the file:\n%s", got)
	}
}

// The rewrite replaces the config through a temporary file in its own
// directory, so an interrupted write leaves the old config rather than a
// truncated one — and the file it leaves behind is readable by its owner alone,
// whatever the mode it had before.
func TestSaveServerKeyCommand_WritesAtomicallyWithANarrowMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a POSIX contract; windows has no 0600")
	}
	t.Parallel()

	path := writeConfigFixture(t, keySourceFixture)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widening the fixture's mode: %v", err)
	}

	if err := SaveServerKeyCommand(path, "llamacpp", "echo sk-from-store"); err != nil {
		t.Fatalf("SaveServerKeyCommand: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want %o", got, 0o600)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reading the config directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Errorf("directory holds %v, want only the config file", entries)
	}
}

// A path nobody named, and a path nothing is at, are refused as such: the caller
// hears which file could not be read instead of a rewrite that silently did
// nothing.
func TestSaveServerKeySource_RefusesAnUnusablePath(t *testing.T) {
	t.Parallel()

	if err := SaveServerKeyCommand("", "llamacpp", "echo sk"); err == nil ||
		!strings.Contains(err.Error(), "no config file path is known") {
		t.Errorf("empty path error = %v, want it to say no path is known", err)
	}

	missing := filepath.Join(t.TempDir(), "absent.yaml")
	if err := SaveServerPlaintextKeyOK(missing, "llamacpp"); err == nil ||
		!strings.Contains(err.Error(), missing) {
		t.Errorf("missing file error = %v, want it to name %s", err, missing)
	}
}
