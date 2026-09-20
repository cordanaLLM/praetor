package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const (
	legacyPraetorBadge        = "[![HISS lattice](https://img.shields.io/badge/Standards-Praetor%20HISS%20lattice-brightgreen)](AGENTS.md)"
	testReadmeGovernanceStart = "<!-- praetor:readme-governance:start -->"
	testReadmeGovernanceEnd   = "<!-- praetor:readme-governance:end -->"
)

func TestAdoptReadmeGovernanceNeverCertifiesAnUnverifiedRepository(t *testing.T) {
	repoPath := newTestRepo(t, "readme-pending")
	initial := "# Demo\n\n" + legacyPraetorBadge + "\n\nHuman introduction.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), initial)

	report, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t),
		Path:           repoPath,
		Profile:        "framework",
		RecordBaseline: false,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	assertNoIssues(t, report)
	content := mustRead(t, filepath.Join(repoPath, readmeFile))
	for _, falseClaim := range []string{"HISS%20Compliant", "This repository conforms"} {
		if strings.Contains(content, falseClaim) {
			t.Fatalf("unverified adoption published %q:\n%s", falseClaim, content)
		}
	}
	if strings.Count(content, legacyPraetorBadge) != 1 {
		t.Fatalf("custom HISS badge was removed or duplicated:\n%s", content)
	}
	for _, marker := range []string{testReadmeGovernanceStart, testReadmeGovernanceEnd} {
		if strings.Count(content, marker) != 1 {
			t.Fatalf("managed governance marker %q missing or duplicated:\n%s", marker, content)
		}
	}
	if !strings.Contains(content, "not a verification certificate") {
		t.Fatalf("managed block does not explain its evidence boundary:\n%s", content)
	}
}

func TestAdoptReadmeGovernanceRefreshesManagedStateIdempotently(t *testing.T) {
	repoPath := newTestRepo(t, "readme-refresh")
	readme := "# Legacy\n\n" + testReadmeGovernanceStart + "\nstale generated claim\n" +
		testReadmeGovernanceEnd + "\n\nHuman tail.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), readme)
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = fail()\n}\nfunc fail() error { return nil }\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true}

	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	first := mustRead(t, filepath.Join(repoPath, readmeFile))
	if strings.Contains(first, "stale generated claim") || !strings.Contains(first, "1%20baselined") {
		t.Fatalf("managed block was not refreshed from the recorded baseline:\n%s", first)
	}
	if !strings.Contains(first, "Human tail.") {
		t.Fatalf("human README content was not preserved:\n%s", first)
	}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	if second := mustRead(t, filepath.Join(repoPath, readmeFile)); second != first {
		t.Fatalf("README reconciliation is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestAdoptReadmeGovernanceRejectsMalformedMarkersWithoutChangingReadme(t *testing.T) {
	repoPath := newTestRepo(t, "readme-malformed")
	initial := "# Demo\n\n" + testReadmeGovernanceStart + "\nunterminated\n\nHuman tail.\n"
	path := filepath.Join(repoPath, readmeFile)
	mustWrite(t, path, initial)

	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed managed markers must fail explicitly, got %v", err)
	}
	if got := mustRead(t, path); got != initial {
		t.Fatalf("failed reconciliation changed README:\n%s", got)
	}
}
