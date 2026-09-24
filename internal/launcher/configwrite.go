package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The key-source writer: the half of a key migration that rewrites the config.
//
// Moving a plaintext api_key into the machine's secret store leaves the entry
// having to say where its key comes from now, and answering "never, this one
// stays" leaves it having to say that. Both are ONE line of a file the user
// owns and llama-launcher only reads, so both are written under the same
// contract: every comment, every key order and every other entry comes back
// byte-identical, because a writer that reformats a config the user hand-wrote
// has taken the file away from them.
//
// The shape is: parse for positions, splice text, re-parse the result and
// compare it against what the edit was supposed to produce, and only then
// write. Anything that cannot be done surgically — an entry the file does not
// carry, an entry written as the plain `llamacpp: true` form with no block to
// edit, flow style ({...}) with no line of its own per key, an api_key whose
// value runs past its own line — is refused with the path and an "edit the file
// by hand" pointer, leaving the file exactly as it was. Guessing at any of them
// risks writing over a key source the user still needs.

// The keys these edits write, and the section they address an entry inside —
// spelled as ServerConfig's UnmarshalYAML tags them, since the splice matches
// them in the node tree rather than through the decoder.
const (
	serversKey        = "servers"
	apiKeyKey         = "api_key"
	apiKeyCmdKey      = "api_key_cmd"
	plaintextKeyOKKey = "plaintext_key_ok"
)

// configFileMode is the mode a rewritten config lands with. A config that names
// an api_key_cmd is one indirection from a secret and may still hold a literal
// beside it, so a rewrite narrows the file to its owner rather than carrying a
// wider mode forward — the same mode GenerateExampleConfig creates it with.
const configFileMode = 0o600

// SaveServerKeyCommand points the servers.<name> entry at command as its key
// source: the entry's literal api_key line is replaced, in place, by the
// api_key_cmd line that runs command for the key. It is what a consented key
// migration persists once the secret is in the store and has been read back out
// of it again.
//
// An entry already pointed at exactly this command, with no literal beside it,
// is left alone — the write is a confirmation, not a rewrite, so a re-offer
// cannot churn the file.
func SaveServerKeyCommand(path, name, command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return errors.New("config: cannot point a server entry at a key command: the command is empty")
	}
	return saveServerKeySource(path, name, func(data []byte) ([]byte, error) {
		return setEntryKeyCommand(data, name, cmd)
	})
}

// SaveServerPlaintextKeyOK records plaintext_key_ok: true on the servers.<name>
// entry — the "never for this entry" answer to the migration offer, which is
// the user's statement that this key stays in the file.
//
// The marker is appended to the entry's block, or its existing line is
// rewritten when the entry already spells the key as false. An entry that
// already says true is left alone.
func SaveServerPlaintextKeyOK(path, name string) error {
	return saveServerKeySource(path, name, func(data []byte) ([]byte, error) {
		return setEntryPlaintextKeyOK(data, name)
	})
}

// saveServerKeySource is the shape both edits share: read the file, splice, and
// write it back atomically unless the splice reports there was nothing to
// change. A splice failure is qualified with the path, because a refusal about
// a file's SHAPE has to say which file the reader should open.
func saveServerKeySource(path, name string, splice func(data []byte) ([]byte, error)) error {
	if name == "" {
		return errors.New("config: cannot rewrite a server entry: no entry name was given")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("config: cannot rewrite a server entry: no config file path is known")
	}
	path = ExpandTilde(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}
	updated, err := splice(data)
	if err != nil {
		return fmt.Errorf("config: cannot rewrite %s: %w", path, err)
	}
	if updated == nil {
		return nil
	}
	return writeConfigAtomically(path, updated)
}

