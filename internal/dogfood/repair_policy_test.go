package dogfood

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func repairPublicPolicyFixture(t *testing.T) *SuiteReport {
	t.Helper()
	loop, _ := adoptedPolicyFixture(t, 35, 40)
	item := &loop.Results[0]
	// Declare the exact commit observed by the hermetic clone fixture.
	item.RequestedSHA = item.SourceSHA
	pinned := item.Repository + "#" + item.SourceSHA
	loop.Options.Repositories = []string{pinned}
	report := repairTestReport(t, 1)
	report.Status, report.Verified = "verified", true
	report.Cases[0] = SuiteCase{ID: "public-policy", Kind: "public", Status: "verified", Repository: pinned, Public: loop}
	return report
}

func cloneRepairPolicyFixture(t *testing.T, report *SuiteReport) *SuiteReport {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var clone SuiteReport
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}

func loadRepairPolicyFixture(t *testing.T, report *SuiteReport) (*SuiteReport, error) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return LoadRepairReport(t.Context(), repairTestFile(t, string(data)))
}

func TestRepairAcceptsCurrentPublicPolicyEvidenceWithoutSourceReads(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	item := &report.Cases[0].Public.Results[0]
	// Reapplying an unchanged baseline retains its count without a fresh breakdown.
	item.Attempts[len(item.Attempts)-1].Adoption.DebtBreakdown = nil
	item.Checkout = absentPath(t, "retained-public-checkout")
	policySource := absentPath(t, "policy-source")
	report.Cases[0].Public.Options.SourceRoot = policySource
	for i := range item.Plan.EffectivePolicy.Sources {
		item.Plan.EffectivePolicy.Sources[i].Path = filepath.Join(policySource, "input")
	}
	loaded, err := loadRepairPolicyFixture(t, report)
	if err != nil {
		t.Fatalf("current retained public evidence rejected: %v", err)
	}
	plan, err := PlanRepairs(t.Context(), loaded, repairTestPolicy(t))
	if err != nil || plan.Status != "no_failures" || len(plan.Jobs) != 0 {
		t.Fatalf("verified policy report manufactured repair work: %v (%+v)", err, plan)
	}
}

func TestRepairVerifiedPublicMissingPolicyEvidenceRequiresRerun(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	for _, test := range []struct {
		name   string
		mutate func(*PublicRepositoryResult)
	}{
		{"plan", func(item *PublicRepositoryResult) { item.Plan = nil }},
		{"planned policy", func(item *PublicRepositoryResult) { item.Plan.EffectivePolicy = nil }},
		{"original scan", func(item *PublicRepositoryResult) { item.OriginalScan = nil }},
		{"original tree digest", func(item *PublicRepositoryResult) { item.OriginalTreeDigest = "" }},
		{"applied policy", func(item *PublicRepositoryResult) { item.Attempts[0].Adoption.EffectivePolicy = nil }},
		{"policy digest", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.PolicySHA256 = "" }},
		{"policy limit", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.MaxFuncLOC = 0 }},
		{"baseline digest", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.BaselineSHA256 = "" }},
		{"baseline verification", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.BaselineVerified = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRepairPolicyFixture(t, report)
			test.mutate(&changed.Cases[0].Public.Results[0])
			if _, err := loadRepairPolicyFixture(t, changed); err == nil || !strings.Contains(err.Error(), "rerun required") {
				t.Fatalf("missing verified evidence did not require rerun: %v", err)
			}
		})
	}
}

func TestRepairPublicRejectsInconsistentPolicyScanAndRatchet(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	for _, test := range repairPublicPolicyMutations() {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneRepairPolicyFixture(t, report)
			test.mutate(&changed.Cases[0].Public.Results[0])
			if _, err := loadRepairPolicyFixture(t, changed); err == nil {
				t.Fatal("inconsistent public verification evidence was accepted")
			}
		})
	}
}

type repairPublicPolicyMutation struct {
	name   string
	mutate func(*PublicRepositoryResult)
}

