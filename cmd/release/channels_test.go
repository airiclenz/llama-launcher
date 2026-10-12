package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeChannelRepo is the tap repository as a clone of it holds it.
type fakeChannelRepo struct {
	// file is the generated file the repository holds, "" for none.
	file string
	// isEmpty is a repository with no commits yet.
	isEmpty bool
}

// fakeChannels is what the channel step meets beyond the probes: the tap it clones.
type fakeChannels struct {
	tap fakeChannelRepo
	// clones maps each clone directory to the repository cloned into it.
	clones map[string]string
}

// respondChannel answers the channel step's commands: the clone of the tap and the git commands
// run inside it. It reports false for any other command.
func (f *publishFake) respondChannel(call recordedCall) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	args := call.args
	if call.name == "gh" && len(args) == 6 && args[0] == "repo" && args[1] == "clone" {
		repo, cloneDir := args[2], args[3]
		if f.channels.clones == nil {
			f.channels.clones = map[string]string{}
		}
		f.channels.clones[cloneDir] = repo
		return "", true, f.writeClone(cloneDir)
	}

	if _, isClone := f.channels.clones[call.dir]; !isClone || call.name != "git" {
		return "", false, nil
	}
	switch args[0] {
	case "rev-list":
		if f.channels.tap.isEmpty {
			return "", true, nil
		}
		return bumpCommit + "\n", true, nil
	case "diff":
		written, err := os.ReadFile(filepath.Join(call.dir, filepath.FromSlash(formulaPath)))
		if err != nil || string(written) == f.channels.tap.file {
			return "", true, err
		}
		return formulaPath + "\n", true, nil
	case "add", "commit", "push":
		return "", true, nil
	}
	return "", false, nil
}

// writeClone makes the tap's clone directory, holding its formula when it has one.
func (f *publishFake) writeClone(cloneDir string) error {
	if err := os.MkdirAll(cloneDir, 0o755); err != nil {
		return err
	}
	content := f.channels.tap.file
	if content == "" {
		return nil
	}
	filePath := filepath.Join(cloneDir, filepath.FromSlash(formulaPath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(content), 0o644)
}

// testReleaseSums is a SHA256SUMS body listing every archive of v0.24.11, one fixture hash each.
func testReleaseSums() string {
	var sums strings.Builder
	for index, name := range releaseAssetsV02411[:len(distTargets)] {
		sums.WriteString(strings.Repeat(fmt.Sprintf("%x", index+10), 64) + "  " + name + "\n")
	}
	return sums.String()
}

// releasedFake is a resumed run of publishVersion: the tag and the whole release are on GitHub
// and the tap's formula pins the previous release — so the channel step is pending.
func releasedFake() *publishFake {
	fake := newPublishFake()
	fake.tagged()
	fake.repo.releases[publishVersion] = releaseAssetsV02411
	fake.remote = fakeRemote{
		hasTap:  true,
		formula: strings.Replace(testFormula, "0.24.11", "0.24.10", 1),
	}
	fake.repo.assetContent[checksumsFile] = testReleaseSums()
	fake.channels = fakeChannels{
		tap: fakeChannelRepo{file: "class LlamaLauncher < Formula\n  version \"0.24.10\"\nend\n"},
	}
	return fake
}

// runChannelPublish runs publish of publishVersion over fake with a written notes file.
func runChannelPublish(t *testing.T, fake *publishFake) publishRunResult {
	t.Helper()
	notesPath := filepath.Join(t.TempDir(), "notes.md")
	writeTestFile(t, notesPath, "## What's new\n\n- A release.\n")
	return runTestPublishWith(t, fake, publishOptions{notesFile: notesPath})
}

// callsInClone returns the git commands run inside the clone of repo, one line each.
func callsInClone(fake *publishFake, commands *fakeRunner, repo string) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	commands.mu.Lock()
	defer commands.mu.Unlock()
	var lines []string
	for _, call := range commands.calls {
		if call.name == "git" && fake.channels.clones[call.dir] == repo {
			lines = append(lines, "git "+strings.Join(call.args, " "))
		}
	}
	return lines
}

// TestPublishCommitsAChannelFileOnlyWhenItChanges pins the tap: it takes the rendered formula,
// committed as `llama-launcher X.Y.Z` and pushed to its branch, only when that changes the file;
// a clone already holding it commits and pushes nothing.
func TestPublishCommitsAChannelFileOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	formula, err := renderFormula(publishVersion, testReleaseSums())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		tap        string
		wantTapGit []string
	}{
		"tap behind": {
			wantTapGit: []string{"git commit --quiet -m llama-launcher 0.24.11", "git push --quiet origin HEAD"},
		},
		"tap already current": {
			tap: formula,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := releasedFake()
			if tc.tap != "" {
				fake.channels.tap.file = tc.tap
			}

			result := runChannelPublish(t, fake)

			if result.err != nil {
				t.Fatalf("publish failed: %v\n%s", result.err, result.output)
			}
			var writes []string
			for _, line := range callsInClone(fake, result.commands, tapRepo) {
				if strings.HasPrefix(line, "git commit") || strings.HasPrefix(line, "git push") {
					writes = append(writes, line)
				}
			}
			if !slices.Equal(writes, tc.wantTapGit) {
				t.Errorf("%s: committed and pushed %q, want %q", tapRepo, writes, tc.wantTapGit)
			}
		})
	}
}

