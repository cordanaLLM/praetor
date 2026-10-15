package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func publicPolicyFunction(lines int) string {
	return "package fixture\n\nfunc Count() int {\n value := 0\n" +
		strings.Repeat(" value++\n", lines-4) + " return value\n}\n"
}

// adoptedPolicyFixture returns a verified public-loop report for a fixture whose manifest sets
// max_func_loc to limit and whose one function spans lines, with the anchor its checks run
// against. The loop runs once per argument pair and package run (sharedPolicyTemplate); every
// caller gets its own copy of the retained checkout and of the report, so it may change either.
func adoptedPolicyFixture(t *testing.T, limit, lines int) (*PublicLoopReport, publicPolicyAnchor) {
	t.Helper()
	report := copyPolicyTemplate(t, sharedPolicyTemplate(t, limit, lines))
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
	publicWrite(t, path, strings.Replace(before, "max_func_loc: 35", "max_func_loc: 34", 1), 0o600)
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

func TestMatchPublicPolicyComparesResolvedValues(t *testing.T) {
	limit50, limit49 := 50, 49
	profileSHA, plannedManifestSHA := strings.Repeat("a", 64), strings.Repeat("b", 64)
	appliedManifestSHA := strings.Repeat("c", 64)
	plannedLayers := []config.PolicyLayer{
		{Source: config.PolicySource{ID: "profile:/input/.config/archetypes/framework.yaml", SHA256: profileSHA}, Complexity: config.ComplexityOverride{MaxFuncLOC: &limit50}},
		{Source: config.PolicySource{ID: "manifest:.standards.yaml", SHA256: plannedManifestSHA}},
	}
	tests := []struct {
		name             string
		planned, applied []config.PolicyLayer
		want             string
	}{
		{"source relocation", plannedLayers, []config.PolicyLayer{
			{Source: config.PolicySource{ID: "profile:/checkout/.config/archetypes/framework.yaml", SHA256: profileSHA}, Complexity: config.ComplexityOverride{MaxFuncLOC: &limit50}},
			{Source: config.PolicySource{ID: "manifest:.standards.yaml", SHA256: appliedManifestSHA}},
		}, ""},
		{"complexity", plannedLayers, []config.PolicyLayer{
			{Source: config.PolicySource{ID: "profile:/input/.config/archetypes/framework.yaml", SHA256: profileSHA}, Complexity: config.ComplexityOverride{MaxFuncLOC: &limit49}},
			{Source: config.PolicySource{ID: "manifest:.standards.yaml", SHA256: plannedManifestSHA}},
		}, "Complexity"},
		{"branch protection", plannedLayers, []config.PolicyLayer{
			{Source: config.PolicySource{ID: "profile:/input/.config/archetypes/framework.yaml", SHA256: profileSHA}, Complexity: config.ComplexityOverride{MaxFuncLOC: &limit50}, Controls: config.ArchetypeControls{BranchProtection: config.BranchProtectionPolicy{RequireSignedCommits: true}}},
			{Source: config.PolicySource{ID: "manifest:.standards.yaml", SHA256: plannedManifestSHA}},
		}, "BranchProtection"},
		{"identical snapshots", plannedLayers, plannedLayers, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planned, err := config.ResolvePolicy(t.Context(), test.planned)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := config.ResolvePolicy(t.Context(), test.applied)
			if err != nil {
				t.Fatal(err)
			}
			err = matchPublicPolicy(planned, applied)
			if test.want == "" && err != nil {
				t.Fatalf("matching policy rejected: %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("policy mismatch error = %v, want section %s", err, test.want)
			}
			if test.name == "source relocation" {
				assertPublicPolicySourceDrift(t, planned, applied)
			}
		})
	}
	broken, err := config.ResolvePolicy(t.Context(), plannedLayers)
	if err != nil {
		t.Fatal(err)
	}
	broken.SHA256 = strings.Repeat("d", 64)
	if err := matchPublicPolicy(broken, broken); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("broken policy seal accepted: %v", err)
	}
}

