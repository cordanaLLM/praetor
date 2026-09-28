package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// sbomNoticesFixture is a notices file listing what sbomNoticesRepo ships at left-pad 1.3.0.
const sbomNoticesFixture = "# Third-party notices\n\n" +
	"## Go standard library and runtime\n\n| Component | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| Go standard library and runtime | 1.27 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |\n\n" +
	"## Go modules\n\n| Module | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `example.com/lib` | v1.2.3 | MIT | `Copyright (c) Someone` |\n\n" +
	"## Container base image\n\n| Image | Tag | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `gcr.io/distroless/static-debian13` | nonroot | Apache-2.0 | none stated |\n\n" +
	"## npm packages of the Markdown gate\n\n| Package | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `left-pad` | 1.3.0 | ISC | `Copyright (c) Pad` |\n"

// sbomNoticesRepo writes a checkout whose npm lock pins left-pad at version under license,
// beside sbomNoticesFixture, and returns its root.
func sbomNoticesRepo(t *testing.T, version, license string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.27\n\nrequire example.com/lib v1.2.3\n")
	writeFixtureFile(t, dir, "Dockerfile", "FROM gcr.io/distroless/static-debian13:nonroot\n")
	writeFixtureFile(t, dir, "tools/markdownlint/package-lock.json",
		`{"lockfileVersion":3,"packages":{"node_modules/left-pad":{"version":"`+version+`","license":"`+license+`"}}}`)
	writeFixtureFile(t, dir, supplychain.NoticesFile, sbomNoticesFixture)
	return dir
}

// Positive: a lock bump is regenerated into the notices, a second run finds them current, and
// --check then passes.
func TestRunSBOMNotices_Positive_RegeneratesABump(t *testing.T) {
	dir := sbomNoticesRepo(t, "1.3.1", "ISC")
	stdout, err := captureStdout(t, func() error { return runSBOM([]string{"notices", "--path", dir}) })
	if err != nil || !strings.Contains(stdout, "regenerated") {
		t.Fatalf("sbom notices = %q, %v", stdout, err)
	}
	want := strings.Replace(sbomNoticesFixture, "| 1.3.0 |", "| 1.3.1 |", 1)
	if got := readFixtureFile(t, dir, supplychain.NoticesFile); got != want {
		t.Errorf("regenerated notices:\n%s\nwant:\n%s", got, want)
	}
	if stdout, err := captureStdout(t, func() error { return runSBOM([]string{"notices", "--path=" + dir}) }); err != nil || !strings.Contains(stdout, "already current") {
		t.Errorf("a second run = %q, %v", stdout, err)
	}
	if stdout, err := captureStdout(t, func() error { return runSBOM([]string{"notices", "--check", "--path", dir}) }); err != nil || !strings.Contains(stdout, "[PASS]") {
		t.Errorf("--check after regeneration = %q, %v", stdout, err)
	}
}

// Negative: --check fails on a bump and names the command, and an unreviewed license stops
// the write; neither touches the file.
func TestRunSBOMNotices_Negative_CheckAndUnknownLicenseLeaveTheFile(t *testing.T) {
	for _, tc := range []struct {
		name, license, want string
		args                []string
	}{
		{"check on a bump", "ISC", "praetorctl sbom notices", []string{"--check"}},
		{"unknown license", "SSPL-1.0", "SSPL-1.0", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := sbomNoticesRepo(t, "1.3.1", tc.license)
			args := append([]string{"notices", "--path", dir}, tc.args...)
			if _, err := captureStdout(t, func() error { return runSBOM(args) }); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("sbom notices = %v, want an error naming %q", err, tc.want)
			}
			if got := readFixtureFile(t, dir, supplychain.NoticesFile); got != sbomNoticesFixture {
				t.Errorf("a failed run changed the notices:\n%s", got)
			}
		})
	}
}

// Boundary: a positional argument is refused, and a checkout without the notices file or one
// of its sources is named rather than written.
func TestRunSBOMNotices_Boundary_RefusesWhatItCannotRender(t *testing.T) {
	dir := sbomNoticesRepo(t, "1.3.0", "ISC")
	if err := runSBOM([]string{"notices", "extra", "--path", dir}); err == nil || !strings.Contains(err.Error(), "no positional arguments") {
		t.Errorf("a positional argument was accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	if err := runSBOM([]string{"notices", "--path", dir}); err == nil || !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("a missing Dockerfile was not named: %v", err)
	}
	empty := t.TempDir()
	if err := runSBOM([]string{"notices", "--path", empty}); err == nil || !strings.Contains(err.Error(), supplychain.NoticesFile) {
		t.Errorf("a missing notices file was not named: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, supplychain.NoticesFile)); !os.IsNotExist(err) {
		t.Errorf("a run without a notices file created one: %v", err)
	}
}
