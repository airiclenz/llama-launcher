package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// describeHomebrew says what the homebrew step does.
func describeHomebrew(run *publishRun) string {
	return describePackageFile(run, tapRepo, formulaPath)
}

// convergeHomebrew renders the formula from the release's SHA256SUMS and commits it to the tap.
func convergeHomebrew(ctx context.Context, run *publishRun) error {
	sums, err := downloadReleaseSums(ctx, run)
	if err != nil {
		return err
	}
	formula, err := renderFormula(run.version.String(), sums)
	if err != nil {
		return err
	}
	return publishPackageFile(ctx, run, tapRepo, formulaPath, formula)
}

// describePackageFile says what a step that publishes one generated file to a channel repository
// does.
func describePackageFile(run *publishRun, repo, path string) string {
	return fmt.Sprintf("render %s from release %s's %s, then commit %q to %s and push it when the file changes",
		path, run.version, checksumsFile, packageCommitSubject(run.version), repo)
}

// publishPackageFile clones repo into a temporary directory, writes content to path there and,
// only when that changes the file, commits it as `llama-launcher X.Y.Z` and pushes the commit straight to
// the repository's branch — no pull request. A repository with no commits yet has
// no branch to push to, so its first commit is pushed to main.
func publishPackageFile(ctx context.Context, run *publishRun, repo, path, content string) error {
	workDir, err := os.MkdirTemp("", "llama-launcher-channel-")
	if err != nil {
		return fmt.Errorf("create the %s clone directory: %w", repo, err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	cloneDir := filepath.Join(workDir, "repo")
	if _, err := run.commands.run(ctx, workDir, nonInteractiveEnv, "gh", "repo", "clone", repo, cloneDir, "--", "--quiet"); err != nil {
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	history, err := run.commands.run(ctx, cloneDir, nil, "git", "rev-list", "--all", "--max-count=1")
	if err != nil {
		return fmt.Errorf("read %s's history: %w", repo, err)
	}

	filePath := filepath.Join(cloneDir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(filePath), directoryMode); err != nil {
		return fmt.Errorf("create %s's directory in the %s clone: %w", path, repo, err)
	}
	if err := os.WriteFile(filePath, []byte(content), documentMode); err != nil {
		return fmt.Errorf("write %s in the %s clone: %w", path, repo, err)
	}
	if _, err := run.commands.run(ctx, cloneDir, nil, "git", "add", "--", path); err != nil {
		return fmt.Errorf("stage %s in the %s clone: %w", path, repo, err)
	}
	staged, err := run.commands.run(ctx, cloneDir, nil, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return fmt.Errorf("compare %s with %s's: %w", path, repo, err)
	}
	if strings.TrimSpace(staged) == "" {
		_, _ = fmt.Fprintf(run.output, "  %s already holds this %s: nothing to push\n", repo, path)
		return nil
	}

	if _, err := run.commands.run(ctx, cloneDir, nil, "git", "commit", "--quiet", "-m", packageCommitSubject(run.version)); err != nil {
		return fmt.Errorf("commit %s to %s: %w", path, repo, err)
	}
	pushRef := "HEAD"
	if strings.TrimSpace(history) == "" {
		pushRef = "HEAD:" + mainBranch
	}
	if _, err := run.commands.run(ctx, cloneDir, nonInteractiveEnv, "git", "push", "--quiet", remoteName, pushRef); err != nil {
		return fmt.Errorf("push %s to %s: %w", path, repo, err)
	}
	return nil
}

// packageCommitSubject is the subject of the commit the tap takes for a release.
func packageCommitSubject(version releaseVersion) string {
	return binaryName + " " + bareRelease(version)
}

// downloadReleaseSums reads the release's SHA256SUMS from GitHub, writing nothing into the
// checkout, whose clean tree the next status or publish gate checks.
func downloadReleaseSums(ctx context.Context, run *publishRun) (string, error) {
	tag := run.version.String()
	sums, err := run.repo.ReadAsset(ctx, tag, checksumsFile)
	if err != nil {
		return "", fmt.Errorf("download %s from GitHub release %s: %w", checksumsFile, tag, err)
	}
	return sums, nil
}
