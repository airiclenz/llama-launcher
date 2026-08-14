package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSecretStore stands in for the machine's real store so the migration
// sequence can be driven from any machine: what Write does, and what the line
// the entry will be pointed at actually prints, are both dictated by the test.
//
// readCmd is handed back verbatim for every entry — it is a real shell command
// in these tests, because migrateKey verifies by RUNNING it through the same
// exec path the resolver uses, and a command that only looked plausible would
// make the read-back a formality.
type fakeSecretStore struct {
	name     string
	readCmd  string
	writeErr error

	written map[string]string
	writes  int
	reads   int
}

func (f *fakeSecretStore) Name() string { return f.name }

func (f *fakeSecretStore) Write(entry, key string) error {
	f.writes++
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.written == nil {
		f.written = make(map[string]string)
	}
	f.written[entry] = key
	return nil
}

func (f *fakeSecretStore) ReadCmd(entry string) string {
	f.reads++
	return f.readCmd
}

// newFakeStore is the ordinary store: it takes any key, and its read-back line
// prints exactly what the test says it should.
func newFakeStore(prints string) *fakeSecretStore {
	return &fakeSecretStore{name: "Test Store", readCmd: "printf %s " + prints}
}

// A run's candidates are the enabled entries holding a literal key, named in
// sorted order — the servers section is a map, so nothing else is stable
// between two launches of the same config.
func TestPlaintextKeyServers_NamesEnabledLiteralKeysSorted(t *testing.T) {
	t.Parallel()

	cfg := &Config{Servers: map[string]ServerConfig{
		"ollama":   {Enabled: true, APIKey: "sk-ollama"},
		"llamacpp": {Enabled: true, APIKey: "sk-llamacpp"},
		"lmstudio": {Enabled: false, APIKey: "sk-lmstudio"},
	}}

	got := plaintextKeyServers(cfg)
	want := []string{"llamacpp", "ollama"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("plaintextKeyServers() = %v, want %v", got, want)
	}
}

// Every shape that is NOT a plaintext key with somewhere better to be. The
// acknowledged one matters most: an entry whose owner has already answered
// "this one stays" must not be counted, or the notice keeps re-asking a
// question that was answered.
func TestPlaintextKeyServers_SkipsWhatIsNotACandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry ServerConfig
	}{
		{"disabled entry", ServerConfig{Enabled: false, APIKey: "sk-a"}},
		{"acknowledged plaintext key", ServerConfig{Enabled: true, APIKey: "sk-a", PlaintextKeyOK: true}},
		{"command source", ServerConfig{Enabled: true, APIKeyCmd: "echo sk-a"}},
		{"no key at all", ServerConfig{Enabled: true}},
		{"whitespace-only literal", ServerConfig{Enabled: true, APIKey: "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{Servers: map[string]ServerConfig{"llamacpp": tt.entry}}
			if got := plaintextKeyServers(cfg); len(got) != 0 {
				t.Errorf("plaintextKeyServers() = %v, want none", got)
			}
		})
	}
}

func TestPlaintextKeyServers_NilConfigHasNoCandidates(t *testing.T) {
	t.Parallel()

	if got := plaintextKeyServers(nil); len(got) != 0 {
		t.Errorf("plaintextKeyServers(nil) = %v, want none", got)
	}
}

// The whole move, in order: the key reaches the store, the line that reads it
// back is verified by running it, and only then does the entry stop naming a
// literal and start naming the command.
func TestMigrateKey_StoresTheKeyAndPointsTheEntryAtIt(t *testing.T) {
	requirePOSIXShell(t)

	path := writeConfigFixture(t, keySourceFixture)
	store := newFakeStore("sk-plaintext")

	if err := migrateKey(store, path, "llamacpp", "sk-plaintext"); err != nil {
		t.Fatalf("migrateKey: %v", err)
	}

	if got := store.written["llamacpp"]; got != "sk-plaintext" {
		t.Errorf("stored key = %q, want %q", got, "sk-plaintext")
	}
	cfg, err := parseConfig(path)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	entry := cfg.Servers["llamacpp"]
	if entry.APIKeyCmd != store.readCmd {
		t.Errorf("api_key_cmd = %q, want %q", entry.APIKeyCmd, store.readCmd)
	}
	if entry.APIKey != "" {
		t.Errorf("api_key = %q, want the literal to be gone", entry.APIKey)
	}
}

