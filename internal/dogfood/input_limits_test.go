package dogfood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func configuredInputLimits(t *testing.T) *InputLimits {
	t.Helper()
	limits, err := normalizeInputLimits(nil)
	if err != nil {
		t.Fatal(err)
	}
	limits.Snapshot.MaxFileBytes = 32 << 20
	limits.Verification.MaxEntries = 32768
	limits.Verification.MaxFileBytes = 256 << 10
	return limits
}

func TestInputLimitsStrictOptionalContract(t *testing.T) {
	want := configuredInputLimits(t)
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeInputLimits(raw)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("configured limits: %+v %v", got, err)
	}
	if got, err := decodeInputLimits(nil); err != nil || got != nil {
		t.Fatalf("absent limits: %+v %v", got, err)
	}
	for _, invalid := range []string{"null", "{}", `{"snapshot":{}}`,
		strings.Replace(string(raw), `"max_entries":20000`, `"max_entries":null`, 1),
		strings.Replace(string(raw), `"max_entries":20000`, `"max_entries":20000,"max_entries":20000`, 1),
		strings.Replace(string(raw), `"max_entries":20000`, `"Max_entries":20000`, 1),
		strings.Replace(string(raw), `"max_file_bytes":33554432`, `"max_file_bytes":1073741824`, 1),
		strings.Replace(string(raw), `"max_entries":20000`, `"max_entries":0`, 1)} {
		if _, err := decodeInputLimits(json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid limits accepted: %s", invalid)
		}
	}
}

func TestSuitePlanRetainsConfiguredInputBudgets(t *testing.T) {
	opts := suiteOptions(t, suiteFixture(t, suiteFixtureRecord))
	opts.Stage = "plan"
	config, _, err := loadSuiteConfig(t.Context(), opts.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config.InputLimits = configuredInputLimits(t)
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	publicWrite(t, opts.ConfigPath, string(data), 0o600)
	report, err := RunSuite(t.Context(), opts)
	if err != nil || report.Verified || !reflect.DeepEqual(report.InputLimits, config.InputLimits) {
		t.Fatalf("suite limits/report: %+v %v", report, err)
	}
	_, sum, err := loadSuiteConfig(t.Context(), opts.ConfigPath)
	if err != nil || report.ConfigSHA256 != sum {
		t.Fatalf("configured input digest: %+v %v", report, err)
	}
}

func TestDiscoveryConfiguredLimitsReachPlannerAndReadback(t *testing.T) {
	root := t.TempDir()
	publicWrite(t, filepath.Join(root, "meson.build"), strings.Repeat("# metadata\n", 8000), 0o600)
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "native", Title: "Native planning", Kind: discoveryVerificationMarker, Analyzer: "adopt", Matches: []string{"meson.build"}}}}
	before, err := ObserveCapabilities(t.Context(), root, policy)
	if err == nil || before.Status != "partial" {
		t.Fatalf("default metadata bound: %+v %v", before, err)
	}
	policy.InputLimits = configuredInputLimits(t)
	report, err := ObserveCapabilities(t.Context(), root, policy)
	if err != nil || report.Status != "observed" || report.Snapshot.Status != "complete" {
		t.Fatalf("configured metadata observation: %+v %v", report, err)
	}
	if report.Observations[0].Basis != "verification-plan-declared-unavailable" {
		t.Fatalf("native execution must stay unqualified: %+v", report.Observations)
	}
	item := DiscoveryCase{Discovery: report, Status: "failed"}
	if err := finishDiscoveryCase(t.Context(), root, &item, nil); err != nil || item.Status != "observed" {
		t.Fatalf("configured snapshot readback: %+v %v", item, err)
	}
}

func TestPublicConfiguredSnapshotReusedAcrossRepeatApply(t *testing.T) {
	opts, _ := publicLoopFixture(t, map[string]string{"large-model.bin": strings.Repeat("x", 17<<20)})
	opts.InputLimits = configuredInputLimits(t)
	opts.InputLimits.Snapshot.MaxFileBytes = 20 << 20
	opts.Apply = true
	opts.MaxAttempts = 3
	// clone fixtures use a retained local source through the existing Git test wrapper.
	report, err := RunPublicLoop(t.Context(), opts)
	if err != nil || !report.Verified {
		t.Fatalf("configured public run: %+v %v", report, err)
	}
	item := report.Results[0]
	if item.OriginalSnapshot.Status != "complete" || !reflect.DeepEqual(item.Plan.Verification.Limits, &opts.InputLimits.Verification) {
		t.Fatalf("public limits not applied: %+v", item)
	}
	for _, attempt := range item.Attempts {
		if attempt.Snapshot == nil || attempt.Snapshot.Status != "complete" {
			t.Fatalf("attempt snapshot missing: %+v", attempt)
		}
	}
	if _, err := os.Stat(filepath.Join(report.RunDir, "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryPolicyPinsStrictInputLimits(t *testing.T) {
	policy := DiscoveryPolicy{Version: 1, InputLimits: configuredInputLimits(t), Rules: []DiscoveryRule{{Key: "native", Title: "Native planning", Kind: discoveryVerificationMarker, Analyzer: "adopt", Matches: []string{"meson.build"}}}}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	publicWrite(t, path, string(data), 0o600)
	got, sum, err := loadDiscoveryPolicy(t.Context(), path)
	if err != nil || !reflect.DeepEqual(got.InputLimits, policy.InputLimits) || sum != discoveryDigest(data) {
		t.Fatalf("configured policy: %+v %s %v", got, sum, err)
	}
	invalid := strings.Replace(string(data), `"max_files":128`, `"max_files":128,"max_files":128`, 1)
	publicWrite(t, path, invalid, 0o600)
	if _, _, err := loadDiscoveryPolicy(t.Context(), path); err == nil {
		t.Fatal("duplicate limits accepted by policy loader")
	}
}
