// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// droppedWarning returns the warning naming the adopter text a forced refresh dropped from
// AGENTS.md, or "".
func droppedWarning(rep *AdoptReport) string {
	index := slices.IndexFunc(rep.Warnings, func(warning string) bool {
		return strings.HasPrefix(warning, agentsFile+": --force replaces")
	})
	if index < 0 {
		return ""
	}
	return rep.Warnings[index]
}

// --force regenerates the harness block, so an edit to a generated invariant row or a prose line
// inside the block is dropped; the report said only that it kept the repository's instructions
// (#1243). Positive: a dry run and the run both name the edited row and count the prose line,
// and the kept text is called the instructions below the block. Negative: a block that is this
// engine's own rendering is refreshed without the warning. Boundary: a row under an ID of the
// repository's own is kept and reported as kept, not as dropped.
func TestAdopt_AgentsMD_ForceNamesDroppedAdopterText(t *testing.T) {
	repoPath := newTestRepo(t, "harness-dropped")
	agents := filepath.Join(repoPath, agentsFile)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	generated := mustRead(t, agents)
	opts.Force = true
	unchanged, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("Adopt --force over the engine's own rendering: %v", err)
	}
	if warning := droppedWarning(unchanged); warning != "" {
		t.Errorf("the engine's own rendering drops no adopter text, got %q", warning)
	}

	edited, _ := harnessAdditionsFixture(t, generated, "\n")
	table := strings.Index(edited, "| Invariant | Rule |")
	edited = edited[:table] + "Enforcement here: the Makefile lint lane decides HISS-10.\n\n" + edited[table:]
	mustWrite(t, agents, edited)
	for _, dryRun := range []bool{true, false} {
		opts.DryRun = dryRun
		rep, err := Adopt(context.Background(), opts)
		if err != nil {
			t.Fatalf("Adopt --force (dry run %t): %v", dryRun, err)
		}
		warning := droppedWarning(rep)
		for _, want := range []string{"1 edited invariant row (HISS-02)", "1 block line the new rendering does not carry"} {
			if !strings.Contains(warning, want) {
				t.Errorf("dry run %t: the warning must carry %q, got %q", dryRun, want, warning)
			}
		}
		detail := findActionDetail(rep.ActionDetails, agentsFile)
		for _, want := range []string{"2 repository invariant rows (ACME-01, ACME-02)", "the instructions below the block",
			"dropped 1 edited invariant row (HISS-02)"} {
			if !strings.Contains(detail, want) {
				t.Errorf("dry run %t: the report entry must carry %q, got %q", dryRun, want, detail)
			}
		}
		if strings.Contains(detail, "repository-specific instructions") || strings.Contains(warning, "ACME") {
			t.Errorf("dry run %t: kept text is named as kept only: %q / %q", dryRun, detail, warning)
		}
	}
}

// The same on every platform, through the harness step alone in a directory that is no
// repository: a forced dry run over an edited generated row and an added prose line names both,
// and over the step's own rendering it warns of nothing.
func TestReconcileAgentHarness_ForceDryRunNamesDroppedAdopterText(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, agentsFile)
	session := func() *adoptSession {
		return &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{},
			verification: &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}},
			opts:         AdoptOptions{Force: true, DryRun: true}}
	}
	first := session()
	first.opts = AdoptOptions{}
	if err := reconcileAgentHarness(context.Background(), first); err != nil {
		t.Fatalf("first harness step: %v", err)
	}
	generated := mustRead(t, path)
	own := session()
	if err := reconcileAgentHarness(context.Background(), own); err != nil {
		t.Fatalf("forced dry run over the step's own rendering: %v", err)
	}
	if warning := droppedWarning(own.report); warning != "" {
		t.Errorf("the step's own rendering drops no adopter text, got %q", warning)
	}
	edited, _ := harnessAdditionsFixture(t, generated, "\n")
	table := strings.Index(edited, "| Invariant | Rule |")
	mustWrite(t, path, edited[:table]+"Enforcement here: the Makefile lint lane decides HISS-10.\n\n"+edited[table:])
	forced := session()
	if err := reconcileAgentHarness(context.Background(), forced); err != nil {
		t.Fatalf("forced dry run over edited text: %v", err)
	}
	warning := droppedWarning(forced.report)
	for _, want := range []string{"1 edited invariant row (HISS-02)", "1 block line the new rendering does not carry"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning must carry %q, got %q (warnings %q)", want, warning, forced.report.Warnings)
		}
	}
}