func repairPublicPolicyMutations() []repairPublicPolicyMutation {
	return []repairPublicPolicyMutation{
		{"coherently changed limits with old digest", func(item *PublicRepositoryResult) {
			item.Plan.EffectivePolicy.Policy.Complexity.MaxFuncLOC = 60
			for i := range item.Attempts {
				item.Attempts[i].Adoption.EffectivePolicy.Policy.Complexity.MaxFuncLOC = 60
				item.Attempts[i].Verification.MaxFuncLOC = 60
			}
		}},
		{"source metadata with old digest", func(item *PublicRepositoryResult) {
			item.Plan.EffectivePolicy.Sources[0].SHA256 = strings.Repeat("d", 64)
		}},
		{"field metadata with old digest", func(item *PublicRepositoryResult) {
			item.Plan.EffectivePolicy.Fields["max_func_loc"] = []string{"builtin:audit-compat-v1"}
		}},
		{"same count different applied policy", func(item *PublicRepositoryResult) {
			item.Attempts[0].Adoption.EffectivePolicy.SHA256 = strings.Repeat("b", 64)
		}},
		{"same digest different applied limit", func(item *PublicRepositoryResult) {
			item.Attempts[0].Adoption.EffectivePolicy.Policy.Complexity.MaxFuncLOC = 60
		}},
		{"verification policy digest", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.PolicySHA256 = strings.Repeat("c", 64)
		}},
		{"verification policy limit", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.MaxFuncLOC = 60 }},
		{"invalid baseline digest", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.BaselineSHA256 = strings.Repeat("z", 64)
		}},
		{"stable tree different baseline digests", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.BaselineSHA256 = strings.Repeat("b", 64)
		}},
		{"original count", func(item *PublicRepositoryResult) { item.OriginalScan.TotalInfractions++ }},
		{"invalid original tree digest", func(item *PublicRepositoryResult) { item.OriginalTreeDigest = strings.Repeat("z", 64) }},
		{"planned adoption debt count", func(item *PublicRepositoryResult) { item.Plan.LegacyDebtCount++ }},
		{"first adoption debt count", func(item *PublicRepositoryResult) { item.Attempts[0].Adoption.LegacyDebtCount++ }},
		{"repeated adoption debt count", func(item *PublicRepositoryResult) { item.Attempts[1].Adoption.LegacyDebtCount++ }},
		{"original breakdown", func(item *PublicRepositoryResult) { item.OriginalScan.Breakdown["HISS-04"]++ }},
		{"original truncated", func(item *PublicRepositoryResult) { item.OriginalScan.Truncated = true }},
		{"verified count", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.Scan.TotalInfractions++ }},
		{"verified breakdown", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.Scan.Breakdown["HISS-04"]++ }},
		// An entry is identified by its anchor, never its line (#29): the same finding in another
		// function is another entry.
		{"same count different entry", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.Scan.Violations[0].Anchor = "fn:elsewhere"
		}},
		{"ratchet previous count", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.Ratchet.PreviousCount++ }},
		{"ratchet current count", func(item *PublicRepositoryResult) { item.Attempts[0].Verification.Ratchet.CurrentCount++ }},
		{"ratchet new entry", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.Ratchet.NewViolations = []baseline.Infraction{{RuleID: "HISS-04", FilePath: "new.go", LineNumber: 1}}
		}},
		{"ratchet touched entry", func(item *PublicRepositoryResult) {
			item.Attempts[0].Verification.Ratchet.TouchedCleanViolations = publicInfractions(item.OriginalScan)
		}},
		{"changed debt file", func(item *PublicRepositoryResult) {
			item.Attempts[0].ChangedFiles = append(item.Attempts[0].ChangedFiles, item.OriginalScan.Violations[0].FilePath)
		}},
		{"noncanonical changed file", func(item *PublicRepositoryResult) { item.Attempts[0].ChangedFiles = []string{"dir/../fixture.go"} }},
		{"applied report is simulated", func(item *PublicRepositoryResult) { item.Attempts[0].Adoption.DryRun = true }},
		{"adoption errors", func(item *PublicRepositoryResult) { item.Attempts[0].Adoption.Errors = []string{"adoption failed"} }},
	}
}

func TestRepairPublicScanAndChangedFileBounds(t *testing.T) {
	if err := validateRepairPublicScan(&hiss.ScanReport{TotalInfractions: hiss.MaxInfractionsCap + 1, Violations: make([]hiss.InvariantViolation, hiss.MaxInfractionsCap+1)}); err == nil {
		t.Fatal("oversized retained scan accepted")
	}
	for _, changed := range [][]string{make([]string, 2*maxPublicTreeEntries+1), {"fixture.go", "fixture.go"}, {"../fixture.go"}} {
		if err := validateRepairChangedFiles(changed); err == nil {
			t.Fatal("unbounded or ambiguous changed-file evidence accepted")
		}
	}
}

