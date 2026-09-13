package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func publicPolicyFunction(lines int) string {
	return "package fixture\n\nfunc Count() int {\n value := 0\n" +
		strings.Repeat(" value++\n", lines-4) + " return value\n}\n"
}

func adoptedPolicyFixture(t *testing.T, limit, lines int) (*PublicLoopReport, publicPolicyAnchor) {
	t.Helper()
	manifest := fmt.Sprintf("version: 1\nrepository:\n  owner: example\n  name: fixture\nprofiles: [framework]\noverrides:\n  complexity:\n    max_func_loc: %d\n", limit)
	opts, _ := publicLoopFixture(t, map[string]string{
		"fixture.go": publicPolicyFunction(lines), ".standards.yaml": manifest,
	})
	opts.Apply = true
	report, err := RunPublicLoop(t.Context(), opts)
	if err != nil || !report.Verified {
		t.Fatalf("policy adoption failed: %v (%+v)", err, report)
	}
	item := report.Results[0]
	return report, publicPolicyAnchor{Policy: item.Plan.EffectivePolicy, Scan: item.OriginalScan}
}

func TestPublicPolicyStrictLegacyAndLOCBoundary(t *testing.T) {
	for _, test := range []struct{ limit, lines, count int }{{35, 40, 1}, {35, 35, 0}, {60, 40, 0}} {
		t.Run(fmt.Sprintf("%d-of-%d", test.lines, test.limit), func(t *testing.T) {
			report, anchor := adoptedPolicyFixture(t, test.limit, test.lines)
			item := report.Results[0]
			if anchor.Scan.TotalInfractions != test.count || len(item.Attempts) != 2 {
				t.Fatalf("incorrect original debt or stabilization: %+v", item)
			}
			for _, attempt := range item.Attempts {
				check := attempt.Verification
				if check.Scan.TotalInfractions != test.count || !check.BaselineVerified || !check.Ratchet.Passed {
					t.Fatalf("strict legacy debt not preserved: %+v", check)
				}
				if check.PolicySHA256 != anchor.Policy.SHA256 || check.MaxFuncLOC != test.limit || len(check.BaselineSHA256) != 64 {
					t.Fatalf("policy/baseline evidence missing: %+v", check)
				}
			}
			if item.Attempts[0].Adoption.LegacyDebtCount != test.count {
				t.Fatalf("adoption baseline differs: %+v", item.Attempts[0].Adoption)
			}
		})
	}
}

func TestPublicPolicyRejectsTouchedAndNewDebtBelowOldDefault(t *testing.T) {
	for _, name := range []string{"fixture.go", "new.go"} {
		t.Run(name, func(t *testing.T) {
			report, anchor := adoptedPolicyFixture(t, 35, 40)
			dir := report.Results[0].Checkout
			source := strings.Replace(publicPolicyFunction(40), "value := 0", "value := 1", 1)
			if name == "new.go" {
				source = strings.Replace(source, "func Count()", "func NewCount()", 1)
			}
			publicWrite(t, filepath.Join(dir, name), source, 0o600)
			check, err := verifyPublicCheckout(t.Context(), dir, anchor, []string{name})
			if err == nil || check.Ratchet == nil || check.Ratchet.Passed || len(check.Ratchet.TouchedCleanViolations) != 1 {
				t.Fatalf("touched/new strict debt falsely verified: %v (%+v)", err, check)
			}
		})
	}
}

func TestPublicPolicyRejectsDriftAndMissingSources(t *testing.T) {
	report, anchor := adoptedPolicyFixture(t, 35, 40)
	dir := report.Results[0].Checkout
	path := filepath.Join(dir, ".standards.yaml")
	before := publicRead(t, path)
	publicWrite(t, path, before+"\n# changed after planning\n", 0o600)
	check, err := verifyPublicCheckout(t.Context(), dir, anchor, nil)
	if err == nil || !strings.Contains(err.Error(), "policy changed") || check.BaselineVerified {
		t.Fatalf("changed policy identity accepted: %v (%+v)", err, check)
	}
	publicWrite(t, path, before, 0o600)
	if err := os.Remove(filepath.Join(dir, ".config", "archetypes", "framework.yaml")); err != nil {
		t.Fatal(err)
	}
	if check, err := verifyPublicCheckout(t.Context(), dir, anchor, nil); err == nil || check.LockVerified {
		t.Fatalf("missing pinned catalog accepted: %v (%+v)", err, check)
	}
}

func TestPublicOriginalRejectsDryRunMutation(t *testing.T) {
	report, anchor := adoptedPolicyFixture(t, 35, 40)
	dir := report.Results[0].Checkout
	before, err := snapshotPublicTree(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	result := &PublicRepositoryResult{Checkout: dir, OriginalTreeDigest: before.digest(), Plan: &adopt.AdoptReport{EffectivePolicy: anchor.Policy}}
	publicWrite(t, filepath.Join(dir, "fixture.go"), publicPolicyFunction(41), 0o600)
	if err := scanPublicOriginal(t.Context(), result); err == nil || !strings.Contains(err.Error(), "dry-run changed") || result.OriginalScan != nil {
		t.Fatalf("post-plan mutation became original anchor: %v (%+v)", err, result)
	}
}

func TestPublicPolicyMissingOrInvalidNeverFallsBack(t *testing.T) {
	report, anchor := adoptedPolicyFixture(t, 35, 40)
	for _, limit := range []int{0, -1, 61} {
		invalid := *anchor.Policy
		invalid.Policy.Complexity.MaxFuncLOC = limit
		if _, err := scanPublicTree(t.Context(), report.Results[0].Checkout, &invalid); err == nil {
			t.Fatalf("invalid limit %d silently defaulted", limit)
		}
	}
	if _, err := scanPublicTree(t.Context(), report.Results[0].Checkout, nil); err == nil {
		t.Fatal("missing policy silently defaulted")
	}
	if _, err := verifyPublicCheckout(t.Context(), report.Results[0].Checkout, publicPolicyAnchor{Scan: anchor.Scan}, nil); err == nil {
		t.Fatal("missing planned policy accepted")
	}
}

func TestPublicPolicyCancellationAndTruncation(t *testing.T) {
	report, anchor := adoptedPolicyFixture(t, 35, 40)
	dir := report.Results[0].Checkout
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := verifyPublicCheckout(ctx, dir, anchor, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("verification cancellation lost: %v", err)
	}
	if _, err := scanPublicTree(ctx, dir, anchor.Policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan cancellation lost: %v", err)
	}
	truncated := publicPolicyAnchor{Policy: anchor.Policy, Scan: &hiss.ScanReport{Truncated: true}}
	if _, err := verifyPublicCheckout(t.Context(), dir, truncated, nil); !errors.Is(err, hiss.ErrScanTruncated) {
		t.Fatalf("truncated original accepted: %v", err)
	}
	source := "package fixture\nfunc Overflow() {\n" + strings.Repeat("panic(1)\n", hiss.MaxInfractionsCap+1) + "}\n"
	publicWrite(t, filepath.Join(dir, "overflow.go"), source, 0o600)
	check, err := verifyPublicCheckout(t.Context(), dir, anchor, []string{"overflow.go"})
	if !errors.Is(err, hiss.ErrScanTruncated) || check.Scan == nil || !check.Scan.Truncated || check.Ratchet != nil {
		t.Fatalf("truncated post-scan accepted: %v (%+v)", err, check)
	}
}
