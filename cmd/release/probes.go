package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The repositories and paths the publish steps write to, and so the ones their probes read.
const (
	launcherRepo = "airiclenz/llama-launcher"
	tapRepo      = "airiclenz/homebrew-tap"
	formulaPath  = "Formula/llama-launcher.rb"
)

// The publish steps, by the name status and publish report them under.
const (
	stepTag      = "tag"
	stepRelease  = "release"
	stepHomebrew = "homebrew"
	stepSmoke    = "smoke"
)

// stepState is where a publish step stands for one version.
type stepState string

// The states a probe reports. Only stateDone lets publish skip a step; stateUnknown means the
// probe could not tell, and is never read as done.
const (
	// stateDone: the remote already holds the step's result for the version.
	stateDone stepState = "done"
	// statePending: publish has the step to do.
	statePending stepState = "pending"
	// stateSetupPending: the channel's one-time setup — its tap repository — has not landed;
	// publish skips the step.
	stateSetupPending stepState = "setup-pending"
	// stateUnknown: the probe could not run, or found the remote in a state publish cannot
	// converge from; the detail names which.
	stateUnknown stepState = "unknown"
)

// stepReport is one publish step's probed state. Its JSON keys are a contract the release skills
// read, like the rest of status's report.
type stepReport struct {
	Name   string    `json:"name"`
	State  stepState `json:"state"`
	Detail string    `json:"detail"`
}

// stepProbe asks the remote, read-only, where one publish step stands for version.
type stepProbe func(ctx context.Context, env probeEnv, version releaseVersion) stepReport

// probeEnv is what the probes read the remotes through: llama-launcher's own checkout and GitHub
// repository through repo, and every other repository — the tap — through commands, run in dir.
type probeEnv struct {
	repo     repo
	commands runner
	dir      string
}

// publishStep is one step publish runs, in order, with the probe that tells whether it is done.
type publishStep struct {
	name  string
	probe stepProbe
}

// publishSteps are the steps publish runs, in the order it runs them. status and publish probe
// through this one list, so what status reports done is exactly what publish skips.
var publishSteps = []publishStep{
	{name: stepTag, probe: probeTag},
	{name: stepRelease, probe: probeRelease},
	{name: stepHomebrew, probe: probeHomebrew},
	{name: stepSmoke, probe: probeSmoke},
}

// formulaVersionPattern finds the version a Homebrew formula pins: its `version "X.Y.Z"` line.
var formulaVersionPattern = regexp.MustCompile(`(?m)^\s*version\s+"([^"]*)"`)

// probeSteps probes every publish step for version, in publish order.
func probeSteps(ctx context.Context, env probeEnv, version releaseVersion) []stepReport {
	steps := make([]stepReport, 0, len(publishSteps))
	for _, step := range publishSteps {
		steps = append(steps, step.probe(ctx, env, version))
	}
	return steps
}

// probeTag reports the tag done only when origin holds an annotated tag for the version: a
// lightweight tag carries no message and is not what a release is cut from, so it is unknown,
// never done.
func probeTag(ctx context.Context, env probeEnv, version releaseVersion) stepReport {
	tag := version.String()
	hasTag, err := env.repo.RemoteHasTag(ctx, tag)
	if err != nil {
		return unknownStep(stepTag, "could not list %s's tags: %v", remoteName, err)
	}
	if !hasTag {
		return stepReport{Name: stepTag, State: statePending, Detail: fmt.Sprintf("%s has no tag %s", remoteName, tag)}
	}

	kind, err := env.repo.RemoteTagType(ctx, tag)
	if err != nil {
		return unknownStep(stepTag, "could not read the type of tag %s: %v", tag, err)
	}
	switch kind {
	case "tag":
		return stepReport{Name: stepTag, State: stateDone, Detail: fmt.Sprintf("annotated tag %s on %s", tag, remoteName)}
	case "commit":
		return unknownStep(stepTag, "%s on %s is a lightweight tag; a release tag must be annotated", tag, remoteName)
	default:
		return unknownStep(stepTag, "tag %s points at a %q object, not an annotated tag", tag, kind)
	}
}