func TestRepairFailedHistoricalPublicEvidenceRemainsTriageInput(t *testing.T) {
	report := repairPublicPolicyFixture(t)
	report.Status, report.Verified = "failed", false
	result := &report.Cases[0]
	result.Status, result.Error = "failed", "historical public verification failed"
	result.Public.Verified = false
	item := &result.Public.Results[0]
	item.Status, item.Error = "failed", result.Error
	item.Plan.EffectivePolicy, item.OriginalScan, item.OriginalTreeDigest = nil, nil, ""
	for i := range item.Attempts {
		attempt := &item.Attempts[i]
		attempt.Adoption.EffectivePolicy = nil
		attempt.Verification.PolicySHA256, attempt.Verification.BaselineSHA256 = "", ""
		attempt.Verification.MaxFuncLOC, attempt.Verification.BaselineVerified = 0, false
	}
	loaded, err := loadRepairPolicyFixture(t, report)
	if err != nil {
		t.Fatalf("historical failed report rejected before triage: %v", err)
	}
	plan, err := PlanRepairs(t.Context(), loaded, repairTestPolicy(t))
	if err != nil || len(plan.Jobs) != 1 || plan.Jobs[0].UntrustedEvidence.CaseID != result.ID {
		t.Fatalf("historical failure lost its triage job: %v (%+v)", err, plan)
	}
}

func repairAppliedPolicyFixture(t *testing.T) *SuiteReport {
	t.Helper()
	report := repairPublicPolicyFixture(t)
	item := &report.Cases[0].Public.Results[0]

	originalManifest := publicRead(t, filepath.Join(item.Checkout, ".standards.yaml"))
	publicWrite(t, filepath.Join(item.Checkout, ".standards.yaml"), originalManifest+"\n# comment changing source hash\n", 0o600)
	appliedPolicy, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: item.Checkout, Audit: true})
	publicWrite(t, filepath.Join(item.Checkout, ".standards.yaml"), originalManifest, 0o600)
	if err != nil {
		t.Fatalf("load alternative applied policy: %v", err)
	}
	if appliedPolicy.SHA256 == item.Plan.EffectivePolicy.SHA256 {
		t.Fatal("expected applied policy SHA-256 to differ from plan")
	}
	if err := matchPublicPolicy(item.Plan.EffectivePolicy, appliedPolicy); err != nil {
		t.Fatalf("expected applied policy content to match plan: %v", err)
	}

	imageRef := baseline.Infraction{
		RuleID:      baseline.ImageRefRuleID,
		FilePath:    ".devcontainer/devcontainer.json",
		LineNumber:  3,
		Message:     "mcr.microsoft.com/devcontainers/python:3",
		Fingerprint: ".devcontainer/devcontainer.json:HISS-11:image:mcr.microsoft.com/devcontainers/python:3",
	}
	item.OriginalImageRefs = []baseline.Infraction{imageRef}
	expectedDebt := publicExpectedBaseline(item.OriginalScan, item.OriginalImageRefs).Count()
	item.Plan.LegacyDebtCount = expectedDebt

	for i := range item.Attempts {
		attempt := &item.Attempts[i]
		attempt.Adoption.EffectivePolicy = appliedPolicy
		attempt.Adoption.LegacyDebtCount = expectedDebt
		attempt.Verification.PolicySHA256 = appliedPolicy.SHA256
		attempt.Verification.MaxFuncLOC = appliedPolicy.Policy.Complexity.MaxFuncLOC
	}
	return report
}

func TestRepairAppliedPolicyDifferentSHAButEqualContentAndImageRefs(t *testing.T) {
	report := repairAppliedPolicyFixture(t)
	loaded, err := loadRepairPolicyFixture(t, report)
	if err != nil {
		t.Fatalf("verified report with differing applied policy SHA rejected: %v", err)
	}
	plan, err := PlanRepairs(t.Context(), loaded, repairTestPolicy(t))
	if err != nil || plan.Status != "no_failures" || len(plan.Jobs) != 0 {
		t.Fatalf("verified report manufactured repair work: %v (%+v)", err, plan)
	}
}

