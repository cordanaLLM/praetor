// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render renders the fixture's HEAD.
func (f *fixture) render(t *testing.T, runner *fakeRunner, check bool) (*Report, error) {
	t.Helper()
	report, err := Render(testContext(t), RenderOptions{Root: f.dir, Check: check, Runner: runner.run})
	noWorktreeLeft(t, f)
	return report, err
}

// staleMain lands a source change on main without its renderings.
func (f *fixture) staleMain(t *testing.T) {
	t.Helper()
	f.write(t, "src/b.txt", "beta\n")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "feat: add beta")
	f.main = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
}

// Positive (#696): render --check passes on a fresh tree; on a stale one, render writes exactly
// the changed artefacts into the checkout, the block alone inside the hand-edited guide, after
// which render --check passes again.
func TestRender_Positive_FreshPassesAndStaleIsWritten(t *testing.T) {
	f := newFixture(t)
	report, err := f.render(t, &fakeRunner{}, true)
	if err != nil || !report.Passed || report.Mode != ModeRender {
		t.Fatalf("a fresh tree must pass render --check: %+v, %v", report, err)
	}
	f.staleMain(t)
	f.write(t, "notes.txt", "untracked and unrelated\n")
	report, err = f.render(t, &fakeRunner{}, false)
	if err != nil || !report.Passed {
		t.Fatalf("render: %+v, %v", report, err)
	}
	if strings.Join(report.Written, ",") != "docs/guide.md,out/table.txt" {
		t.Fatalf("written = %q", report.Written)
	}
	if got := f.read(t, "out/table.txt"); got != "ALPHA\nBETA\n" {
		t.Fatalf("table = %q", got)
	}
	if got := f.read(t, "docs/guide.md"); got != guideText("Intro.", "ALPHA\nBETA") {
		t.Fatalf("guide = %q", got)
	}
	f.git(t, "commit", "-q", "-am", "chore(generated): render")
	if report, err = f.render(t, &fakeRunner{}, true); err != nil || !report.Passed {
		t.Fatalf("the regenerated tree must pass render --check: %+v, %v", report, err)
	}
}

// Negative (#696): render --check fails on a stale tree and writes nothing; a failing generator
// fails both modes and writes nothing; a file the checkout changed since HEAD is never
// overwritten.
func TestRender_Negative_StaleFailingAndDirty(t *testing.T) {
	f := newFixture(t)
	f.staleMain(t)
	report, err := f.render(t, &fakeRunner{}, true)
	if err != nil || report.Passed || len(report.Written) != 0 {
		t.Fatalf("a stale tree must fail render --check: %+v, %v", report, err)
	}
	mustProblem(t, report, "table: stale: out/table.txt differ from their rendering", "land a regeneration change")
	if got := f.read(t, "out/table.txt"); got != "ALPHA\n" {
		t.Fatalf("render --check wrote %q", got)
	}

	f.write(t, "out/table.txt", "LOCAL EDIT\n")
	if _, err := f.render(t, &fakeRunner{}, false); err == nil || !strings.Contains(err.Error(), "out/table.txt has changes the commit does not hold") {
		t.Fatalf("a locally changed artefact must not be overwritten: %v", err)
	}
	if got := f.read(t, "out/table.txt"); got != "LOCAL EDIT\n" {
		t.Fatalf("the local edit was overwritten: %q", got)
	}
	f.git(t, "checkout", "--", "out/table.txt")

	f.write(t, ".standards.yaml", strings.Replace(fixtureManifest, `["render", "block"]`, `["render", "fail"]`, 1))
	f.git(t, "commit", "-q", "-am", "declare a failing generator")
	report, err = f.render(t, &fakeRunner{}, false)
	if err != nil || report.Passed || len(report.Written) != 0 {
		t.Fatalf("a failing generator must fail render and write nothing: %+v, %v", report, err)
	}
	mustProblem(t, report, "guide block: does not render")
	if got := f.read(t, "out/table.txt"); got != "ALPHA\n" {
		t.Fatalf("a failed render wrote the table: %q", got)
	}
}

// Boundary (#696): an artefact whose rendering removes its only file is reported as rendering
// nothing; a rendering that deletes a selected file of a multi-file artefact writes the deletion.
func TestRender_Boundary_EmptyAndRemovedRenderings(t *testing.T) {
	f := newFixture(t)
	f.write(t, ".standards.yaml", strings.Replace(fixtureManifest, `["render", "table"]`, `["git", "rm", "-q", "out/table.txt"]`, 1))
	f.git(t, "commit", "-q", "-am", "declare a generator that removes its output")
	report, err := Render(testContext(t), RenderOptions{Root: f.dir, Check: true, Runner: mixedRunner(&fakeRunner{})})
	noWorktreeLeft(t, f)
	if err != nil || report.Passed {
		t.Fatalf("a rendering that selects no file must fail: %+v, %v", report, err)
	}
	mustProblem(t, report, "table: does not render: the rendering selects no file of out/table.txt")

	removed := newFixture(t)
	removed.write(t, "out/extra.txt", "STALE EXTRA\n")
	removed.write(t, ".standards.yaml", strings.Replace(strings.Replace(fixtureManifest, `        - "out/table.txt"`, `        - "out/*.txt"`, 1),
		`["render", "table"]`, `["git", "rm", "-q", "out/extra.txt"]`, 1))
	removed.git(t, "add", "-A")
	removed.git(t, "commit", "-q", "-m", "an extra output the generator no longer writes")
	report, err = Render(testContext(t), RenderOptions{Root: removed.dir, Runner: mixedRunner(&fakeRunner{})})
	noWorktreeLeft(t, removed)
	if err != nil || !report.Passed || strings.Join(report.Written, ",") != "out/extra.txt" {
		t.Fatalf("a removed output must be written as a removal: %+v, %v", report, err)
	}
	if _, statErr := os.Stat(filepath.Join(removed.dir, "out", "extra.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out/extra.txt survived its removal by the rendering: %v", statErr)
	}
	if got := removed.read(t, "out/table.txt"); got != "ALPHA\n" {
		t.Fatalf("an unchanged output must stay as committed: %q", got)
	}
}