// setEntryKeyCommand returns the config bytes with the named entry's api_key
// line replaced by an api_key_cmd line, or nil bytes when the entry already
// reads that way. The command's text comes from the YAML marshaller, which owns
// the quoting — so a command carrying a #, a colon or a leading quote lands as
// a value a reader takes back out unchanged rather than as a syntax break.
func setEntryKeyCommand(data []byte, name, command string) ([]byte, error) {
	before, entry, err := serverEntryConfig(data, name)
	if err != nil {
		return nil, err
	}
	if entry.APIKey == "" && entry.APIKeyCmd == command {
		return nil, nil
	}
	text, err := renderYAMLScalar(command)
	if err != nil {
		return nil, err
	}
	updated, err := spliceEntryKeyCommand(data, name, text)
	if err != nil {
		return nil, err
	}
	want := entry
	want.APIKey, want.APIKeyCmd = "", command
	return verifiedEntrySplice(updated, before, name, want)
}

// setEntryPlaintextKeyOK returns the config bytes with the named entry marked
// plaintext_key_ok: true, or nil bytes when it already is.
func setEntryPlaintextKeyOK(data []byte, name string) ([]byte, error) {
	before, entry, err := serverEntryConfig(data, name)
	if err != nil {
		return nil, err
	}
	if entry.PlaintextKeyOK {
		return nil, nil
	}
	updated, err := spliceEntryPlaintextKeyOK(data, name)
	if err != nil {
		return nil, err
	}
	want := entry
	want.PlaintextKeyOK = true
	return verifiedEntrySplice(updated, before, name, want)
}

// serverEntryConfig parses the config the way the launcher reads it and reports
// the whole parsed file plus the entry named name — the before-state every
// verified splice compares against, and the refusal for a name the servers
// section does not carry.
func serverEntryConfig(data []byte, name string) (Config, ServerConfig, error) {
	var before Config
	if err := yaml.Unmarshal(data, &before); err != nil {
		return Config{}, ServerConfig{}, fmt.Errorf("it does not parse: %w", redactYAMLError(err))
	}
	entry, ok := before.Servers[name]
	if !ok {
		return Config{}, ServerConfig{}, fmt.Errorf(
			"it has no servers.%s entry — it configures %s; edit the file by hand",
			name, configuredServerNames(before.Servers))
	}
	return before, entry, nil
}

