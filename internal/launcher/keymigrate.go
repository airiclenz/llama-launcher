package launcher

// The key migration, as policy: which `servers:` entries still carry their API key as plain text in
// the config file, the move that gets one of them out of there, and the notice for the runs that
// cannot offer the move at all.
//
// The move is assembled HERE rather than inside either half it is made of. internal/keystore knows
// how to hold a secret and configwrite.go knows how to rewrite one entry of the file, but "write it,
// read it back through the command you are about to persist, and only then rewrite" is a policy
// about a user's credential rather than a fact about either. Nothing in this file has a user
// interface: it takes an answer and carries it out, so the same engine serves the interactive menu's
// offer and the notice a non-interactive command prints.
//
// Nothing here migrates anything on its own. A plaintext key with nowhere to go earns a notice
// naming the manual alternatives, and the move runs only on the user's own answer.
//
// Adapted from apogee's cmd/apogee/keymigrate.go at commit 1c0037b.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/airiclenz/llama-launcher/internal/keystore"
)

// secretStore is the half of [keystore.Store] this file uses, declared here so the sequence below
// can be exercised against a fake: the consumer names the interface it needs and the provider
// returns its concrete type.
type secretStore interface {
	Name() string
	Write(entry, key string) error
	ReadCmd(entry string) string
}

// probeKeyStore is the production probe, adapting [keystore.Probe] to the interface above. It is a
// function value the caller passes in rather than something the decision calls directly, so the
// choice between an offer and a notice can be tested from a machine of any kind.
func probeKeyStore() (secretStore, bool) {
	store, ok := keystore.Probe()
	if !ok {
		return nil, false
	}
	return store, true
}

