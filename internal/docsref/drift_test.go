// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// driftSurfaces is an adopter's declaration: a build script documented in its onboarding guide
// and a release workflow declared without documentation.
var driftSurfaces = []config.DocsSurface{
	{Name: "build script", Paths: []string{"scripts/*.sh"}, Exclude: []string{"scripts/test_*.sh"}, Docs: []string{"docs/onboarding.md"}},
	{Name: "release workflow", Paths: []string{".github/workflows/release.yml"}},
}

// mustDrift runs Drift and fails the test on an error.
func mustDrift(t *testing.T, changed []string, waivers ...Waiver) *DriftReport {
	t.Helper()
	report, err := Drift(changed, driftSurfaces, waivers)
	if err != nil {
		t.Fatalf("Drift(%v): %v", changed, err)
	}
	return report
}

// Positive: a surface changed with its documentation passes and names the document, and a
// change that touches no surface, documentation-only included, passes without a word.
func TestDriftPositive(t *testing.T) {
	report := mustDrift(t, []string{"scripts/build.sh", "docs/onboarding.md"})
	if len(report.Findings) != 0 || !slices.Equal(report.Documented, []string{"build script: docs/onboarding.md"}) {
		t.Fatalf("documented change: %+v", report)
	}
	for _, changed := range [][]string{{"docs/onboarding.md"}, {"internal/build.go", "README.md"}, {}} {
		report := mustDrift(t, changed)
		if len(report.Findings)+len(report.Documented)+len(report.Waived) != 0 || report.Surfaces != 2 || report.Paths != len(changed) {
			t.Errorf("change %v touches no surface: %+v", changed, report)
		}
	}
}

// Negative: a surface changed without its documentation fails and names the surface, the
// changed path and the mapped document; neither a decision record, another guide nor a file of
// the surface itself stands in for that document.
func TestDriftNegative(t *testing.T) {
	want := "build script: scripts/build.sh changed without an edit to docs/onboarding.md"
	for _, changed := range [][]string{
		{"scripts/build.sh"},
		{"scripts/build.sh", "docs/adr/0007-build.md"},
		{"scripts/build.sh", "docs/other.md"},
	} {
		report := mustDrift(t, changed)
		if !slices.Equal(report.Findings, []string{want}) || len(report.Documented) != 0 {
			t.Errorf("change %v: findings %q, want %q", changed, report.Findings, want)
		}
	}
	self := []config.DocsSurface{{Name: "guide script", Paths: []string{"docs/**"}, Docs: []string{"docs/*.md"}}}
	report, err := Drift([]string{"docs/onboarding.md"}, self, nil)
	if err != nil || len(report.Findings) != 1 {
		t.Fatalf("a surface's own file must not document it: %+v, %v", report, err)
	}
}

// Boundary: an unmapped surface is reported rather than passing; a waiver admits every finding
// and keeps it visible; excluded files are not the surface; each surface reports separately;
// a long path list is shortened; the path bound admits its limit and refuses one more.
func TestDriftBoundary(t *testing.T) {
	change := []string{"scripts/build.sh", ".github/workflows/release.yml"}
	report := mustDrift(t, change)
	if len(report.Findings) != 2 || !strings.Contains(report.Findings[1],
		"release workflow: .github/workflows/release.yml changed, and the surface is unmapped") {
		t.Fatalf("unmapped surface: %q", report.Findings)
	}
	waiver := Waiver{Source: "commit abc", Reason: "the dry run changed, the documented path did not"}
	report = mustDrift(t, change, waiver)
	if len(report.Findings) != 0 || len(report.Waived) != 2 || !slices.Equal(report.Waivers, []Waiver{waiver}) {
		t.Fatalf("waived change: %+v", report)
	}
	if report := mustDrift(t, []string{"scripts/test_build.sh"}); len(report.Findings) != 0 {
		t.Fatalf("an excluded file is not the surface: %q", report.Findings)
	}
	many := []string{"scripts/a.sh", "scripts/b.sh", "scripts/c.sh", "scripts/d.sh", "scripts/e.sh"}
	if report := mustDrift(t, many); len(report.Findings) != 1 ||
		!strings.Contains(report.Findings[0], "scripts/a.sh, scripts/b.sh, scripts/c.sh and 2 more changed") {
		t.Fatalf("long path list: %q", report.Findings)
	}
	exact := make([]string, MaxDriftPaths)
	for index := range exact {
		exact[index] = fmt.Sprintf("src/%d.go", index)
	}
	if _, err := Drift(exact, driftSurfaces, nil); err != nil {
		t.Fatalf("exactly %d paths: %v", MaxDriftPaths, err)
	}
	if _, err := Drift(append(exact, "src/over.go"), driftSurfaces, nil); err == nil || !strings.Contains(err.Error(), "reads at most 5000") {
		t.Fatalf("one path over the bound: %v", err)
	}
	deep := strings.Repeat("d/", maxPathSegments) + "x.sh"
	if surfaceFile(config.DocsSurface{Paths: []string{"**/*.sh"}}, deep) {
		t.Fatal("a path deeper than the segment bound must not match")
	}
}

