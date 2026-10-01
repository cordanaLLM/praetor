// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"strings"
	"testing"
)

// check runs a pull-request check of the fixture's current branch against main.
func (f *fixture) check(t *testing.T, runner *fakeRunner, branch, title string) *Report {
	t.Helper()
	report, err := Check(testContext(t), CheckOptions{Root: f.dir, Base: "main", Branch: branch, Title: title, Runner: runner.run})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	noWorktreeLeft(t, f)
	return report
}

// Positive (#696): a change to the sources alone passes. Both artefacts render, each names the
// changed source, and each reports that its committed file now lags the sources, which a pull
// request is allowed to leave to the regeneration change.
func TestCheck_Positive_SourceChangePasses(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "feat/beta", map[string]*string{"src/b.txt": text("beta\n")})
	runner := &fakeRunner{}
	report := f.check(t, runner, "feat/beta", "feat: add beta")
	if !report.Passed || len(report.Problems) != 0 || report.Regeneration {
		t.Fatalf("a source-only change must pass: %+v", report)
	}
	table := resultFor(t, report, "table")
	if table.Rendered != RenderedOK || strings.Join(table.SourcesChanged, ",") != "src/b.txt" ||
		strings.Join(table.Changed, ",") != "out/table.txt" || len(table.Edited) != 0 {
		t.Fatalf("table = %+v", table)
	}
	if block := resultFor(t, report, "guide block"); block.Rendered != RenderedOK || strings.Join(block.Changed, ",") != "docs/guide.md" {
		t.Fatalf("guide block = %+v", block)
	}
	if strings.Join(runner.ran, ";") != "render table;render block" {
		t.Fatalf("ran %q, want each command once in declaration order", runner.ran)
	}
	if len(report.Artefacts) != 2 {
		t.Fatalf("a built-in artefact that applies neither at the base nor at the head is not guarded: %+v", report.Artefacts)
	}
}

// Negative (#696): a change that edits a declared artefact is refused, whole file or block, and
// so is one whose generator fails, even though it edits nothing generated: the gate fails closed.
func TestCheck_Negative_EditsAndFailuresAreRefused(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "feat/edit", map[string]*string{"out/table.txt": text("HAND EDIT\n"), "docs/guide.md": text(guideText("Intro.", "HAND"))})
	report := f.check(t, &fakeRunner{}, "feat/edit", "feat: edit generated files")
	if report.Passed {
		t.Fatalf("an edit of declared artefacts must fail: %+v", report)
	}
	mustProblem(t, report, "table: the change edits out/table.txt", "only a regeneration change", "regen/<name>", "chore(generated)")
	mustProblem(t, report, "guide block: the change edits docs/guide.md")

	failing := newFixture(t)
	failing.write(t, ".standards.yaml", strings.Replace(fixtureManifest, `["render", "table"]`, `["render", "fail"]`, 1))
	failing.git(t, "commit", "-q", "-am", "declare a failing generator")
	failing.main = strings.TrimSpace(failing.git(t, "rev-parse", "HEAD"))
	failing.branch(t, "feat/beta", map[string]*string{"src/b.txt": text("beta\n")})
	report = failing.check(t, &fakeRunner{}, "feat/beta", "feat: add beta")
	if report.Passed || resultFor(t, report, "table").Rendered != RenderedFailed {
		t.Fatalf("a failing generator must fail the check: %+v", report)
	}
	mustProblem(t, report, "table: does not render", "render fail failed", "generator exploded")
}

// Negative (#696): a change cannot escape the rule by dropping the declaration together with the
// artefact edit, nor by declaring a marker that would admit itself: the base's declaration and
// marker judge it.
func TestCheck_Negative_TheBaseDeclarationJudges(t *testing.T) {
	f := newFixture(t)
	undeclared := strings.Replace(fixtureManifest, `    - name: "table"
      paths:
        - "out/table.txt"
      command: ["render", "table"]
      sources:
        - "src/*.txt"
`, "", 1)
	f.branch(t, "feat/undeclare", map[string]*string{".standards.yaml": text(undeclared), "out/table.txt": text("HAND\n")})
	report := f.check(t, &fakeRunner{}, "feat/undeclare", "feat: stop declaring the table")
	mustProblem(t, report, "table: the change edits out/table.txt")

	selfAdmitted := strings.Replace(fixtureManifest, "generated:\n", "generated:\n  regeneration:\n    branch_prefix: \"feat/\"\n    title_type: \"feat\"\n", 1)
	f.branch(t, "feat/self", map[string]*string{".standards.yaml": text(selfAdmitted), "out/table.txt": text("ALPHA\n\n")})
	report = f.check(t, &fakeRunner{}, "feat/self", "feat: admit myself")
	if report.Regeneration || report.Marker.BranchPrefix != "regen/" {
		t.Fatalf("the change's own marker must not admit it: %+v", report)
	}
	mustProblem(t, report, "table: the change edits out/table.txt")
}

