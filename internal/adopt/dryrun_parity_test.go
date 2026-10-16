// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The dry run is the review surface of an adoption, of a forced one above all, so it has to name
// every path the run it previews writes, with the same action, under the same effective policy
// (#366). These tests run a dry run and a real run on two identical copies of one fixture and
// compare the two reports and the tree the real run left.

// parityRun is one adoption of a parity fixture: its report and the tree before and after it.
type parityRun struct {
	report        *AdoptReport
	before, after map[string]string
}

// adoptParityCopy builds a fresh copy of the fixture named name, prepares it, and adopts it with
// opts at that copy. Both copies share name, so both resolve the same origin identity.
func adoptParityCopy(t *testing.T, name string, prepare func(t *testing.T, repo string), opts AdoptOptions) parityRun {
	t.Helper()
	repo := newTestRepo(t, name)
	prepare(t, repo)
	opts.Path = repo
	before := snapshotTree(t, repo)
	rep, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("adopt (dry run %v, force %v): %v", opts.DryRun, opts.Force, err)
	}
	assertNoIssues(t, rep)
	return parityRun{report: rep, before: before, after: snapshotTree(t, repo)}
}

// plannedActions lists every action entry of rep as "action path", sorted.
func plannedActions(rep *AdoptReport) []string {
	actions := make([]string, 0, len(rep.ActionDetails))
	for _, entry := range rep.ActionDetails {
		actions = append(actions, entry.Action+" "+entry.Path)
	}
	slices.Sort(actions)
	return actions
}

// actionDifference returns the entries of a that b lacks, counting duplicates.
func actionDifference(a, b []string) []string {
	remaining := make(map[string]int, len(b))
	for _, entry := range b {
		remaining[entry]++
	}
	var missing []string
	for _, entry := range a {
		if remaining[entry] > 0 {
			remaining[entry]--
			continue
		}
		missing = append(missing, entry)
	}
	return missing
}

// parityWrites lists every file the run added or changed, as a slash path, git metadata and the
// private ledger included (changedPaths leaves both out). Directories are left out: a new file
// below one names it.
func parityWrites(run parityRun) []string {
	var changed []string
	for rel, hash := range run.after {
		if hash != "dir" && run.before[rel] != hash {
			changed = append(changed, filepath.ToSlash(rel))
		}
	}
	slices.Sort(changed)
	return changed
}

// coveredBy reports whether one of the report's paths names rel, or a directory holding it.
func coveredBy(rep *AdoptReport, rel string) bool {
	for _, entry := range rep.ActionDetails {
		if entry.Action == actionSkip {
			continue
		}
		if entry.Path == rel || strings.HasPrefix(rel, strings.TrimSuffix(entry.Path, "/")+"/") {
			return true
		}
	}
	return false
}

// diffParity returns every way the dry run fails to preview the real run: a different effective
// policy, archetype or facets, an action entry only one side records, or a file the real run
// wrote that no planned path names.
func diffParity(dry, applied parityRun) []string {
	var problems []string
	switch {
	case dry.report.EffectivePolicy == nil || applied.report.EffectivePolicy == nil:
		problems = append(problems, "an effective policy is missing")
	case dry.report.EffectivePolicy.SHA256 != applied.report.EffectivePolicy.SHA256:
		problems = append(problems, fmt.Sprintf("effective policy differs: dry %s, applied %s",
			dry.report.EffectivePolicy.SHA256, applied.report.EffectivePolicy.SHA256))
	}
	if dry.report.Archetype != applied.report.Archetype || !slices.Equal(dry.report.Facets, applied.report.Facets) {
		problems = append(problems, fmt.Sprintf("archetype or facets differ: dry %s %v, applied %s %v",
			dry.report.Archetype, dry.report.Facets, applied.report.Archetype, applied.report.Facets))
	}
	planned, done := plannedActions(dry.report), plannedActions(applied.report)
	for _, entry := range actionDifference(planned, done) {
		problems = append(problems, "dry run only: "+entry)
	}
	for _, entry := range actionDifference(done, planned) {
		problems = append(problems, "real run only: "+entry)
	}
	for _, rel := range parityWrites(applied) {
		if !coveredBy(dry.report, rel) {
			problems = append(problems, "written by the real run, never planned: "+rel)
		}
	}
	return append(problems, slices.Concat(createdOverExisting("dry run", dry), createdOverExisting("real run", applied))...)
}

