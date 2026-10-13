package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// The paths and headings prepare edits. The rollup heading's shape — `## X.Y.Z`, the version
// without its `v`, no brackets and no date — is the one CHANGELOG.md gives every release.
const (
	changelogFile      = "CHANGELOG.md"
	unreleasedHeading  = "## Unreleased"
	releaseHeadingLead = "## "
)

// prepareOptions is what the prepare verb was asked to do.
type prepareOptions struct {
	// dir is the checkout prepare edits and commits in.
	dir string
	// version is the release to cut, as typed: prepare parses it itself, so a malformed one is
	// a refused run (exit 1) rather than a usage error.
	version string
	// openRepo opens the checkout's repo; nil opens the execRepo over the run's runner.
	openRepo repoOpener
}

// newPrepareCommand builds the prepare verb.
func newPrepareCommand(commands runner, openRepo repoOpener) *cobra.Command {
	options := prepareOptions{dir: ".", openRepo: openRepo}

	cmd := &cobra.Command{
		Use:   "prepare vX.Y.Z",
		Short: "Make the CHANGELOG rollup and VERSION bump commits for a release",
		Long: "prepare makes exactly two local commits and pushes nothing:\n" +
			"\"docs(changelog): roll up X.Y.Z\", which moves everything under ## Unreleased in\n" +
			"CHANGELOG.md under a new ## X.Y.Z heading and leaves ## Unreleased empty above it, then\n" +
			"\"chore(version): bump version to X.Y.Z\", which writes the bare X.Y.Z to\n" +
			"VERSION. It refuses, writing nothing, when the version is malformed or not above the\n" +
			"highest vX.Y.Z tag (with no such tag, any version is above it), VERSION already reads\n" +
			"it, the working tree is not clean, ## Unreleased is missing or holds no bullet, or\n" +
			"CHANGELOG.md already has a heading for the version. It fetches origin's main and its\n" +
			"tags first, so the version is judged against the published tags, and refuses when that\n" +
			"fetch fails. A bare X.Y.Z reads as vX.Y.Z.",
		Args: cobra.ExactArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			options.version = args[0]
			return runPrepare(cmd.Context(), commands, cmd.OutOrStdout(), options)
		}),
	}
	return cmd
}

// runPrepare checks every precondition before it writes anything, then makes the rollup commit
// and the bump commit, in that order, each staging its one path by name.
func runPrepare(ctx context.Context, commands runner, output io.Writer, options prepareOptions) error {
	version, err := parseReleaseVersion(options.version)
	if err != nil {
		return err
	}

	checkout := options.openRepo.open(commands, options.dir)
	rollup, err := checkPrepare(ctx, checkout, options.dir, version)
	if err != nil {
		return err
	}

	bare := strings.TrimPrefix(version.String(), "v")
	rollupSubject := "docs(changelog): roll up " + bare
	if err := checkout.CommitFile(ctx, changelogFile, rollup, rollupSubject); err != nil {
		return err
	}
	bumpSubject := "chore(version): bump version to " + bare
	if err := checkout.CommitFile(ctx, versionFile, []byte(bare+"\n"), bumpSubject); err != nil {
		return fmt.Errorf("%w (the rollup commit %q was made)", err, rollupSubject)
	}

	_, _ = fmt.Fprintf(output, "committed %q\ncommitted %q\nnothing was pushed\n", rollupSubject, bumpSubject)
	return nil
}

// checkPrepare runs every refusal check and, when none refuses, returns CHANGELOG.md as the
// rollup commit will write it. Every refusal is reported together, so one run names them all.
// It fetches origin's main and its tags first, so the version is judged against the published
// tags rather than stale local ones, then asks checkout for the tree's state and the tags, and
// reads CHANGELOG.md and VERSION from dir.
func checkPrepare(ctx context.Context, checkout repo, dir string, version releaseVersion) ([]byte, error) {
	var refusals []error

	if err := checkout.FetchMain(ctx); err != nil {
		refusals = append(refusals, fmt.Errorf("fetch %s's %s and its tags: %w", remoteName, mainBranch, err))
	}

	isClean, err := checkout.IsClean(ctx)
	switch {
	case err != nil:
		refusals = append(refusals, fmt.Errorf("read the working tree's status: %w", err))
	case !isClean:
		refusals = append(refusals, errors.New("the working tree is not clean"))
	}

	tags, err := checkout.ReleaseTags(ctx)
	if err != nil {
		refusals = append(refusals, fmt.Errorf("list the release tags: %w", err))
	} else if highest := highestReleaseTag(strings.Join(tags, "\n")); highest != "" {
		if refusal := checkAboveTag(version, highest); refusal != nil {
			refusals = append(refusals, refusal)
		}
	}

	if refusal := checkVersionFile(dir, version); refusal != nil {
		refusals = append(refusals, refusal)
	}

	var rollup []byte
	changelog, err := os.ReadFile(filepath.Join(dir, changelogFile))
	if err != nil {
		refusals = append(refusals, fmt.Errorf("read %s: %w", changelogFile, err))
	} else if rollup, err = rollUpChangelog(changelog, version); err != nil {
		refusals = append(refusals, err)
	}

	if len(refusals) > 0 {
		return nil, fmt.Errorf("refusing to prepare %s, nothing was written:\n%w", version, errors.Join(refusals...))
	}
	return rollup, nil
}

