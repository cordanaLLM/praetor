package dogfood

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObserveCapabilitiesScannerAndNestedEvidence(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "src/a.ts", "export const answer = 42\n")
	writeDiscoveryFile(t, root, "nested/go.mod", "module example.test/nested\n")
	writeDiscoveryFile(t, root, "nested/main.go", "package main\n")
	writeDiscoveryFile(t, root, ".workingdir/ignored.ts", "ignored\n")
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{
		{Key: "hiss:typescript", Title: "TypeScript", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"},
		{Key: "needs:go", Title: "Go", Kind: discoveryNeedsMarker, Matches: []string{"go.mod"}, Analyzer: "go"},
	}}
	report, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if report.Status != "observed" || report.FilesObserved != 3 || report.FilesMatched != 2 {
		t.Fatalf("scope: %+v", report)
	}
	if len(report.Observations) != 2 {
		t.Fatalf("one observation per rule: %+v", report.Observations)
	}
	if report.Observations[0].Status != "unsupported" || report.Observations[0].EvidenceCount != 1 || len(report.Observations[0].Evidence) != 1 {
		t.Fatalf("typescript: %+v", report.Observations[0])
	}
	if report.Observations[0].Evidence[0].SHA256 == "" || strings.Contains(report.Observations[0].Evidence[0].Path, ".workingdir") {
		t.Fatalf("evidence: %+v", report.Observations[0])
	}
	if report.Observations[1].Status != "available" || report.Observations[1].EvidenceCount != 1 {
		t.Fatalf("go: %+v", report.Observations[1])
	}
}

func TestObserveCapabilitiesNativeMesonAndGPUExtensions(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "meson.build", "project('fixture')\n")
	writeDiscoveryFile(t, root, "src/kernel.cu", "__global__ void kernel() {}\n")
	writeDiscoveryFile(t, root, "src/kernel.hip", "__global__ void kernel() {}\n")
	writeDiscoveryFile(t, root, "src/host.cxx", "int main() { return 0; }\n")
	writeDiscoveryFile(t, root, "src/native.mm", "int main() { return 0; }\n")
	writeDiscoveryFile(t, root, "src/native.cuh", "__global__ void kernel() {}\n")
	writeDiscoveryFile(t, root, "src/native.metal", "kernel void kernel() {}\n")
	writeDiscoveryFile(t, root, "src/native.comp", "#version 450\n")
	writeDiscoveryFile(t, root, "src/native.glsl", "void main() {}\n")
	policyPath := filepath.Join("..", "..", ".config", "dogfood", "discovery-policy.json")
	policy, _, err := loadDiscoveryPolicy(context.Background(), policyPath)
	if err != nil {
		t.Fatalf("load default policy: %v", err)
	}
	report, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if report.Status != "observed" || report.FilesObserved != 9 || report.FilesMatched != 9 {
		t.Fatalf("scope: %+v", report)
	}
	observations := make(map[string]CapabilityObservation, len(report.Observations))
	for _, observation := range report.Observations {
		observations[observation.Key] = observation
	}
	for key, want := range map[string]struct {
		status string
		count  int
	}{
		"hiss:native":       {"available", 3},
		"hiss:cuda-headers": {"unsupported", 1},
		"hiss:apple-native": {"unsupported", 2},
		"hiss:shaders":      {"unsupported", 2},
		"needs:native":      {"available", 1},
	} {
		observation, ok := observations[key]
		if !ok || observation.Status != want.status || observation.EvidenceCount != want.count {
			t.Errorf("%s: got=%+v present=%v", key, observation, ok)
		}
	}
	verification := observations["verification:native"]
	if verification.Status != "unknown" || verification.Basis != "verification-plan-declared-unavailable" {
		t.Fatalf("native verification: %+v", verification)
	}
}

func TestObserveCapabilitiesNoMatchAndInvalidPolicy(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "README.md", "hello\n")
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "hiss:typescript", Title: "TypeScript", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"}}}
	report, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil || report.Status != "observed" || report.Observations[0].Basis != "not_applicable" {
		t.Fatalf("no match: report=%+v err=%v", report, err)
	}
	bad := policy
	bad.Rules[0].Matches = []string{"../ts"}
	if _, err := ObserveCapabilities(context.Background(), root, bad); err == nil {
		t.Fatal("traversal policy accepted")
	}
}

