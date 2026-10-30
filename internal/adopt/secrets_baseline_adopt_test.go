package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/toolpins"
	"github.com/cordanaLLM/praetor/internal/util"
)

func setupSecretsAdoptFixture(t *testing.T, withGitleaks bool) string {
	t.Helper()
	repoPath := newTestRepo(t, "secrets-adopt-fixture")
	secretFile := filepath.Join(repoPath, "creds.py")
	mustWrite(t, secretFile, "API_KEY = \"SYNTH-NEVER-SAVE\"\n")
	readmeFile := filepath.Join(repoPath, "README.md")
	mustWrite(t, readmeFile, "# Secrets Adopt Fixture\n")
	if out, err := util.RunGit(t.Context(), repoPath, "add", "creds.py", "README.md"); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := util.RunGit(t.Context(), repoPath, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "add secret and readme"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if withGitleaks {
		stubDir := t.TempDir()
		payload := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":1,"Commit":"aaaa","Secret":"SYNTH-NEVER-SAVE","Fingerprint":"synth-adopt-fp"}]`
		testsupport.BuildFakeGitleaks(t, stubDir, payload)
		t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return repoPath
}

func TestAdopt_SecretsBaseline_FirstApplied(t *testing.T) {
	repoPath := setupSecretsAdoptFixture(t, true)
	opts := readoptOptions(t, repoPath)
	rep, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("first adoption failed: %v", err)
	}
	assertNoIssues(t, rep)

	base, err := baseline.LoadBaseline(filepath.Join(repoPath, baselineFile))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if base.Count() != 1 {
		t.Fatalf("baseline Count() = %d, want 1", base.Count())
	}
	if rep.LegacyDebtCount != base.Count() {
		t.Fatalf("report LegacyDebtCount = %d, want %d (baseline Count)", rep.LegacyDebtCount, base.Count())
	}
	if rep.DebtBreakdown["HISS-12"] != 1 {
		t.Fatalf("report DebtBreakdown[HISS-12] = %d, want 1", rep.DebtBreakdown["HISS-12"])
	}
	readmeContent := mustRead(t, filepath.Join(repoPath, "README.md"))
	if !strings.Contains(readmeContent, "1 recorded infraction; audit forbids growth.") {
		t.Fatalf("README debt line missing '1 recorded infraction; audit forbids growth.':\n%s", readmeContent)
	}
}

func assertTreesMatch(t *testing.T, before, after map[string]string) {
	t.Helper()
	for rel, c1 := range before {
		c2, ok := after[rel]
		if !ok {
			t.Fatalf("file %s removed on second adoption", rel)
		}
		if c1 != c2 {
			t.Fatalf("file %s changed on second adoption: before %q after %q", rel, c1, c2)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Fatalf("file %s added on second adoption", rel)
		}
	}
}

func TestAdopt_SecretsBaseline_SecondAppliedIdempotent(t *testing.T) {
	repoPath := setupSecretsAdoptFixture(t, true)
	opts := readoptOptions(t, repoPath)
	rep1, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("first adoption failed: %v", err)
	}
	assertNoIssues(t, rep1)

	tree1 := snapshotTree(t, repoPath)

	rep2, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("second adoption failed: %v", err)
	}
	assertNoIssues(t, rep2)

	tree2 := snapshotTree(t, repoPath)
	assertTreesMatch(t, tree1, tree2)

	base2, err := baseline.LoadBaseline(filepath.Join(repoPath, baselineFile))
	if err != nil {
		t.Fatalf("load second baseline: %v", err)
	}
	if base2.Count() != 1 || len(base2.Infractions) != 1 || base2.Infractions[0].RuleID != "HISS-12" {
		t.Fatalf("second baseline infractions changed: %+v", base2)
	}
}

func TestAdopt_SecretsBaseline_DryRun(t *testing.T) {
	repoPath := setupSecretsAdoptFixture(t, true)
	opts := readoptOptions(t, repoPath)
	opts.DryRun = true
	rep, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("dry run adoption failed: %v", err)
	}
	assertNoIssues(t, rep)

	if rep.LegacyDebtCount != 1 {
		t.Fatalf("dry run report LegacyDebtCount = %d, want 1", rep.LegacyDebtCount)
	}
	if rep.DebtBreakdown["HISS-12"] != 1 {
		t.Fatalf("dry run report DebtBreakdown[HISS-12] = %d, want 1", rep.DebtBreakdown["HISS-12"])
	}
	if _, err := os.Stat(filepath.Join(repoPath, baselineFile)); !os.IsNotExist(err) {
		t.Fatalf("expected .standards-baseline.json not to be written in dry run, but stat err = %v", err)
	}
}

func TestAdopt_SecretsBaseline_GitleaksAbsent(t *testing.T) {
	repoPath := setupSecretsAdoptFixture(t, false)
	opts := readoptOptions(t, repoPath)
	rep, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adoption failed without gitleaks: %v", err)
	}
	assertNoIssues(t, rep)

	details := actionDetailsFor(rep, "secrets")
	if !strings.Contains(details, "gitleaks not found on PATH") || !strings.Contains(details, toolpins.GitleaksVersion) {
		t.Fatalf("details = %q, want missing gitleaks and pinned version %s", details, toolpins.GitleaksVersion)
	}
	if rep.LegacyDebtCount != 0 {
		t.Fatalf("report LegacyDebtCount = %d, want 0 without gitleaks", rep.LegacyDebtCount)
	}
	if count, ok := rep.DebtBreakdown["HISS-12"]; ok && count > 0 {
		t.Fatalf("report DebtBreakdown[HISS-12] = %d, want 0 without gitleaks", count)
	}
}
