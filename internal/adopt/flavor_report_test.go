package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flavorDetail returns the first action detail recorded for rel whose text names a flavor
// template, so a detail another adoption step wrote for the same path does not count.
func flavorDetail(rep *AdoptReport, rel string) (ActionDetail, bool) {
	for _, d := range rep.ActionDetails {
		if d.Path == rel && strings.Contains(d.Details, "flavor template") {
			return d, true
		}
	}
	return ActionDetail{}, false
}

// TestAdopt_Positive_ReportsFlavorScaffold pins the adoption half of BUG-188: the apply report
// used to be discarded, so the flavor templates adoption wrote never reached its report.
func TestAdopt_Positive_ReportsFlavorScaffold(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-service")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/svc\n")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	d, ok := flavorDetail(rep, "Dockerfile")
	if !ok || d.Action != actionCreate || !strings.Contains(d.Details, "go-service") || !contains(rep.CreatedFiles, "Dockerfile") {
		t.Fatalf("the go-service Dockerfile must be reported as created by the flavor, got %+v in %v", d, rep.CreatedFiles)
	}
}

// TestAdopt_Negative_FlavorWriteFailureIsAnError asserts a template the flavor could not write
// fails the adoption instead of disappearing with the discarded apply report.
func TestAdopt_Negative_FlavorWriteFailureIsAnError(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-blocked")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/svc\n")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")
	// A directory where the flavor writes its Dockerfile makes that one template unreadable.
	if err := os.MkdirAll(filepath.Join(repoPath, "Dockerfile"), 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("a flavor failure is recorded in the report, not returned: %v", err)
	}
	found := false
	for _, e := range rep.Errors {
		found = found || (strings.Contains(e, "apply flavor go-service") && strings.Contains(e, "Dockerfile"))
	}
	if !found {
		t.Fatalf("the failed flavor template must reach the report errors, got %v", rep.Errors)
	}
	if _, ok := flavorDetail(rep, ".gosec.json"); !ok {
		t.Errorf("templates written before the failure must still be reported, got %+v", rep.ActionDetails)
	}
}

// TestAdopt_Boundary_NoFlavorMatchScaffoldsNothing pins BUG-939: a repository no flavor matches
// used to be scaffolded as go-library, because detection substituted that name.
func TestAdopt_Boundary_NoFlavorMatchScaffoldsNothing(t *testing.T) {
	repoPath := newTestRepo(t, "flavor-unmatched")
	mustWrite(t, filepath.Join(repoPath, "Rakefile"), "task :default\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !hasAction(rep, flavorReportPath, actionSkip) {
		t.Fatalf("an unmatched repository must record the flavor scaffold as skipped, got %+v", rep.ActionDetails)
	}
	warned := false
	for _, w := range rep.Warnings {
		warned = warned || strings.Contains(w, "Not applicable: no registered flavor matches")
	}
	if !warned {
		t.Errorf("the operator must be told no flavor applied, got warnings %v", rep.Warnings)
	}
	for _, d := range rep.ActionDetails {
		if strings.Contains(d.Details, "go-library flavor template") {
			t.Errorf("no go-library template may be scaffolded for an unmatched repository: %+v", d)
		}
	}
}