// A literal the file surrounded with whitespace is stored the way APIKeyFor
// hands it to the server, because that is also the only form the round trip
// could ever hand back: the resolver trims what a command prints.
func TestMigrateKey_StoresTheTrimmedKey(t *testing.T) {
	requirePOSIXShell(t)

	path := writeConfigFixture(t, keySourceFixture)
	store := newFakeStore("sk-plaintext")

	if err := migrateKey(store, path, "llamacpp", "  sk-plaintext\n"); err != nil {
		t.Fatalf("migrateKey: %v", err)
	}
	if got := store.written["llamacpp"]; got != "sk-plaintext" {
		t.Errorf("stored key = %q, want it trimmed to %q", got, "sk-plaintext")
	}
}

// A store that hands back something other than what went in has stored
// something else, or stored it somewhere this line does not read. The config is
// the user's only remaining copy of the key at that moment, so it is left
// exactly as it was and the failing command is quoted for them to check.
func TestMigrateKey_ReadBackMismatchLeavesTheConfigAlone(t *testing.T) {
	requirePOSIXShell(t)

	path := writeConfigFixture(t, keySourceFixture)
	store := newFakeStore("sk-something-else")

	err := migrateKey(store, path, "llamacpp", "sk-plaintext")
	if err == nil {
		t.Fatal("migrateKey succeeded on a read-back mismatch")
	}
	for _, want := range []string{"llamacpp", "Test Store", "different key", path, store.readCmd} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if got := configFileText(t, path); got != keySourceFixture {
		t.Errorf("config was rewritten:\n%s", got)
	}
}

// A read-back that fails outright says the same thing: the key is in the store,
// the file was not touched, and here is why the line did not work.
func TestMigrateKey_ReadBackFailureLeavesTheConfigAlone(t *testing.T) {
	requirePOSIXShell(t)

	path := writeConfigFixture(t, keySourceFixture)
	store := &fakeSecretStore{name: "Test Store", readCmd: "exit 3"}

	err := migrateKey(store, path, "llamacpp", "sk-plaintext")
	if err == nil {
		t.Fatal("migrateKey succeeded on a failing read-back command")
	}
	for _, want := range []string{"llamacpp", "Test Store", "reading it back", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if got := configFileText(t, path); got != keySourceFixture {
		t.Errorf("config was rewritten:\n%s", got)
	}
}

// Until the store has the key, nothing else may happen at all: no read-back to
// verify a secret that was never stored, and above all no rewrite pointing the
// entry at a command that would find nothing.
func TestMigrateKey_StoreRefusalStopsBeforeAnythingElse(t *testing.T) {
	t.Parallel()

	refused := errors.New("the store refused the key")
	path := writeConfigFixture(t, keySourceFixture)
	store := &fakeSecretStore{name: "Test Store", readCmd: "printf %s sk-plaintext", writeErr: refused}

	if err := migrateKey(store, path, "llamacpp", "sk-plaintext"); !errors.Is(err, refused) {
		t.Fatalf("migrateKey error = %v, want the store's own refusal", err)
	}
	if store.reads != 0 {
		t.Errorf("read back %d times after a refused write, want 0", store.reads)
	}
	if got := configFileText(t, path); got != keySourceFixture {
		t.Errorf("config was rewritten:\n%s", got)
	}
}

// The notice is the whole answer where no offer can be raised, so it has to
// carry everything the user needs: which entries, which file — the one this run
// read, not the default path — why nothing is being offered, and each way out
// they can take by hand.
func TestPlaintextKeyNotice_NamesEntriesPathReasonAndAlternatives(t *testing.T) {
	t.Parallel()

	const path = "/somewhere/else/config.yaml"
	notice := plaintextKeyNotice(path, reasonNoStore, []string{"llamacpp"})

	for _, want := range []string{
		"the api_key for llamacpp is stored in plain text in " + path,
		reasonNoStore,
		"api_key_cmd:",
		"chmod 600 " + path,
		"plaintext_key_ok: true",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q does not contain %q", notice, want)
		}
	}
}