func TestRepairAppliedPolicyDifferentSHAVerificationMismatch(t *testing.T) {
	report := repairAppliedPolicyFixture(t)
	report.Cases[0].Public.Results[0].Attempts[0].Verification.PolicySHA256 = strings.Repeat("c", 64)
	_, err := loadRepairPolicyFixture(t, report)
	if err == nil || !strings.Contains(err.Error(), "public verification policy differs from the planned policy") {
		t.Fatalf("expected 'public verification policy differs from the planned policy', got: %v", err)
	}
}

func TestRepairAppliedPolicyDifferentSHAMaxFuncLOCMismatch(t *testing.T) {
	report := repairAppliedPolicyFixture(t)
	report.Cases[0].Public.Results[0].Attempts[0].Verification.MaxFuncLOC = 99
	_, err := loadRepairPolicyFixture(t, report)
	if err == nil || !strings.Contains(err.Error(), "public verification policy differs from the planned policy") {
		t.Fatalf("expected 'public verification policy differs from the planned policy', got: %v", err)
	}
}

func TestRepairAppliedPolicyDifferentSHAPlanDebtCountHISSOnly(t *testing.T) {
	report := repairAppliedPolicyFixture(t)
	item := &report.Cases[0].Public.Results[0]
	item.Plan.LegacyDebtCount = item.OriginalScan.TotalInfractions
	_, err := loadRepairPolicyFixture(t, report)
	if err == nil || !strings.Contains(err.Error(), "planned adoption debt count differs from the independent original scan") {
		t.Fatalf("expected 'planned adoption debt count differs...', got: %v", err)
	}
}

func TestRepairAppliedPolicyDifferentSHAAppliedDebtCountHISSOnly(t *testing.T) {
	report := repairAppliedPolicyFixture(t)
	item := &report.Cases[0].Public.Results[0]
	item.Attempts[0].Adoption.LegacyDebtCount = item.OriginalScan.TotalInfractions
	_, err := loadRepairPolicyFixture(t, report)
	if err == nil || !strings.Contains(err.Error(), "applied adoption debt count differs from the independent original scan") {
		t.Fatalf("expected 'applied adoption debt count differs...', got: %v", err)
	}
}

func TestRepairValidatesOriginalImageRefs(t *testing.T) {
	validRef := baseline.Infraction{
		RuleID:      baseline.ImageRefRuleID,
		FilePath:    ".devcontainer/devcontainer.json",
		LineNumber:  3,
		Message:     "python:3",
		Fingerprint: ".devcontainer/devcontainer.json:HISS-11:image:python:3",
	}

	if err := validateRepairOriginalImageRefs(nil); err != nil {
		t.Fatalf("nil image refs rejected: %v", err)
	}
	if err := validateRepairOriginalImageRefs([]baseline.Infraction{}); err != nil {
		t.Fatalf("empty image refs rejected: %v", err)
	}
	if err := validateRepairOriginalImageRefs([]baseline.Infraction{validRef}); err != nil {
		t.Fatalf("valid image ref rejected: %v", err)
	}

	invalidCases := []struct {
		name   string
		mutate func(*baseline.Infraction)
	}{
		{"rule_id not HISS-11", func(r *baseline.Infraction) { r.RuleID = "HISS-04" }},
		{"line_number zero", func(r *baseline.Infraction) { r.LineNumber = 0 }},
		{"line_number negative", func(r *baseline.Infraction) { r.LineNumber = -1 }},
		{"empty fingerprint", func(r *baseline.Infraction) { r.Fingerprint = "" }},
		{"empty file_path", func(r *baseline.Infraction) { r.FilePath = "" }},
		{"relative escaping path", func(r *baseline.Infraction) { r.FilePath = "../other/file.json" }},
		{"non-canonical path", func(r *baseline.Infraction) { r.FilePath = "a/../b.json" }},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			ref := validRef
			tc.mutate(&ref)
			if err := validateRepairOriginalImageRefs([]baseline.Infraction{ref}); err == nil {
				t.Fatalf("invalid infraction accepted for %s", tc.name)
			}
		})
	}

	t.Run("count exceeds cap", func(t *testing.T) {
		oversized := make([]baseline.Infraction, hiss.MaxInfractionsCap+1)
		for i := range oversized {
			oversized[i] = validRef
		}
		if err := validateRepairOriginalImageRefs(oversized); err == nil {
			t.Fatal("oversized image refs accepted")
		}
	})
}