// createdOverExisting names every path run lists as created although the file existed before it:
// an overwrite has to be listed as reconciled or replaced, never as a creation.
func createdOverExisting(side string, run parityRun) []string {
	var problems []string
	for _, rel := range run.report.CreatedFiles {
		if hash, ok := run.before[filepath.FromSlash(rel)]; ok && hash != "dir" {
			problems = append(problems, side+" lists an existing file as created: "+rel)
		}
	}
	return problems
}

// runParity adopts two copies of the fixture, one dry and one real, under the same options, and
// returns both runs. The dry run must leave its copy untouched.
func runParity(t *testing.T, name string, prepare func(t *testing.T, repo string), opts AdoptOptions) (parityRun, parityRun) {
	t.Helper()
	opts.LockSourceRoot = newAdoptLockSource(t)
	opts.DryRun = true
	dry := adoptParityCopy(t, name, prepare, opts)
	assertTreeUnchanged(t, dry.before, dry.after)
	opts.DryRun = false
	return dry, adoptParityCopy(t, name, prepare, opts)
}

// writeParityLibrary seeds the smallest tree flavor detection names go-library (goLibrary).
func writeParityLibrary(t *testing.T, repo string) {
	t.Helper()
	for rel, body := range goLibrary {
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), body)
	}
}

// Positive: on a first adoption the dry run names every path the real run writes, flavor
// templates, persona copies and the pre-commit hook included, with the same action and under the
// same effective policy.
func TestAdoptDryRunParity_Positive_FirstAdoption(t *testing.T) {
	dry, applied := runParity(t, "parity-first", writeParityLibrary, AdoptOptions{})
	if problems := diffParity(dry, applied); len(problems) > 0 {
		t.Fatalf("the dry run does not preview the real run:\n%s", strings.Join(problems, "\n"))
	}
	for _, rel := range []string{".golangci.yml", ".claude/agents/repo-auditor.md", ".git/hooks/pre-commit"} {
		if !contains(dry.report.CreatedFiles, rel) {
			t.Errorf("the dry run does not plan %s for creation: %v", rel, dry.report.CreatedFiles)
		}
	}
}

// adoptedParityLibrary is writeParityLibrary adopted once by a real run, then edited where a
// forced run overwrites or keeps a file: a persona copy, a flavor template, the lock, a pinned
// catalog file, the DevContainer, the label taxonomy and the ADR template.
func adoptedParityLibrary(t *testing.T, repo string) {
	t.Helper()
	writeParityLibrary(t, repo)
	if _, err := Adopt(context.Background(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t)}); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "repo-auditor.md"), "# hand-edited persona copy\n")
	mustWrite(t, filepath.Join(repo, ".golangci.yml"), "version: \"2\"\n# hand-edited linter configuration\n")
	for _, rel := range []string{lockFile, ".config/archetypes/framework.yaml", labelsFile, adrTemplateFile} {
		full := filepath.Join(repo, filepath.FromSlash(rel))
		mustWrite(t, full, mustRead(t, full)+"# hand edit\n")
	}
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(devcontainerFile)), "{\"name\": \"hand-edited\"}\n")
}

// Boundary: a forced re-adoption of an adopted repository is previewed path for path, and a file
// it overwrites is planned as replaced, never as created.
func TestAdoptDryRunParity_Boundary_ForcedReadoption(t *testing.T) {
	dry, applied := runParity(t, "parity-forced", adoptedParityLibrary, AdoptOptions{Force: true})
	if problems := diffParity(dry, applied); len(problems) > 0 {
		t.Fatalf("the forced dry run does not preview the forced run:\n%s", strings.Join(problems, "\n"))
	}
	for _, rel := range []string{".claude/agents/repo-auditor.md", lockFile, ".config/archetypes/framework.yaml", devcontainerFile} {
		if !hasAction(dry.report, rel, actionReplace) {
			t.Errorf("the forced dry run does not plan the edited %s as replaced: %+v", rel, dry.report.ActionDetails)
		}
	}
	if contains(dry.report.CreatedFiles, ".git/hooks/pre-commit") {
		t.Error("the forced dry run plans the installed pre-commit hook as created")
	}
}

