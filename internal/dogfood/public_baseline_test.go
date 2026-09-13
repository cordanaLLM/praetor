package dogfood

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func publicBaselineBytes(t *testing.T, recorded *baseline.Baseline) []byte {
	t.Helper()
	data, err := json.Marshal(recorded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func publicBaselineRecord(scan *hiss.ScanReport) *baseline.Baseline {
	return &baseline.Baseline{Version: 1, TotalInfractions: scan.TotalInfractions, Infractions: publicInfractions(scan)}
}

func publicBaselineOriginal() *hiss.ScanReport {
	// Same rule/file/line produces legitimate colliding fingerprints. Messages
	// remain independent debt entries and must retain their multiplicity.
	return &hiss.ScanReport{TotalInfractions: 2, Violations: []hiss.InvariantViolation{
		{RuleID: "HISS-07", FilePath: "fixture.go", LineNumber: 4, Message: "first ignored error"},
		{RuleID: "HISS-07", FilePath: "fixture.go", LineNumber: 4, Message: "second ignored error"},
	}}
}

func TestPublicBaselineAcceptsReorderingAndMetadata(t *testing.T) {
	original := publicBaselineOriginal()
	recorded := publicBaselineRecord(original)
	recorded.Infractions[0], recorded.Infractions[1] = recorded.Infractions[1], recorded.Infractions[0]
	recorded.GeneratedAt, recorded.Repository, recorded.CommitSHA = "different time", "different repo", "different commit"
	recorded.IncreaseRationale = "explicit imported legacy debt"
	dir := t.TempDir()
	publicWrite(t, filepath.Join(dir, ".standards-baseline.json"), string(publicBaselineBytes(t, recorded)), 0o600)
	if digest, err := verifyPublicBaseline(t.Context(), dir, original); err != nil || len(digest) != 64 {
		t.Fatalf("valid reordered entries or metadata rejected: %s %v", digest, err)
	}
}

func TestPublicBaselineRejectsCorruptedEntriesAndCounts(t *testing.T) {
	original := publicBaselineOriginal()
	cases := map[string]func(*baseline.Baseline){
		"version":     func(b *baseline.Baseline) { b.Version = 2 },
		"count":       func(b *baseline.Baseline) { b.TotalInfractions++ },
		"omitted":     func(b *baseline.Baseline) { b.Infractions = b.Infractions[:1]; b.TotalInfractions = 1 },
		"duplicate":   func(b *baseline.Baseline) { b.Infractions[1] = b.Infractions[0] },
		"fingerprint": func(b *baseline.Baseline) { b.Infractions[0].Fingerprint = "wrong" },
		"message":     func(b *baseline.Baseline) { b.Infractions[0].Message = "wrong" },
		"symbol":      func(b *baseline.Baseline) { b.Infractions[0].Symbol = "wrong" },
		"line":        func(b *baseline.Baseline) { b.Infractions[0].LineNumber++ },
	}
	dir := t.TempDir()
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			recorded := publicBaselineRecord(original)
			mutate(recorded)
			publicWrite(t, filepath.Join(dir, ".standards-baseline.json"), string(publicBaselineBytes(t, recorded)), 0o600)
			if digest, err := verifyPublicBaseline(t.Context(), dir, original); err == nil || digest != "" {
				t.Fatalf("corrupt baseline falsely verified: %s %v", digest, err)
			}
		})
	}
}

func TestPublicCheckoutRejectsPersistedBaselineCorruption(t *testing.T) {
	report, anchor := adoptedPolicyFixture(t, 35, 40)
	dir := report.Results[0].Checkout
	recorded := publicBaselineRecord(anchor.Scan)
	recorded.Infractions[0].Fingerprint = "fabricated"
	publicWrite(t, filepath.Join(dir, ".standards-baseline.json"), string(publicBaselineBytes(t, recorded)), 0o600)
	check, err := verifyPublicCheckout(t.Context(), dir, anchor, nil)
	if err == nil || check.BaselineVerified || check.Ratchet != nil {
		t.Fatalf("persisted corruption produced passing ratchet: %v (%+v)", err, check)
	}
}

func TestPublicBaselineRejectsAmbiguousJSON(t *testing.T) {
	valid := string(publicBaselineBytes(t, publicBaselineRecord(publicBaselineOriginal())))
	cases := map[string]string{
		"duplicate":   strings.Replace(valid, "\"version\":1", "\"version\":2,\"version\":1", 1),
		"alias":       strings.Replace(valid, "\"total_infractions\":2", "\"Total_Infractions\":0,\"total_infractions\":2", 1),
		"entry-alias": strings.Replace(valid, "\"rule_id\":", "\"Rule_Id\":\"wrong\",\"rule_id\":", 1),
		"unknown":     strings.Replace(valid, "{", "{\"unknown\":true,", 1),
		"missing":     strings.Replace(valid, "\"version\":1,", "", 1),
		"null-scalar": strings.Replace(valid, "\"version\":1", "\"version\":null", 1),
		"null-entry":  strings.Replace(valid, "\"message\":\"first ignored error\"", "\"message\":null", 1),
		"null-array":  "{\"version\":1,\"generated_at\":\"\",\"repository\":\"\",\"commit_sha\":\"\",\"total_infractions\":0,\"infractions\":null}",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePublicBaseline([]byte(data)); err == nil {
				t.Fatalf("ambiguous JSON accepted: %s", data)
			}
		})
	}
}

func TestPublicBaselineRequiresZeroDebtArtifactAndBoundedRead(t *testing.T) {
	original := &hiss.ScanReport{}
	dir := t.TempDir()
	path := filepath.Join(dir, ".standards-baseline.json")
	if _, err := verifyPublicBaseline(t.Context(), dir, original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing zero-debt baseline fabricated: %v", err)
	}
	data := publicBaselineBytes(t, publicBaselineRecord(original))
	exact := append(data, bytes.Repeat([]byte(" "), maxRepairReportBytes-len(data))...)
	publicWrite(t, path, string(exact), 0o600)
	if _, err := verifyPublicBaseline(t.Context(), dir, original); err != nil {
		t.Fatalf("exact byte bound rejected: %v", err)
	}
	publicWrite(t, path, string(append(exact, ' ')), 0o600)
	if _, err := verifyPublicBaseline(t.Context(), dir, original); err == nil {
		t.Fatal("oversize baseline accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.json")
	publicWrite(t, target, string(data), 0o600)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyPublicBaseline(t.Context(), dir, original); err == nil {
		t.Fatal("symlink baseline accepted")
	}
}

func TestPublicBaselineRejectsInvalidAnchorsAndCancellation(t *testing.T) {
	dir := t.TempDir()
	invalid := []*hiss.ScanReport{nil, {TotalInfractions: 1}, {Truncated: true}, {Violations: []hiss.InvariantViolation{{RuleID: "HISS-07"}}}}
	for _, original := range invalid {
		if _, err := verifyPublicBaseline(t.Context(), dir, original); err == nil {
			t.Fatalf("invalid independent anchor accepted: %+v", original)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := verifyPublicBaseline(ctx, dir, &hiss.ScanReport{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("baseline cancellation lost: %v", err)
	}
	var nilContext context.Context
	if _, err := readPublicBaseline(nilContext, dir); err == nil {
		t.Fatal("nil context accepted")
	}
}