func TestObserveCapabilitiesEmptyAndCancellation(t *testing.T) {
	root := t.TempDir()
	if _, err := ObserveCapabilities(context.Background(), root, DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "x", Title: "x", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"}}}); err == nil {
		t.Fatal("empty snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ObserveCapabilities(ctx, root, DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "x", Title: "x", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"}}}); err == nil {
		t.Fatal("cancelled observation accepted")
	}
}

func writeDiscoveryFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryMixedExtensionsEvidenceAndUniqueCounts(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "available.go", "package x\n")
	writeDiscoveryFile(t, root, "missing.TS", "export {}\n")
	rule := DiscoveryRule{Key: "mixed", Title: "mixed", Kind: discoveryScannerExtension, Matches: []string{".go", ".ts"}, Analyzer: "hiss"}
	second := rule
	second.Key = "duplicate-demand"
	result, err := ObserveCapabilities(context.Background(), root, DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{rule, second}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("double counted files: %+v", result)
	}
	for _, obs := range result.Observations {
		if obs.Status != "unsupported" || obs.EvidenceCount != 1 || len(obs.Evidence) != 1 || obs.Evidence[0].Path != "missing.TS" {
			t.Fatalf("available extension masked gap or contaminated evidence: %+v", obs)
		}
	}
}

func TestDiscoveryMixedMarkerRootsAndBound(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "available/go.mod", "module example.test/a\n")
	writeDiscoveryFile(t, root, "missing/custom.mod", "missing\n")
	rule := DiscoveryRule{Key: "mixed", Title: "mixed", Kind: discoveryNeedsMarker, Matches: []string{"go.mod", "custom.mod"}, Analyzer: "go"}
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{rule}}
	result, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil {
		t.Fatal(err)
	}
	obs := result.Observations[0]
	if obs.Status != "unsupported" || obs.EvidenceCount != 1 || obs.Evidence[0].Path != "missing/custom.mod" {
		t.Fatalf("mixed roots: %+v", obs)
	}
	for i := 0; i < maxDiscoveryRoots; i++ {
		writeDiscoveryFile(t, root, fmt.Sprintf("root-%03d/go.mod", i), "module example.test/a\n")
	}
	result, err = ObserveCapabilities(context.Background(), root, policy)
	if err == nil || result.Status != "partial" || result.Observations[0].Status != "unknown" {
		t.Fatalf("silently truncated roots: %+v %v", result, err)
	}
}

func TestDiscoveryEvidenceCapAndVerificationUnknown(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxDiscoveryEvidence+1; i++ {
		writeDiscoveryFile(t, root, fmt.Sprintf("source-%03d.ts", i), "export {}\n")
	}
	writeDiscoveryFile(t, root, "project/Makefile", "# no declared verification\n")
	result, err := ObserveCapabilities(context.Background(), root, DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{
		{Key: "ts", Title: "ts", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"},
		{Key: "verify", Title: "verify", Kind: discoveryVerificationMarker, Matches: []string{"Makefile"}, Analyzer: "adopt"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Observations[0].EvidenceCount != maxDiscoveryEvidence+1 || len(result.Observations[0].Evidence) != maxDiscoveryEvidence {
		t.Fatalf("evidence bound: %+v", result)
	}
	if result.Observations[1].Status != "unknown" || result.Status != "observed" {
		t.Fatalf("ambiguous upstream setup labeled capability: %+v", result)
	}
}

func TestDiscoverySnapshotPrivateAndSymlinkIsolation(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "a.go", "package a\n")
	writeDiscoveryFile(t, root, ".workingdir/private.ts", "private\n")
	outside := t.TempDir()
	writeDiscoveryFile(t, outside, "secret.ts", "secret\n")
	if err := os.Symlink(filepath.Join(outside, "secret.ts"), filepath.Join(root, "linked.ts")); err != nil {
		t.Fatal(err)
	}
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "ts", Title: "ts", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"}}}
	result, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesObserved != 1 || result.FilesMatched != 0 {
		t.Fatalf("followed ignored or symlink content: %+v", result)
	}
	before := result.TreeSHA256
	writeDiscoveryFile(t, root, ".workingdir/private.ts", "changed\n")
	result, err = ObserveCapabilities(context.Background(), root, policy)
	if err != nil || result.TreeSHA256 != before {
		t.Fatalf("private state affected public evidence: %+v %v", result, err)
	}
}