// probeRelease reports the GitHub release done only when it carries every asset a release ships:
// the four archives and SHA256SUMS, each fully uploaded.
func probeRelease(ctx context.Context, env probeEnv, version releaseVersion) stepReport {
	tag := version.String()
	uploaded, isFound, err := env.repo.ReleaseAssets(ctx, tag)
	if err != nil {
		return unknownStep(stepRelease, "could not read GitHub release %s: %v", tag, err)
	}
	if !isFound {
		return stepReport{Name: stepRelease, State: statePending, Detail: fmt.Sprintf("no GitHub release %s", tag)}
	}

	want := releaseAssetNames(version)
	missing := missingAssets(want, uploaded)
	if len(missing) > 0 {
		return stepReport{Name: stepRelease, State: statePending, Detail: fmt.Sprintf(
			"release %s lacks %d of its %d assets: %s", tag, len(missing), len(want), strings.Join(missing, ", "))}
	}
	return stepReport{Name: stepRelease, State: stateDone, Detail: fmt.Sprintf("release %s with all %d assets", tag, len(want))}
}

// missingAssets returns the names in want that uploaded lacks, in want's order.
func missingAssets(want, uploaded []string) []string {
	var missing []string
	for _, name := range want {
		if !slices.Contains(uploaded, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// releaseAssetNames are the files a release of version carries: one archive per release
// platform, then SHA256SUMS.
func releaseAssetNames(version releaseVersion) []string {
	bare := bareRelease(version)
	names := make([]string, 0, len(distTargets)+1)
	for _, target := range distTargets {
		names = append(names, target.archiveName(bare))
	}
	return append(names, checksumsFile)
}

// probeHomebrew reports the tap setup-pending while its repository does not exist, and done when
// its formula pins the version. A tap without the formula is pending: the repository is the
// one-time setup, the formula is what publish writes.
func probeHomebrew(ctx context.Context, env probeEnv, version releaseVersion) stepReport {
	if _, err := env.commands.run(ctx, env.dir, nil, "gh", "api", "repos/"+tapRepo, "--jq", ".full_name"); err != nil {
		if isNotFound(err) {
			return stepReport{Name: stepHomebrew, State: stateSetupPending, Detail: fmt.Sprintf("no %s repository yet", tapRepo)}
		}
		return unknownStep(stepHomebrew, "could not read the %s repository: %v", tapRepo, err)
	}

	formula, err := readRepoFile(ctx, env.commands, env.dir, tapRepo, formulaPath)
	if err != nil {
		if isNotFound(err) {
			return stepReport{Name: stepHomebrew, State: statePending, Detail: fmt.Sprintf("%s has no %s", tapRepo, formulaPath)}
		}
		return unknownStep(stepHomebrew, "could not read %s from %s: %v", formulaPath, tapRepo, err)
	}

	fields := formulaVersionPattern.FindStringSubmatch(formula)
	if fields == nil {
		return stepReport{Name: stepHomebrew, State: statePending, Detail: fmt.Sprintf("%s pins no version", formulaPath)}
	}
	return pinnedVersionStep(stepHomebrew, "formula", fields[1], version)
}

// probeSmoke reports the smoke test pending always: it is an action publish takes last, not a
// state any remote holds.
func probeSmoke(context.Context, probeEnv, releaseVersion) stepReport {
	return stepReport{Name: stepSmoke, State: statePending, Detail: "an action, not a remote state: publish runs it last"}
}

// pinnedVersionStep reports a channel file done when the version it pins is the release's bare
// version, pending otherwise.
func pinnedVersionStep(name, file, pinned string, version releaseVersion) stepReport {
	bare := bareRelease(version)
	if pinned == bare {
		return stepReport{Name: name, State: stateDone, Detail: fmt.Sprintf("%s pins %s", file, bare)}
	}
	return stepReport{Name: name, State: statePending, Detail: fmt.Sprintf("%s pins %q, not %s", file, pinned, bare)}
}

// readRepoFile reads one file of a GitHub repository through the contents API.
func readRepoFile(ctx context.Context, commands runner, dir, repo, path string) (string, error) {
	encoded, err := commands.run(ctx, dir, nil, "gh", "api", "repos/"+repo+"/contents/"+path, "--jq", ".content")
	if err != nil {
		return "", err
	}
	// The contents API wraps its base64 at 60 columns.
	decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	return string(decoded), nil
}

// isReleaseNotFound reports whether a gh release failure is GitHub having no such release — or,
// for `gh release view` with no tag, no published release at all — as opposed to a call that
// could not be made.
func isReleaseNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "release not found")
}

// isNotFound reports whether a gh api failure is GitHub answering 404 — the path or repository
// does not exist, or the repository is empty — as opposed to a call that could not be made.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "(HTTP 404)")
}

// unknownStep is a step whose probe could not tell where it stands.
func unknownStep(name, format string, args ...any) stepReport {
	return stepReport{Name: name, State: stateUnknown, Detail: fmt.Sprintf(format, args...)}
}

// bareRelease is the version without its `v`, as archive names and the channel files carry it.
func bareRelease(version releaseVersion) string {
	return strings.TrimPrefix(version.String(), "v")
}