// configuredServerNames spells what the servers section DOES carry, for a
// refusal about a name it does not: the message answers "which entry, then?"
// without sending the reader back to the file.
func configuredServerNames(servers map[string]ServerConfig) string {
	if len(servers) == 0 {
		return "no servers at all"
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, strconv.Quote(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// spliceEntryKeyCommand rewrites the entry's api_key line as an api_key_cmd
// line, keeping the user's own indentation, the gap they aligned the value
// with, and any end-of-line note — the line is theirs, and only its key and its
// value are this edit's business.
func spliceEntryKeyCommand(data []byte, name, text string) ([]byte, error) {
	lines, entry, err := serverEntryNode(data, name)
	if err != nil {
		return nil, err
	}
	keyNode, valueNode := mappingEntry(entry, apiKeyKey)
	if keyNode == nil {
		return nil, fmt.Errorf(
			"its servers.%s entry has no %s: line to replace; add the key source by hand", name, apiKeyKey)
	}
	head, gap, tail, err := scalarLineParts(lines, apiKeyKey, keyNode, valueNode)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(lines)
	out[keyNode.Line-1] = strings.TrimSuffix(head, apiKeyKey+":") + apiKeyCmdKey + ":" + gap + text + tail
	return joinConfigLines(out), nil
}

// spliceEntryPlaintextKeyOK writes the marker into the entry's block: over the
// line the entry already spells it on, or — the ordinary case — as a new last
// child of the block, at the indentation its siblings use.
//
// The insertion point is the last line the entry's own subtree reaches, which
// is where the next sibling key would go. A value whose text the node tree
// cannot measure would put that line somewhere inside the entry instead;
// nothing here tries to detect that, because verifiedEntrySplice reads the
// result back and refuses any edit that moved a value nobody asked it to touch.
func spliceEntryPlaintextKeyOK(data []byte, name string) ([]byte, error) {
	lines, entry, err := serverEntryNode(data, name)
	if err != nil {
		return nil, err
	}
	if keyNode, valueNode := mappingEntry(entry, plaintextKeyOKKey); keyNode != nil {
		head, gap, tail, err := scalarLineParts(lines, plaintextKeyOKKey, keyNode, valueNode)
		if err != nil {
			return nil, err
		}
		out := slices.Clone(lines)
		out[keyNode.Line-1] = head + gap + "true" + tail
		return joinConfigLines(out), nil
	}
	at := maxNodeLine(entry)
	if at < 1 || at > len(lines) {
		return nil, fmt.Errorf(
			"its servers.%s entry points at line %d, which is outside the file; edit the file by hand", name, at)
	}
	marker := strings.Repeat(" ", entry.Column-1) + plaintextKeyOKKey + ": true"
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, marker)
	out = append(out, lines[at:]...)
	return joinConfigLines(out), nil
}

// serverEntryNode finds the block mapping of the servers entry named name in
// the parsed document, and returns it with the file's lines — the two views a
// splice works between.
//
// The shapes it refuses are the ones where those two views would disagree about
// which line to edit: no servers section, a section written in flow style
// ([...] / {...}) or holding something other than a mapping, an entry written
// in the plain bool form (`llamacpp: true`), which has no block for a key
// source to live in, and an entry written as a flow mapping, which has no line
// of its own per key.
func serverEntryNode(data []byte, name string) ([]string, *yaml.Node, error) {
	root, err := configRootMapping(data)
	if err != nil {
		return nil, nil, err
	}
	keyNode, servers := mappingEntry(root, serversKey)
	switch {
	case keyNode == nil:
		return nil, nil, errors.New("it has no servers: section; edit the file by hand")
	case servers.Style&yaml.FlowStyle != 0:
		return nil, nil, errors.New("its servers: section is written in flow style; edit the file by hand")
	case servers.Kind != yaml.MappingNode:
		return nil, nil, errors.New(
			"its servers: holds something other than a mapping of server entries; edit the file by hand")
	}
	entryKey, entry := mappingEntry(servers, name)
	switch {
	case entryKey == nil:
		return nil, nil, fmt.Errorf("its servers: section has no %s: entry; edit the file by hand", name)
	case entry.Kind == yaml.ScalarNode:
		return nil, nil, fmt.Errorf(
			"its servers.%s entry is written as the plain %s: %s form, which has no block for a key source; "+
				"give it a block of settings by hand", name, name, entry.Value)
	case entry.Kind != yaml.MappingNode:
		return nil, nil, fmt.Errorf(
			"its servers.%s entry is not a block of settings; edit the file by hand", name)
	case entry.Style&yaml.FlowStyle != 0:
		return nil, nil, fmt.Errorf(
			"its servers.%s entry is written in flow style ({...}); edit the file by hand", name)
	}
	return splitConfigLines(data), entry, nil
}

// configRootMapping decodes the config's single YAML document and returns its
// top-level mapping. A second document is refused: the parser reads only the
// first, so an edit landing in the last one would be written and never read.
func configRootMapping(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("it holds no settings at all; edit the file by hand")
		}
		return nil, redactYAMLError(err)
	}
	var second yaml.Node
	switch err := decoder.Decode(&second); {
	case err == nil:
		return nil, errors.New(
			"it holds more than one YAML document, and only the first is ever read; edit the file by hand")
	case !errors.Is(err, io.EOF):
		return nil, redactYAMLError(err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("its top level is not a mapping of settings; edit the file by hand")
	}
	if doc.Content[0].Style&yaml.FlowStyle != 0 {
		return nil, errors.New("its top level is written in flow style ({...}); edit the file by hand")
	}
	return doc.Content[0], nil
}

// mappingEntry finds one key of a block mapping and returns its key and value
// nodes, or two nils when the mapping does not have it. The first match wins,
// which is also the only match a config the launcher can read has: a duplicate
// key fails the parse both edits do before they splice.
func mappingEntry(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// maxNodeLine is the last line the node's subtree reaches — for an entry whose
// settings each sit on their own line, the line to append the next setting
// after.
func maxNodeLine(n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		if l := maxNodeLine(c); l > last {
			last = l
		}
	}
	return last
}

// scalarLineParts splits the key's line into the text through its colon, the
// gap before the value, and any trailing comment — the pieces a replacement
// rebuilds the line from, keeping the user's own indentation, alignment and
// end-of-line note.
//
// Splitting it is also the check that the line IS a single plain `key: value`,
// which is what makes rewriting it safe: a value spanning lines, a block
// scalar, or a key the text spells differently from the node tree is refused,
// since rewriting one line of it would leave the rest of the old value behind.
func scalarLineParts(lines []string, key string, keyNode, valueNode *yaml.Node) (head, gap, tail string, err error) {
	line := keyNode.Line
	if line < 1 || line > len(lines) {
		return "", "", "", fmt.Errorf("its %s: sits on line %d, which is outside the file", key, line)
	}
	raw := lines[line-1]
	indent := keyNode.Column - 1
	if indent < 0 || indent > len(raw) || !strings.HasPrefix(raw[indent:], key+":") {
		return "", "", "", fmt.Errorf("its %s: line reads unexpectedly at line %d; edit the file by hand", key, line)
	}
	head, gap = raw[:indent+len(key)+1], " "
	if !isNullNode(valueNode) {
		if err := valueFitsOwnLine(raw, key, line, valueNode); err != nil {
			return "", "", "", err
		}
		if start := valueNode.Column - 1; start > len(head) && start <= len(raw) &&
			strings.TrimSpace(raw[len(head):start]) == "" {
			gap = raw[len(head):start]
		}
	}
	return head, gap, valueLineComment(raw, head, keyNode, valueNode), nil
}

// valueFitsOwnLine reports whether the value begins AND ends on its key's own
// line, which is what a one-line rewrite may assume. It asks the parser rather
// than the node tree, which reports where a scalar starts but not where it
// ends: the key's line is re-read on its own, and a value the whole file reads
// differently from that one line is a value with more of itself elsewhere — a
// block scalar, a folded plain scalar, a quoted value closing further down.
func valueFitsOwnLine(raw, key string, line int, value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("its %s: holds a list or a block, not a single value; edit the file by hand", key)
	}
	spanning := fmt.Errorf(
		"its %s: value does not fit on its own line (a block, or a value continued on the next line); "+
			"edit the file by hand", key)
	if value.Line != line || value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return spanning
	}
	var alone yaml.Node
	if err := yaml.Unmarshal([]byte(strings.TrimLeft(raw, " ")), &alone); err != nil {
		return spanning
	}
	if len(alone.Content) != 1 || alone.Content[0].Kind != yaml.MappingNode ||
		len(alone.Content[0].Content) != 2 {
		return spanning
	}
	aloneKey, aloneValue := alone.Content[0].Content[0], alone.Content[0].Content[1]
	if aloneKey.Value != key || aloneValue.Value != value.Value || aloneValue.Style != value.Style {
		return spanning
	}
	return nil
}