// A waiver needs a reason: the trailer and the body statement each yield one waiver per
// reasoned line, and a bare keyword yields none.
func TestWaivers(t *testing.T) {
	message := "fix: tighten the build\n\nBody.\n\nDocs-Waiver: the flag is internal\ndocs-waiver:   second reason  \nDocs-Waiver:\nSigned-off-by: a <a@b.c>\n"
	got := CommitWaivers("abc123", message)
	want := []Waiver{{Source: "commit abc123", Reason: "the flag is internal"}, {Source: "commit abc123", Reason: "second reason"}}
	if !slices.Equal(got, want) {
		t.Fatalf("CommitWaivers = %+v, want %+v", got, want)
	}
	if got := CommitWaivers("abc123", "Docs-Waiver:   \nText mentioning Docs-Waiver: inline\n"); len(got) != 0 {
		t.Fatalf("a waiver without a reason, or not at a line start, waives nothing: %+v", got)
	}
	body := BodyWaivers("## Summary\r\nNo docs needed: test-only change\r\n")
	if !slices.Equal(body, []Waiver{{Source: "pull request body", Reason: "test-only change"}}) || body[0].String() != "pull request body: test-only change" {
		t.Fatalf("BodyWaivers = %+v", body)
	}
	if got := BodyWaivers("no docs needed:\nnext line\n"); len(got) != 0 {
		t.Fatalf("a statement without a reason waives nothing: %+v", got)
	}
	long := CommitWaivers("c", "Docs-Waiver: "+strings.Repeat("r", 2*maxWaiverReasonBytes))
	if len(long) != 1 || len(long[0].Reason) != maxWaiverReasonBytes+len("... [truncated]") || !strings.HasSuffix(long[0].Reason, "[truncated]") {
		t.Fatalf("a long reason must be truncated: %d bytes", len(long[0].Reason))
	}
}

// surfaceRepository writes files into a fresh git work tree and returns its root.
func surfaceRepository(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	for _, rel := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// CheckSurfaces: positive, a declaration whose globs all select files is clean; negative, a
// paths glob selecting nothing, a docs glob selecting nothing and a docs glob selecting only a
// decision record are each named; boundary, a paths glob whose only match is excluded selects
// nothing.
func TestCheckSurfaces(t *testing.T) {
	root := surfaceRepository(t, "scripts/build.sh", "scripts/test_build.sh", "docs/onboarding.md",
		".github/workflows/release.yml", "docs/adr/0001-build.md")
	problems, err := CheckSurfaces(t.Context(), root, driftSurfaces)
	if err != nil || len(problems) != 0 {
		t.Fatalf("clean declaration: %q, %v", problems, err)
	}
	stale := []config.DocsSurface{
		{Name: "gone", Paths: []string{"tools/*.py"}, Docs: []string{"docs/tools.md", "docs/adr/*.md"}},
		{Name: "tests only", Paths: []string{"scripts/test_*.sh"}, Exclude: []string{"scripts/test_*.sh"}, Docs: []string{"docs/onboarding.md"}},
	}
	problems, err = CheckSurfaces(t.Context(), root, stale)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`gone: paths glob "tools/*.py" selects no file of the repository`,
		`gone: docs glob "docs/tools.md" matches no document of the repository (decision records under docs/adr/ never count)`,
		`gone: docs glob "docs/adr/*.md" matches no document of the repository (decision records under docs/adr/ never count)`,
		`tests only: paths glob "scripts/test_*.sh" selects no file of the repository`,
	}
	if !slices.Equal(problems, want) {
		t.Fatalf("stale declaration:\n got %q\nwant %q", problems, want)
	}
	if _, err := CheckSurfaces(t.Context(), filepath.Join(root, "scripts"), driftSurfaces); err == nil {
		t.Fatal("a directory below the work tree top must be refused")
	}
}