// Negative: the comparison itself fails when either side gains a path the other lacks, or the
// policies differ, so a parity regression cannot pass unnoticed.
func TestAdoptDryRunParity_Negative_DivergenceDetected(t *testing.T) {
	dry, applied := runParity(t, "parity-negative", writeParityLibrary, AdoptOptions{})
	gained := *dry.report
	gained.ActionDetails = append(slices.Clone(gained.ActionDetails), ActionDetail{Path: "extra.txt", Action: actionCreate})
	if problems := diffParity(parityRun{report: &gained}, applied); !slices.Contains(problems, "dry run only: create extra.txt") {
		t.Errorf("a path only the dry run plans went unnoticed: %v", problems)
	}
	lost := *dry.report
	lost.ActionDetails = slices.DeleteFunc(slices.Clone(lost.ActionDetails), func(entry ActionDetail) bool {
		return entry.Path == ".golangci.yml"
	})
	if problems := diffParity(parityRun{report: &lost}, applied); !slices.Contains(problems, "written by the real run, never planned: .golangci.yml") {
		t.Errorf("a written path the dry run lacks went unnoticed: %v", problems)
	}
	policy := *dry.report.EffectivePolicy
	policy.SHA256 = strings.Repeat("0", 64)
	other := *dry.report
	other.EffectivePolicy = &policy
	if problems := diffParity(parityRun{report: &other}, applied); len(problems) == 0 || !strings.HasPrefix(problems[0], "effective policy differs") {
		t.Errorf("a different effective policy went unnoticed: %v", problems)
	}
	overwritten := parityRun{report: dry.report, before: map[string]string{lockFile: "hash"}}
	if problems := createdOverExisting("dry run", overwritten); !slices.Contains(problems, "dry run lists an existing file as created: "+lockFile) {
		t.Errorf("an existing file listed as created went unnoticed: %v", problems)
	}
}

// Boundary: a scaffold an earlier version wrote unlisted, an ADR template or a Paperclip rules
// page already present beside a missing index or harness, is kept and listed, never overwritten
// unreported, and a new one is listed under its own path.
func TestAdoptDryRunParity_Boundary_SecondaryScaffoldsListedAndKept(t *testing.T) {
	const operator = "# Operator text\n"
	kept := func(t *testing.T, repo string) {
		writeParityLibrary(t, repo)
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(adrTemplateFile)), operator)
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(paperclipRulesFile)), operator)
	}
	dry, applied := runParity(t, "parity-secondary", kept, AdoptOptions{})
	if problems := diffParity(dry, applied); len(problems) > 0 {
		t.Fatalf("the dry run does not preview the real run:\n%s", strings.Join(problems, "\n"))
	}
	for _, rel := range []string{adrTemplateFile, paperclipRulesFile} {
		if got := applied.after[filepath.FromSlash(rel)]; got != applied.before[filepath.FromSlash(rel)] {
			t.Errorf("%s was overwritten", rel)
		}
	}
	if !hasAction(applied.report, adrTemplateFile, actionReconcile) || hasAction(applied.report, paperclipRulesFile, actionCreate) {
		t.Errorf("the kept scaffolds are listed wrongly: %+v", applied.report.ActionDetails)
	}
	fresh, _ := runParity(t, "parity-secondary-fresh", writeParityLibrary, AdoptOptions{})
	for _, rel := range []string{adrTemplateFile, paperclipRulesFile} {
		if !contains(fresh.report.CreatedFiles, rel) {
			t.Errorf("%s is not planned for creation: %v", rel, fresh.report.CreatedFiles)
		}
	}
}

// Negative: where the real run cannot activate hooks, in a directory git does not know, the dry
// run reports the same failure instead of a clean preview. It used to skip hook activation, so
// it passed where the run it previewed failed.
func TestAdoptDryRunParity_Negative_HookActivationFailurePreviewed(t *testing.T) {
	source := newAdoptLockSource(t)
	for _, dryRun := range []bool{true, false} {
		stubDir := t.TempDir()
		writeStub(t, stubDir, "lefthook", "exit 1\n")
		hermeticPath(t, stubDir)
		repo := filepath.Join(t.TempDir(), "no-git")
		mustWrite(t, filepath.Join(repo, "README.md"), "# no git\n")
		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repo, SkipGitValidation: true, DryRun: dryRun})
		if err != nil {
			t.Fatalf("adopt (dry run %v): %v", dryRun, err)
		}
		if !slices.ContainsFunc(rep.Errors, func(e string) bool { return strings.HasPrefix(e, "git hooks:") }) {
			t.Errorf("dry run %v: the hook activation failure is not reported: %v", dryRun, rep.Errors)
		}
	}
}