// valueLineComment is the end-of-line note the line carries, with the
// whitespace in front of it — the user's remark about this setting, which stays
// on the line a rewrite rebuilds. On a bare `key:` the parser hangs the note off
// the key instead of the value, so both are consulted.
func valueLineComment(raw, head string, keyNode, valueNode *yaml.Node) string {
	comment := valueNode.LineComment
	if comment == "" {
		comment = keyNode.LineComment
	}
	if comment == "" {
		return ""
	}
	at := strings.LastIndex(raw, comment)
	if at <= len(head) {
		return ""
	}
	for at > 0 && (raw[at-1] == ' ' || raw[at-1] == '\t') {
		at--
	}
	return raw[at:]
}

// isNullNode reports whether a node is the empty value a bare `key:` parses to
// — a value there is no text of, so it can be written over but not read from.
func isNullNode(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// renderYAMLScalar renders one value as the YAML text for it, quoted by the
// marshaller wherever a bare scalar would mean something else. A value needing
// more than one line is refused: these edits occupy exactly the line they
// splice.
func renderYAMLScalar(v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("rendering the value: %w", err)
	}
	text := strings.TrimRight(string(out), "\n")
	if strings.Contains(text, "\n") {
		return "", errors.New("the value does not fit on one line; edit the file by hand")
	}
	return text, nil
}