func assertPublicPolicySourceDrift(t *testing.T, planned, applied *config.EffectivePolicy) {
	t.Helper()
	profile, manifest := 1, 2
	if planned.SHA256 == applied.SHA256 || planned.Sources[profile].ID == applied.Sources[profile].ID ||
		planned.Sources[profile].SHA256 != applied.Sources[profile].SHA256 ||
		planned.Sources[manifest].SHA256 == applied.Sources[manifest].SHA256 {
		t.Fatalf("source-only drift fixture is incomplete: planned=%+v applied=%+v", planned.Sources, applied.Sources)
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

// branchyFunction returns a Go file holding one function of cyclomatic complexity n.
func branchyFunction(n int) string {
	return "package fixture\n\nfunc Branchy(x int) int {\n" +
		strings.Repeat(" if x > 0 {\n  x++\n }\n", n-1) + " return x\n}\n"
}

// Positive, negative and boundary: public verification measures under the planned policy's
// complexity limit, not the scanner default, and carries the measurement in the retained
// scans without adding a single infraction. A limit of 6 admits cyclomatic 6 and measures 7.
func TestPublicPolicyComplexityIsMeasuredNotEnforced(t *testing.T) {
	for _, test := range []struct{ cyclomatic, measured int }{{6, 0}, {7, 1}} {
		t.Run(fmt.Sprintf("cyclomatic-%d", test.cyclomatic), func(t *testing.T) {
			manifest := "version: 1\nrepository:\n  owner: example\n  name: fixture\nprofiles: [framework]\n" +
				"overrides:\n  complexity:\n    max_cyclomatic: 6\n"
			opts, _ := publicLoopFixture(t, map[string]string{
				"fixture.go": branchyFunction(test.cyclomatic), ".standards.yaml": manifest,
			})
			opts.Apply = true
			report, err := RunPublicLoop(t.Context(), opts)
			if err != nil || !report.Verified {
				t.Fatalf("adoption failed: %v (%+v)", err, report)
			}
			item := report.Results[0]
			for _, scan := range []*hiss.ScanReport{item.OriginalScan, item.Attempts[len(item.Attempts)-1].Verification.Scan} {
				if scan.TotalInfractions != 0 || len(scan.Complexity.Measurements) != test.measured {
					t.Fatalf("scan = %d infractions, complexity %+v; want 0 and %d measurements", scan.TotalInfractions, scan.Complexity, test.measured)
				}
				if test.measured > 0 && scan.Complexity.Measurements[0].Limit != 6 {
					t.Errorf("measured against %d, want the planned limit of 6", scan.Complexity.Measurements[0].Limit)
				}
			}
		})
	}
}

// TestFirstPublicPolicyDifferenceNamesEveryResolvedPolicyField changes each exported section of
// config.ResolvedPolicy on its own and expects firstPublicPolicyDifference to name that section.
func TestFirstPublicPolicyDifferenceNamesEveryResolvedPolicyField(t *testing.T) {
	typ := reflect.TypeFor[config.ResolvedPolicy]()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			t.Fatalf("config.ResolvedPolicy field %s is unexported and would be skipped by the comparison", field.Name)
		}
		var planned config.ResolvedPolicy
		applied := planned
		value := reflect.ValueOf(&applied).Elem().Field(i)
		if !setNonZero(value) {
			t.Fatalf("no non-zero test value for config.ResolvedPolicy.%s (%s): extend setNonZero", field.Name, field.Type)
		}
		section, err := firstPublicPolicyDifference(planned, applied)
		if err != nil || section != field.Name {
			t.Fatalf("change to %s: firstPublicPolicyDifference = (%q, %v)", field.Name, section, err)
		}
	}
}

// setNonZero gives v a value different from its zero value, descending into the first field of a
// struct; it reports false when it cannot.
func setNonZero(v reflect.Value) bool {
	for depth := 0; depth < 8; depth++ {
		switch v.Kind() {
		case reflect.Struct:
			if v.NumField() == 0 {
				return false
			}
			v = v.Field(0)
			continue
		case reflect.Slice:
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			return true
		case reflect.String:
			v.SetString("changed")
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int, reflect.Int64, reflect.Int32:
			v.SetInt(7)
		case reflect.Uint, reflect.Uint64, reflect.Uint32:
			v.SetUint(7)
		case reflect.Map:
			v.Set(reflect.MakeMapWithSize(v.Type(), 0))
			return false
		default:
			return false
		}
		return true
	}
	return false
}