// TestPublishWritesTheRenderedFileIntoTheClone pins what the tap receives: exactly the formula
// rendered from the release's SHA256SUMS, staged at its path, before the commit.
func TestPublishWritesTheRenderedFileIntoTheClone(t *testing.T) {
	t.Parallel()
	fake := releasedFake()
	var written string
	respond := fake.respond
	commands := &fakeRunner{respond: func(call recordedCall) (string, error) {
		if call.name == "git" && len(call.args) > 0 && call.args[0] == "commit" {
			content, err := os.ReadFile(filepath.Join(call.dir, filepath.FromSlash(formulaPath)))
			if err != nil {
				return "", err
			}
			written = string(content)
		}
		return respond(call)
	}}
	run := &publishRun{commands: commands, repo: fake.repo, output: &strings.Builder{}, version: mustVersion(t, publishVersion),
		options: publishOptions{dir: t.TempDir()}}

	if err := convergeHomebrew(t.Context(), run); err != nil {
		t.Fatal(err)
	}

	formula, err := renderFormula(publishVersion, testReleaseSums())
	if err != nil {
		t.Fatal(err)
	}
	if written != formula {
		t.Errorf("the tap's %s is not the rendered formula:\n%s", formulaPath, written)
	}
	if index := lineIndex(callsInClone(fake, commands, tapRepo), "git add -- "+formulaPath); index < 0 {
		t.Errorf("%s: %s was not staged", tapRepo, formulaPath)
	}
}

// TestPublishPushesAnEmptyTapToMain pins the first formula: a tap repository with no commits has
// no branch for HEAD to push to, so its first commit goes to main.
func TestPublishPushesAnEmptyTapToMain(t *testing.T) {
	t.Parallel()
	fake := releasedFake()
	fake.remote.formula = ""
	fake.channels.tap = fakeChannelRepo{isEmpty: true}

	result := runChannelPublish(t, fake)

	if result.err != nil {
		t.Fatalf("publish failed: %v\n%s", result.err, result.output)
	}
	clone := callsInClone(fake, result.commands, tapRepo)
	if index := lineIndex(clone, "git push"); index < 0 || clone[index] != "git push --quiet origin HEAD:main" {
		t.Errorf("tap clone commands = %q, want the push git push --quiet origin HEAD:main", clone)
	}
}

// TestPublishSkipsAChannelWhoseSetupIsPending pins the tap's one-time setup: a missing tap
// repository is skipped and named, touched never, and the run succeeds.
func TestPublishSkipsAChannelWhoseSetupIsPending(t *testing.T) {
	t.Parallel()
	fake := releasedFake()
	fake.remote.hasTap = false

	result := runChannelPublish(t, fake)

	if result.err != nil {
		t.Fatalf("publish failed: %v\n%s", result.err, result.output)
	}
	if want := "homebrew: skipped, its one-time setup is pending"; !strings.Contains(result.output, want) {
		t.Errorf("output lacks %q:\n%s", want, result.output)
	}
	lines := commandLines(result.commands)
	if index := lineIndex(lines, "gh repo clone "+tapRepo); index >= 0 {
		t.Errorf("ran %q for a channel whose setup is pending", lines[index])
	}
}

// TestPublishChannelConvergesFromRemoteStateOnceTheTagExists pins item 6's resume for the
// channel step: days later, with VERSION moved on and a dirty tree — either of which refuses a
// run that consults them — publish still updates the pending tap without consulting the gate or
// VERSION at HEAD, and pushes nothing to llama-launcher's own origin.
func TestPublishChannelConvergesFromRemoteStateOnceTheTagExists(t *testing.T) {
	t.Parallel()
	fake := releasedFake()
	fake.repo.isDirty = true
	fake.repo.files[versionFile] = "v0.24.12\n"

	result := runChannelPublish(t, fake)

	if result.err != nil {
		t.Fatalf("publish failed: %v\n%s", result.err, result.output)
	}
	lines := commandLines(result.commands)
	if index := lineIndex(lines, repoCallName+" "+methodPushMain); index >= 0 {
		t.Errorf("ran %q, want only the tap update once the tag exists", lines[index])
	}
	if index := lineIndex(lines, "gh repo clone "+tapRepo); index < 0 {
		t.Errorf("the tap was not updated; commands: %q", lines)
	}
}