// Several entries are reported in one notice rather than one each, and the
// sentence agrees with itself when it names more than one.
func TestPlaintextKeyNotice_NamesEveryEntryInOneSentence(t *testing.T) {
	t.Parallel()

	notice := plaintextKeyNotice("/c.yaml", reasonNoPrompt, []string{"llamacpp", "ollama"})

	if !strings.Contains(notice, "the api_keys for llamacpp, ollama are stored") {
		t.Errorf("notice does not name both entries in agreement: %q", notice)
	}
	if !strings.Contains(notice, reasonNoPrompt) {
		t.Errorf("notice %q does not carry the reason", notice)
	}
}

// The two reasons are the caller's knowledge, and each has to be true of the
// path that uses it: a run that never probed cannot claim there is no store,
// and a run that found none cannot blame the absence of a prompt.
func TestPlaintextKeyNotice_ReasonsSayWhatTheirCallerKnows(t *testing.T) {
	t.Parallel()

	if !strings.Contains(reasonNoStore, "no secret store") {
		t.Errorf("reasonNoStore = %q, want it to say the machine has no store", reasonNoStore)
	}
	if !strings.Contains(reasonNoPrompt, "llama-launcher with no arguments") {
		t.Errorf("reasonNoPrompt = %q, want it to point at the interactive run", reasonNoPrompt)
	}
}

// Nothing to report is nothing to say — the callers ask for the notice only
// when there are candidates, and an empty one must not become a stray line of
// output if that ever changes.
func TestPlaintextKeyNotice_NoEntriesIsNoNotice(t *testing.T) {
	t.Parallel()

	if got := plaintextKeyNotice("/c.yaml", reasonNoStore, nil); got != "" {
		t.Errorf("plaintextKeyNotice with no entries = %q, want empty", got)
	}
}

// Two enabled entries, each holding its own key: what a run over several
// candidates works on, and the only fixture where a store handing every entry
// the same key back would go unnoticed.
const twoPlaintextKeysFixture = `servers:
  llamacpp:
    enabled: true
    api_key: sk-llamacpp
  ollama:
    enabled: true
    api_key: sk-ollama

profiles:
  small:
    model: small.gguf
`

// roundTripStore is a store that really stores: its read-back line prints what
// Write was given for THAT entry, so a run over several entries only passes if
// each key went in under its own name and came back out under it.
type roundTripStore struct {
	written map[string]string
	failOn  string
	err     error
}

func (s *roundTripStore) Name() string { return "Test Store" }

func (s *roundTripStore) Write(entry, key string) error {
	if entry == s.failOn {
		return s.err
	}
	if s.written == nil {
		s.written = make(map[string]string)
	}
	s.written[entry] = key
	return nil
}

func (s *roundTripStore) ReadCmd(entry string) string {
	return "printf %s " + s.written[entry]
}

// twoKeyConfig is the in-memory config matching twoPlaintextKeysFixture, with
// the path of a freshly written copy of it.
func twoKeyConfig(t *testing.T) *Config {
	t.Helper()

	return &Config{
		ConfigPath: writeConfigFixture(t, twoPlaintextKeysFixture),
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true, APIKey: "sk-llamacpp"},
			"ollama":   {Enabled: true, APIKey: "sk-ollama"},
		},
	}
}

// The "move them" answer, over more than one entry: every key reaches the store
// under its own name, no literal is left in the file, and each line names the
// entry and the file that changed — the user is about to be handed those lines
// as the whole account of what just happened to their credentials.
func TestMigrateKeys_MovesEveryEntryAndNamesWhatChanged(t *testing.T) {
	requirePOSIXShell(t)

	cfg := twoKeyConfig(t)
	store := &roundTripStore{}

	lines, err := migrateKeys(store, cfg, []string{"llamacpp", "ollama"})
	if err != nil {
		t.Fatalf("migrateKeys: %v", err)
	}

	if store.written["llamacpp"] != "sk-llamacpp" || store.written["ollama"] != "sk-ollama" {
		t.Errorf("store holds %v, want each entry's own key", store.written)
	}
	// The fake's read-back line quotes the key it prints, so the file legitimately
	// still contains the characters — what must be gone is the api_key: source.
	text := configFileText(t, cfg.ConfigPath)
	if strings.Contains(text, "api_key: sk-llamacpp") || strings.Contains(text, "api_key: sk-ollama") {
		t.Errorf("a literal key source survived the move:\n%s", text)
	}
	if got := strings.Count(text, "api_key_cmd:"); got != 2 {
		t.Errorf("config names %d key commands, want 2:\n%s", got, text)
	}

	if len(lines) != 2 {
		t.Fatalf("reported %d lines, want one per entry: %v", len(lines), lines)
	}
	for i, name := range []string{"llamacpp", "ollama"} {
		for _, want := range []string{name, store.Name(), cfg.ConfigPath} {
			if !strings.Contains(lines[i], want) {
				t.Errorf("line %q does not mention %q", lines[i], want)
			}
		}
	}
}