// Positive (#696): a regeneration change, recognised by the base's marker on both its branch and
// its title, may commit the renderings, a block included, and passes when they equal the
// rendering of its sources.
func TestCheck_Positive_RegenerationMarkerAdmitsTheRendering(t *testing.T) {
	f := newFixture(t)
	f.write(t, "src/b.txt", "beta\n")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "feat: add beta")
	f.main = strings.TrimSpace(f.git(t, "rev-parse", "HEAD"))
	f.branch(t, "regen/batch-1", map[string]*string{"out/table.txt": text("ALPHA\nBETA\n"), "docs/guide.md": text(guideText("Intro.", "ALPHA\nBETA"))})
	report := f.check(t, &fakeRunner{}, "regen/batch-1", "chore(generated): render batch 1")
	if !report.Passed || !report.Regeneration || len(report.Outside) != 0 {
		t.Fatalf("a fresh regeneration change must pass: %+v", report)
	}
	if table := resultFor(t, report, "table"); strings.Join(table.Edited, ",") != "out/table.txt" || len(table.Changed) != 0 {
		t.Fatalf("table = %+v", table)
	}
}

// Negative (#696): the marker admits renderings only. A regeneration change that edits a source,
// edits the hand-written part of a file around a block, or commits a file that differs from its
// rendering is refused; the marker needs both the branch and the title.
func TestCheck_Negative_RegenerationIsNoBypass(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "regen/smuggle", map[string]*string{"src/a.txt": text("gamma\n"), "out/table.txt": text("GAMMA\n"),
		"docs/guide.md": text(guideText("Changed intro.", "GAMMA"))})
	report := f.check(t, &fakeRunner{}, "regen/smuggle", "chore(generated): render")
	mustProblem(t, report, "edits only declared generated artefacts", "src/a.txt", "docs/guide.md")

	f.branch(t, "regen/stale", map[string]*string{"out/table.txt": text("WRONG\n")})
	report = f.check(t, &fakeRunner{}, "regen/stale", "chore(generated): render")
	mustProblem(t, report, "table: the regeneration change commits out/table.txt, which differ from their rendering")

	f.branch(t, "regen/title", map[string]*string{"out/table.txt": text("TITLE\n")})
	for _, title := range []string{"", "fix: render", "chore: render", "chore(docs): render"} {
		report = f.check(t, &fakeRunner{}, "regen/title", title)
		if report.Regeneration {
			t.Errorf("title %q must not carry the marker", title)
		}
	}
	report = f.check(t, &fakeRunner{}, "feat/regen", "chore(generated): render")
	if report.Regeneration {
		t.Fatal("a branch without the prefix must not carry the marker")
	}
}

// Boundary (#696): an edit beside the block is no edit of the artefact; a removed artefact file
// and a renamed one are edits under their old path; an unknown base is an error, not a pass.
func TestCheck_Boundary_BlocksRenamesAndRevisions(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "docs/intro", map[string]*string{"docs/guide.md": text(guideText("A better intro.", "ALPHA"))})
	report := f.check(t, &fakeRunner{}, "docs/intro", "docs: improve the intro")
	if !report.Passed || len(resultFor(t, report, "guide block").Edited) != 0 {
		t.Fatalf("an edit outside the block must pass: %+v", report)
	}
	f.git(t, "checkout", "-q", "-B", "feat/move", f.main)
	f.git(t, "mv", "out/table.txt", "out/moved.txt")
	f.git(t, "commit", "-q", "-m", "move the table")
	report = f.check(t, &fakeRunner{}, "feat/move", "feat: move the table")
	if edited := resultFor(t, report, "table").Edited; strings.Join(edited, ",") != "out/table.txt" {
		t.Fatalf("a rename must count as an edit of the old path: %q", edited)
	}
	if _, err := Check(testContext(t), CheckOptions{Root: f.dir, Base: "no-such-branch", Runner: (&fakeRunner{}).run}); err == nil ||
		!strings.Contains(err.Error(), "names no commit") {
		t.Fatalf("an unknown base must be an error: %v", err)
	}
	if _, err := Check(testContext(t), CheckOptions{Root: f.dir, Base: "--output=x", Runner: (&fakeRunner{}).run}); err == nil {
		t.Fatal("an option-shaped base must be refused")
	}
	noWorktreeLeft(t, f)
}
