// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// previewSession is a session adopting a go-service repository under the framework profile,
// which go-service's workflows are planned for, in a dry run or a real one.
func previewSession(t *testing.T, dryRun bool, declined ...string) *adoptSession {
	t.Helper()
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(repo, "cmd/app/main.go"), "package main\n\nfunc main() {}\n")
	return &adoptSession{repoPath: repo, arch: "framework", opts: AdoptOptions{DryRun: dryRun}, report: &AdoptReport{}, declined: declined}
}

// A dry run previews over what it recorded, writes and removals alike, and over the flavor's
// planned workflows at every path it recorded nothing for; a recorded write wins over the
// flavor's body, as the earlier step's file does in the run.
func TestPreviewWorkflows_Positive_RecordedFilesThenTheFlavorsWorkflows(t *testing.T) {
	s := previewSession(t, true)
	flavorWorkflows, err := flavor.PlannedWorkflows(t.Context(), s.repoPath, s.arch)
	if err != nil || len(flavorWorkflows) == 0 {
		t.Fatalf("the fixture must plan flavor workflows: %v, %v", flavorWorkflows, err)
	}
	recordedAtFlavorPath := []byte("recorded\n")
	s.planDryRunWrite(DocumentationWorkflowFile, []byte(DocumentationWorkflow()))
	s.planDryRunWrite(flavorWorkflows[0].Path, recordedAtFlavorPath)
	s.planDryRunRemoval(".github/workflows/retired.yml")

	planned, err := s.previewWorkflows(t.Context())
	if err != nil {
		t.Fatalf("preview workflows: %v", err)
	}
	if got := planned[DocumentationWorkflowFile]; string(got) != DocumentationWorkflow() {
		t.Fatalf("the recorded documentation workflow is missing: %q", got)
	}
	if removed, ok := planned[".github/workflows/retired.yml"]; !ok || removed != nil {
		t.Fatalf("a recorded removal must stay a nil entry, got %q, %v", removed, ok)
	}
	if !bytes.Equal(planned[flavorWorkflows[0].Path], recordedAtFlavorPath) {
		t.Fatalf("a recorded write must win over the flavor's body: %q", planned[flavorWorkflows[0].Path])
	}
	for _, w := range flavorWorkflows[1:] {
		if string(planned[w.Path]) != w.Content {
			t.Fatalf("flavor workflow %s is not planned", w.Path)
		}
	}
}

// A real run previews nothing and records nothing: it has written every workflow before the
// ruleset step. A declined flavor step plans no flavor workflow, and a decline list that does not
// resolve fails the preview.
func TestPreviewWorkflows_Negative_RealRunsAndDeclinedFlavors(t *testing.T) {
	realRun := previewSession(t, false)
	realRun.planDryRunWrite(DocumentationWorkflowFile, []byte("x"))
	realRun.planDryRunRemoval(".github/workflows/retired.yml")
	if planned, err := realRun.previewWorkflows(t.Context()); err != nil || planned != nil || realRun.dryRunWrites != nil {
		t.Fatalf("a real run must plan nothing: %v, %v, %v", planned, err, realRun.dryRunWrites)
	}
	declined := previewSession(t, true, "working-dir-and-flavor")
	if planned, err := declined.previewWorkflows(t.Context()); err != nil || len(planned) != 0 {
		t.Fatalf("a declined flavor step must plan no workflow: %v, %v", planned, err)
	}
	invalid := previewSession(t, true, "no-such-step")
	if _, err := invalid.previewWorkflows(t.Context()); err == nil {
		t.Fatal("a decline list that does not resolve must fail the preview")
	}
}

// A dry run that recorded nothing over a repository no flavor matches plans an empty, non-nil
// set: it previews the disk alone. An empty recorded write stays a write, not a removal.
func TestPreviewWorkflows_Boundary_NothingRecordedAndEmptyWrites(t *testing.T) {
	s := &adoptSession{repoPath: t.TempDir(), opts: AdoptOptions{DryRun: true}, report: &AdoptReport{}}
	planned, err := s.previewWorkflows(t.Context())
	if err != nil || planned == nil || len(planned) != 0 {
		t.Fatalf("nothing recorded, no flavor: %v, %v", planned, err)
	}
	s.planDryRunWrite(".github/workflows/empty.yml", nil)
	if written, ok := s.dryRunWrites[".github/workflows/empty.yml"]; !ok || written == nil {
		t.Fatalf("an empty write must not read as a removal: %v, %v", written, ok)
	}
}