// A failure part way through stops the run there and hands back what already
// moved. The entry that failed keeps its literal — its key is still only in the
// file — and the one that succeeded stays migrated, because its key really is
// in the store now.
func TestMigrateKeys_StopAtTheFirstFailureStillReportsWhatMoved(t *testing.T) {
	requirePOSIXShell(t)

	cfg := twoKeyConfig(t)
	refused := errors.New("the store refused the key")
	store := &roundTripStore{failOn: "ollama", err: refused}

	lines, err := migrateKeys(store, cfg, []string{"llamacpp", "ollama"})
	if !errors.Is(err, refused) {
		t.Fatalf("migrateKeys error = %v, want the store's own refusal", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "llamacpp") {
		t.Fatalf("reported %v, want only the entry that moved", lines)
	}

	text := configFileText(t, cfg.ConfigPath)
	if strings.Contains(text, "api_key: sk-llamacpp") {
		t.Errorf("the entry that moved kept its literal:\n%s", text)
	}
	if !strings.Contains(text, "api_key: sk-ollama") {
		t.Errorf("the entry that failed was rewritten anyway:\n%s", text)
	}
}

// The "never for these entries" answer writes the marker and nothing else: the
// keys stay exactly where their owner said they should, and the run stops being
// a candidate — which is what makes the answer stick across launches.
func TestKeepPlaintextKeys_RecordsTheAnswerAndLeavesTheKeys(t *testing.T) {
	t.Parallel()

	cfg := twoKeyConfig(t)

	lines, err := keepPlaintextKeys(cfg, []string{"llamacpp", "ollama"})
	if err != nil {
		t.Fatalf("keepPlaintextKeys: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("reported %d lines, want one per entry: %v", len(lines), lines)
	}
	for i, name := range []string{"llamacpp", "ollama"} {
		for _, want := range []string{name, cfg.ConfigPath, "plaintext_key_ok"} {
			if !strings.Contains(lines[i], want) {
				t.Errorf("line %q does not mention %q", lines[i], want)
			}
		}
	}

	saved, err := parseConfig(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("re-reading the config: %v", err)
	}
	for _, name := range []string{"llamacpp", "ollama"} {
		entry := saved.Servers[name]
		if !entry.PlaintextKeyOK {
			t.Errorf("%s does not carry plaintext_key_ok", name)
		}
		if entry.APIKey != "sk-"+name {
			t.Errorf("%s api_key = %q, want it left alone", name, entry.APIKey)
		}
	}
	if got := plaintextKeyServers(saved); len(got) != 0 {
		t.Errorf("plaintextKeyServers() = %v after the answer, want none", got)
	}
}

// countingProbe reports what the machine has, and how many times it was asked —
// the second half is the point: probing costs a subprocess.
func countingProbe(store secretStore, ok bool, calls *int) func() (secretStore, bool) {
	return func() (secretStore, bool) {
		*calls++
		return store, ok
	}
}

// A run whose keys are all somewhere sensible says nothing and, above all, asks
// the machine nothing: the probe must never run for a question that will not be
// put.
func TestDecideKeyOffer_NothingToOfferProbesNothing(t *testing.T) {
	t.Parallel()

	calls := 0
	cfg := &Config{ConfigPath: "/c.yaml", Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true, APIKey: "sk-a", PlaintextKeyOK: true},
	}}

	offer := decideKeyOffer(cfg, countingProbe(newFakeStore("sk-a"), true, &calls))

	if len(offer.Names) != 0 || offer.Store != nil || offer.Notice != "" {
		t.Errorf("decideKeyOffer() = %+v, want an empty offer", offer)
	}
	if calls != 0 {
		t.Errorf("probed %d times with nothing to offer, want 0", calls)
	}
}