// splitConfigLines splits the file into lines without a trailing empty element,
// so a rejoin plus one closing newline reproduces the file exactly. A blank
// file has no lines at all.
func splitConfigLines(data []byte) []string {
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// joinConfigLines rejoins the lines, always ending the file with a newline.
func joinConfigLines(lines []string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

// verifiedEntrySplice is the gate a splice passes before it reaches the disk:
// the result must parse, must hold the servers section with the named entry
// changed to exactly want and every other entry untouched, must agree with the
// original on every setting outside that section, and must still satisfy the
// one-key-source rule the load enforces — swapping which source an entry
// declares is the whole point of the edit, so leaving that rule satisfied is
// how this writer knows the swap landed.
func verifiedEntrySplice(updated []byte, before Config, name string, want ServerConfig) ([]byte, error) {
	var after Config
	if err := yaml.Unmarshal(updated, &after); err != nil {
		return nil, fmt.Errorf("the edited file would not parse: %w", redactYAMLError(err))
	}
	switch {
	case !serversChangedOnlyAt(before.Servers, after.Servers, name, want):
		return nil, fmt.Errorf(
			"the edit did not put the key source on the servers.%s entry where a reader would look for it; "+
				"edit the file by hand", name)
	case !sameApartFromServers(before, after):
		return nil, errors.New("the edit would have changed more than the servers section; edit the file by hand")
	}
	if problems := after.apiKeySourceErrors(); len(problems) > 0 {
		return nil, fmt.Errorf("the edited file would not load: %s", problems[0])
	}
	return updated, nil
}

// serversChangedOnlyAt reports whether after is before with the entry named
// name replaced by want and nothing else moved — the shape a key-source splice
// must produce.
func serversChangedOnlyAt(before, after map[string]ServerConfig, name string, want ServerConfig) bool {
	expected := make(map[string]ServerConfig, len(before))
	for entryName, entry := range before {
		expected[entryName] = entry
	}
	expected[name] = want
	return reflect.DeepEqual(after, expected)
}

// sameApartFromServers reports whether two parsed configs agree on everything
// outside the servers section — the guarantee that a textual splice touched
// nothing else. The section is blanked in both copies and what is left is
// compared whole, so a key the line arithmetic clipped, reordered or re-typed
// shows up as a difference even though the writer never meant to touch it.
func sameApartFromServers(before, after Config) bool {
	before.Servers, after.Servers = nil, nil
	return reflect.DeepEqual(before, after)
}

// writeConfigAtomically replaces the config with data in one step: the bytes go
// to a temporary file in the config's own directory (the same filesystem, so
// the rename is atomic) and are renamed over it. An interrupted write therefore
// leaves the old config intact rather than a truncated one — the file may be
// the only place a key still lives.
func writeConfigAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("config: creating a temporary file beside %s: %w", path, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename below has moved it into place

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: closing %s: %w", name, err)
	}
	if err := os.Chmod(name, configFileMode); err != nil {
		return fmt.Errorf("config: setting the mode of %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}
