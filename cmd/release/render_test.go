package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// goldenVersion is the release the golden package files under testdata are rendered for.
const goldenVersion = "0.0.0"

// The fixture hashes the formula tests read, one per macOS and Linux archive.
const (
	formulaDarwinArm64Hash = "1111111111111111111111111111111111111111111111111111111111111111"
	formulaDarwinAmd64Hash = "2222222222222222222222222222222222222222222222222222222222222222"
	formulaLinuxArm64Hash  = "3333333333333333333333333333333333333333333333333333333333333333"
	formulaLinuxAmd64Hash  = "4444444444444444444444444444444444444444444444444444444444444444"
)

// The fixture hashes the SHA256SUMS line tests read, one per Windows archive plus one for an
// archive the formula does not install.
const (
	windowsAmd64Hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	windowsArm64Hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	otherArchiveHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// goldenSums is the SHA256SUMS body the golden files are rendered from: the four formula
// archives and the two Windows archives of goldenVersion.
func goldenSums() string {
	return formulaLinuxAmd64Hash + "  llama-launcher_0.0.0_linux_amd64.tar.gz\n" +
		formulaLinuxArm64Hash + "  llama-launcher_0.0.0_linux_arm64.tar.gz\n" +
		formulaDarwinAmd64Hash + "  llama-launcher_0.0.0_darwin_amd64.tar.gz\n" +
		formulaDarwinArm64Hash + "  llama-launcher_0.0.0_darwin_arm64.tar.gz\n" +
		windowsAmd64Hash + "  llama-launcher_0.0.0_windows_amd64.zip\n" +
		windowsArm64Hash + "  llama-launcher_0.0.0_windows_arm64.zip\n"
}

// TestRenderFormulaMatchesTheGoldenFormula renders goldenVersion, tagged and bare, and expects
// testdata/llama-launcher-0.0.0.rb byte for byte.
func TestRenderFormulaMatchesTheGoldenFormula(t *testing.T) {
	t.Parallel()
	want := readTestFile(t, filepath.Join("testdata", "llama-launcher-"+goldenVersion+".rb"))

	for _, version := range []string{"v" + goldenVersion, goldenVersion} {
		got, err := renderFormula(version, goldenSums())
		if err != nil {
			t.Fatalf("renderFormula(%q) = %v", version, err)
		}
		if got != want {
			t.Errorf("renderFormula(%q) differs from testdata/llama-launcher-%s.rb:\n%s", version, goldenVersion, got)
		}
	}
}

// TestChecksumForReadsBothLineForms reads a hash from the text and the binary SHA256SUMS line
// forms, and lower-cases an upper-case one.
func TestChecksumForReadsBothLineForms(t *testing.T) {
	t.Parallel()

	const name = "llama-launcher_1.2.3_windows_amd64.zip"
	tests := map[string]string{
		"text form":          windowsAmd64Hash + "  " + name + "\n",
		"binary form":        windowsAmd64Hash + " *" + name + "\n",
		"upper case":         strings.ToUpper(windowsAmd64Hash) + "  " + name + "\n",
		"crlf line endings":  otherArchiveHash + "  other.zip\r\n" + windowsAmd64Hash + "  " + name + "\r\n",
		"first match wins":   windowsAmd64Hash + "  " + name + "\n" + windowsArm64Hash + "  " + name + "\n",
		"suffix is no match": windowsArm64Hash + "  x" + name + "\n" + windowsAmd64Hash + "  " + name + "\n",
	}
	for label, sums := range tests {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			got, err := checksumFor(sums, name)
			if err != nil {
				t.Fatalf("checksumFor = %v", err)
			}
			if got != windowsAmd64Hash {
				t.Errorf("checksumFor = %q; want %q", got, windowsAmd64Hash)
			}
		})
	}
}

// TestRenderRefusesABadOrMissingHash refuses to render any package file when SHA256SUMS omits an
// archive the file installs or lists a hash that is not 64 hex digits.
func TestRenderRefusesABadOrMissingHash(t *testing.T) {
	t.Parallel()

	const (
		bare        = "1.2.3"
		darwinArm64 = "llama-launcher_1.2.3_darwin_arm64.tar.gz"
	)
	// otherFormulaSums lists the three formula archives every formula case leaves intact.
	otherFormulaSums := formulaDarwinAmd64Hash + "  llama-launcher_1.2.3_darwin_amd64.tar.gz\n" +
		formulaLinuxArm64Hash + "  llama-launcher_1.2.3_linux_arm64.tar.gz\n" +
		formulaLinuxAmd64Hash + "  llama-launcher_1.2.3_linux_amd64.tar.gz\n"
	render := map[string]func(string, string) (string, error){
		"formula": renderFormula,
	}

	tests := map[string]struct {
		file    string
		sums    string
		wantErr error
		archive string
	}{
		"formula: archive missing": {file: "formula", sums: otherFormulaSums, wantErr: errNoChecksum, archive: darwinArm64},
		"formula: short hash":      {file: "formula", sums: formulaDarwinArm64Hash[1:] + "  " + darwinArm64 + "\n" + otherFormulaSums, wantErr: errMalformedChecksum, archive: darwinArm64},
		"formula: not hex":         {file: "formula", sums: "z" + formulaDarwinArm64Hash[1:] + "  " + darwinArm64 + "\n" + otherFormulaSums, wantErr: errMalformedChecksum, archive: darwinArm64},
	}
	for label, tc := range tests {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			got, err := render[tc.file]("v"+bare, tc.sums)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("render = %q, %v; want %v", got, err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.archive) {
				t.Errorf("error %q does not name %s", err, tc.archive)
			}
		})
	}
}

// TestRenderRefusesABadVersion refuses a version that is not vX.Y.Z or a bare X.Y.Z before it
// reads a single hash.
func TestRenderRefusesABadVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"", "v1.2", "1.2.3-rc1", "v01.2.3", "latest"} {
		if _, err := renderFormula(version, ""); err == nil || !strings.Contains(err.Error(), "not a release version") {
			t.Errorf("renderFormula(%q) = %v; want a not-a-release-version error", version, err)
		}
	}
}

// TestPackageDataRefusesAnUnknownTarget refuses an archive for a platform no release ships, so a
// template typo fails the render rather than pinning a URL that does not exist.
func TestPackageDataRefusesAnUnknownTarget(t *testing.T) {
	t.Parallel()

	data := packageData{Version: "1.2.3", tag: "v1.2.3", sums: windowsAmd64Hash + "  llama-launcher_1.2.3_freebsd_amd64.tar.gz\n"}
	if _, err := data.Archive("freebsd", "amd64"); err == nil || !strings.Contains(err.Error(), "not a release target") {
		t.Errorf("Archive(freebsd, amd64) = %v; want a not-a-release-target error", err)
	}
}