// No store on this machine: there is nothing to offer the key, so the run says
// so — with the reason that is actually true of this path — and raises no offer
// it could not carry out.
func TestDecideKeyOffer_NoStoreYieldsTheNotice(t *testing.T) {
	t.Parallel()

	calls := 0
	cfg := &Config{ConfigPath: "/c.yaml", Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true, APIKey: "sk-a"},
	}}

	offer := decideKeyOffer(cfg, countingProbe(nil, false, &calls))

	if calls != 1 {
		t.Errorf("probed %d times, want exactly 1", calls)
	}
	if offer.Store != nil {
		t.Errorf("offer carries a store on a machine that has none: %+v", offer)
	}
	for _, want := range []string{"llamacpp", cfg.ConfigPath, reasonNoStore} {
		if !strings.Contains(offer.Notice, want) {
			t.Errorf("notice %q does not mention %q", offer.Notice, want)
		}
	}
}

// A store to move the key into makes it a question rather than a statement: the
// offer carries the store and the entries it is about, and no notice — the user
// is being asked, not told.
func TestDecideKeyOffer_AStoreYieldsAnOfferNamingTheEntries(t *testing.T) {
	t.Parallel()

	calls := 0
	store := newFakeStore("sk-a")
	cfg := &Config{ConfigPath: "/c.yaml", Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true, APIKey: "sk-a"},
		"ollama":   {Enabled: true, APIKey: "sk-b"},
	}}

	offer := decideKeyOffer(cfg, countingProbe(store, true, &calls))

	if calls != 1 {
		t.Errorf("probed %d times, want exactly 1", calls)
	}
	if offer.Store != secretStore(store) {
		t.Errorf("offer.Store = %v, want the probed store", offer.Store)
	}
	if strings.Join(offer.Names, ",") != "llamacpp,ollama" {
		t.Errorf("offer names %v, want both entries in sorted order", offer.Names)
	}
	if offer.Notice != "" {
		t.Errorf("offer carries a notice as well as a question: %q", offer.Notice)
	}
}

// The non-interactive half, end to end through the real dispatcher: a command
// that runs and exits cannot ask, so it says what it found on stderr — naming
// the entry, the config file this run actually read, and why no offer is coming
// — and then does its job.
func TestRun_NonInteractiveCommandNoticesAPlaintextKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "servers:\n" +
		"  llamacpp:\n" +
		"    enabled: true\n" +
		"    api_key: sk-plaintext\n" +
		"log_dir: " + dir + "\n" +
		"defaults:\n" +
		"  server: llamacpp\n" +
		"  host: 127.0.0.1\n" +
		"  port: 1\n" +
		"profiles:\n" +
		"  small:\n" +
		"    model: small.gguf\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	// LoadConfig pushes configured keys onto the package-global backends; clear
	// them again so this config's key does not outlive the test.
	t.Cleanup(func() { applyAPIKeys(&Config{}) })

	code := 0
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = Run([]string{"--config", path, "list"}) })
	})

	if code != 0 {
		t.Errorf("Run(list) = %d, want 0 — the notice must not stop the command", code)
	}
	for _, want := range []string{"warning: ", "llamacpp", path, reasonNoPrompt} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not mention %q", stderr, want)
		}
	}
}

// The production probe's "no store" answer must be a nil store, not a zero
// value wrapped in a non-nil interface: the caller decides between an offer and
// a notice on exactly this, and a typed nil would raise an offer no store can
// complete.
func TestProbeKeyStore_ReportsNoStoreAsNil(t *testing.T) {
	store, ok := probeKeyStore()

	if ok != (store != nil) {
		t.Fatalf("probeKeyStore() = (%v, %v), want a store exactly when it reports one", store, ok)
	}
	if ok && store.Name() == "" {
		t.Errorf("probeKeyStore() reported a store with no name")
	}
}