// plaintextKeyServers names the enabled `servers:` entries whose key is sitting in the config file:
// a literal api_key and no plaintext_key_ok beside it.
//
// The marker is the user's own "never for this entry", so an entry carrying it is not a candidate
// and is not counted in the notice either — an acknowledged decision that keeps being reported is a
// decision the user is being asked to make again. A disabled entry is left alone for the reason
// resolveKeyCommands skips one: the launcher never talks to that server, so its key is not part of
// this run. An entry with a command source has no literal to move, and validation has already
// refused any entry that names both sources.
//
// The names come back sorted. The servers section is a map, so sorted order is the only order that
// is the same on two consecutive runs — and a notice or an offer that reshuffles its list between
// launches reads as if the list had changed.
func plaintextKeyServers(cfg *Config) []string {
	if cfg == nil {
		return nil
	}

	names := make([]string, 0, len(cfg.Servers))
	for name, sc := range cfg.Servers {
		if !sc.Enabled || sc.PlaintextKeyOK || strings.TrimSpace(sc.APIKey) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// migrateKey moves one entry's key out of the config file and into the store, in the one order that
// can never lose a key or leave a config file pointing at nothing:
//
//  1. WRITE the secret into the store. Until this succeeds nothing has changed anywhere.
//  2. READ IT BACK — by running the exact api_key_cmd line that is about to be persisted, through
//     the resolver's own exec path, and comparing what comes out to what went in. "The store tool
//     exited 0" is a much weaker claim than "the command that will live in this file produces this
//     key": quoting, an account name the store folded, a tool that stored something else under the
//     same attributes — every one of those passes step 1 and fails here.
//  3. REWRITE the entry, and only then. A failed read or a mismatch leaves the config exactly as it
//     was, so the run keeps working off the literal it already has and the user is told why.
//
// The key is trimmed once, up front, and it is the trimmed form that is both stored and compared:
// that is the key APIKeyFor hands the server, and it is the only form the round trip could return
// anyway, since runKeyCommand trims what a command prints.
func migrateKey(store secretStore, path, name, key string) error {
	key = strings.TrimSpace(key)
	if err := store.Write(name, key); err != nil {
		return err
	}

	command := store.ReadCmd(name)
	got, err := runKeyCommand(name, command, keyCommandTimeout)
	if err != nil {
		return fmt.Errorf("llama-launcher: server %q: the key is now in %s, but reading it back with %q "+
			"failed, so %s was left alone: %w", name, store.Name(), command, path, err)
	}
	if got != key {
		return fmt.Errorf("llama-launcher: server %q: %s handed back a different key than the one just "+
			"stored, so %s was left alone — check %q by hand before trying again",
			name, store.Name(), path, command)
	}
	return SaveServerKeyCommand(path, name, command)
}

// migrateKeys carries out a "move them" answer for a whole run of entries: one migrateKey each, in
// the order the offer named them, stopping at the first failure.
//
// The lines it returns name the entries that actually moved, and it returns them alongside the error
// that stopped it rather than instead of them: each successful entry has already had the config file
// rewritten under it, so a run that gives up half way must still be able to say which half.
func migrateKeys(store secretStore, cfg *Config, names []string) ([]string, error) {
	moved := make([]string, 0, len(names))
	for _, name := range names {
		if err := migrateKey(store, cfg.ConfigPath, name, cfg.Servers[name].APIKey); err != nil {
			return moved, err
		}
		moved = append(moved, fmt.Sprintf("%s: key moved into %s — %s now reads it with api_key_cmd",
			name, store.Name(), cfg.ConfigPath))
	}
	return moved, nil
}

// keepPlaintextKeys carries out a "never for these entries" answer: plaintext_key_ok: true on each
// named entry, which is what stops the offer coming back for it. Same order and same stop-at-the-
// first-failure rule as migrateKeys; the key itself is not touched, because keeping it where it is
// was the answer.
func keepPlaintextKeys(cfg *Config, names []string) ([]string, error) {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if err := SaveServerPlaintextKeyOK(cfg.ConfigPath, name); err != nil {
			return kept, err
		}
		kept = append(kept, fmt.Sprintf("%s: plaintext_key_ok: true recorded in %s", name, cfg.ConfigPath))
	}
	return kept, nil
}

// The two reasons a plaintext key is reported rather than offered a move. Which one is true is the
// caller's knowledge, not the notice's: the interactive path reaches the notice only after a probe
// found no store, while a command that will never prompt does not probe at all and would be
// asserting something it never checked if it said the same. Each is a lower-case clause with no
// trailing period, because plaintextKeyNotice folds it into the middle of its first sentence.
const (
	reasonNoStore  = "this machine has no secret store llama-launcher can move it into"
	reasonNoPrompt = "this command never prompts, so no offer to move it into a secret store is coming — " +
		"run llama-launcher with no arguments for that offer"
)

// plaintextKeyNotice is what a run that cannot offer a migration says instead: which entries hold a
// key in the file, why no offer is coming, and the three things their owner can do about it without
// the launcher's help. The reason comes from the caller — see the two constants above.
//
// It carries no "llama-launcher:" or "warning:" prefix of its own, because both callers print it
// through a sink that has one.
//
// It names the file the run actually read rather than the default path, because --config and
// LLAMA_LAUNCHER_CONFIG both move it, and a notice pointing at a file the run never opened would
// send the user to edit the wrong one.
func plaintextKeyNotice(path string, reason string, names []string) string {
	if len(names) == 0 {
		return ""
	}

	subject := "the api_key for " + strings.Join(names, ", ") + " is"
	if len(names) > 1 {
		subject = "the api_keys for " + strings.Join(names, ", ") + " are"
	}
	return fmt.Sprintf("%s stored in plain text in %s, and %s. You can point the entry at any command "+
		"that prints the key instead (api_key_cmd: pass show llama-launcher/llamacpp — the line is handed "+
		"to a shell, so a pipeline works), or at least keep the file to your own account (chmod 600 %s). "+
		"Adding `plaintext_key_ok: true` to an entry answers this for good.",
		subject, path, reason, path)
}

// keyOffer is what a run should do about the plaintext keys it found, decided before any of it is
// drawn: nothing at all (no Names), say something (Notice), or ask (Store non-nil).
type keyOffer struct {
	Names  []string
	Store  secretStore
	Notice string
}

// decideKeyOffer answers "ask, tell, or stay quiet?" — the whole decision, with no terminal in it,
// so the interactive path's behaviour can be checked from a machine of any kind.
//
// The probe is a parameter rather than a call, and it is reached only when there is something to
// offer: a probe is a subprocess, and a run whose keys are all somewhere sensible has no question to
// ask, so it must not pay for the answer.
func decideKeyOffer(cfg *Config, probe func() (secretStore, bool)) keyOffer {
	names := plaintextKeyServers(cfg)
	if len(names) == 0 {
		return keyOffer{}
	}

	store, ok := probe()
	if !ok {
		return keyOffer{Names: names, Notice: plaintextKeyNotice(cfg.ConfigPath, reasonNoStore, names)}
	}
	return keyOffer{Names: names, Store: store}
}