// checkAboveTag refuses a version that is not strictly above highest, the highest release tag.
func checkAboveTag(version releaseVersion, highest string) error {
	highestVersion, err := parseReleaseVersion(highest)
	if err != nil {
		return fmt.Errorf("read the highest release tag: %w", err)
	}
	if version.compare(highestVersion) <= 0 {
		return fmt.Errorf("%s is not above the highest release tag %s", version, highest)
	}
	return nil
}

// checkVersionFile refuses a version VERSION already reads, with or without its `v`: an unpushed
// VERSION bump has claimed it, and pushing that bump tags it, so the release must be the next one
// instead.
func checkVersionFile(dir string, version releaseVersion) error {
	raw, err := os.ReadFile(filepath.Join(dir, versionFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", versionFile, err)
	}
	current, err := parseReleaseVersion(strings.TrimSpace(string(raw)))
	if err != nil || current.compare(version) != 0 {
		// A VERSION that does not parse cannot equal the version; the bump commit replaces it.
		return nil
	}
	next := releaseVersion{major: version.major, minor: version.minor, patch: version.patch + 1}
	return fmt.Errorf("%s already reads %s, an unpushed version bump: cut the next patch, %s, instead",
		versionFile, strings.TrimPrefix(version.String(), "v"), next)
}

// rollUpChangelog returns changelog with a `## X.Y.Z` heading inserted directly under
// `## Unreleased`, one blank line on each side, so every bullet and `### Section` that sat under
// Unreleased now sits under the release. It refuses a changelog with no Unreleased heading, an
// Unreleased section with no bullet, or a heading for the version already, each refusal naming
// the file and the line. The version heading is matched as a whole trimmed line, so `## 1.8.10`
// is never taken for `## 1.8.1`. The file's own line ending is kept, so a CRLF checkout gains a
// CRLF heading.
func rollUpChangelog(changelog []byte, version releaseVersion) ([]byte, error) {
	text := string(changelog)
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")

	bare := strings.TrimPrefix(version.String(), "v")
	versionHeading := releaseHeadingLead + bare
	unreleased := -1
	for index, line := range lines {
		content := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(content) == versionHeading {
			return nil, fmt.Errorf("%s:%d: already has a %s heading", changelogFile, index+1, versionHeading)
		}
		if unreleased < 0 && content == unreleasedHeading {
			unreleased = index
		}
	}
	if unreleased < 0 {
		return nil, fmt.Errorf("%s has no %s heading", changelogFile, unreleasedHeading)
	}

	// The Unreleased section runs to the next release heading (`## `, never a `### ` section
	// heading); its body starts at its first non-blank line.
	body := unreleased + 1
	for body < len(lines) && strings.TrimSpace(lines[body]) == "" {
		body++
	}
	hasBullet := false
	for _, line := range lines[body:] {
		if strings.HasPrefix(line, releaseHeadingLead) {
			break
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			hasBullet = true
			break
		}
	}
	if !hasBullet {
		return nil, fmt.Errorf("%s:%d: no bullet under %s to release", changelogFile, unreleased+1, unreleasedHeading)
	}

	var rollup strings.Builder
	for _, line := range lines[:unreleased] {
		rollup.WriteString(line)
	}
	rollup.WriteString(unreleasedHeading + newline + newline + versionHeading + newline + newline)
	for _, line := range lines[body:] {
		rollup.WriteString(line)
	}
	return []byte(rollup.String()), nil
}
