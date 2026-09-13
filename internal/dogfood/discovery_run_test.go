package dogfood

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryLocalRetentionAndReplay(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	writeDiscoveryFile(t, source, "main.ts", "export {}\n")
	writeDiscoveryFile(t, parent, "policy.json", `{"version":1,"rules":[{"key":"ts","title":"TypeScript","kind":"scanner_extension","matches":[".ts"],"analyzer":"hiss"}]}`)
	opts := DiscoveryOptions{Path: source, PolicyPath: filepath.Join(parent, "policy.json"), ArtifactDir: filepath.Join(parent, "run"), Stage: "observe"}
	report, err := RunDiscovery(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "observed" || !report.Complete || report.Verified || len(report.Candidates) != 1 {
		t.Fatalf("result: %+v", report)
	}
	if report.Candidates[0].RepositoryCount != 1 || report.Cases[0].SourceSHA != "" || report.Cases[0].TreeSHA256 == "" {
		t.Fatalf("local provenance: %+v", report)
	}
	for _, path := range []string{"plan.json", "report.json", "case-001/report.json"} {
		data, err := os.ReadFile(filepath.Join(opts.ArtifactDir, path))
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(data) {
			t.Fatalf("invalid saved %s", path)
		}
		info, err := os.Stat(filepath.Join(opts.ArtifactDir, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("evidence mode %s: %v", path, info.Mode())
		}
	}
	data, err := os.ReadFile(filepath.Join(source, "main.ts"))
	if err != nil || string(data) != "export {}\n" {
		t.Fatalf("source modified: %q %v", data, err)
	}
	if _, err := RunDiscovery(context.Background(), opts); err == nil {
		t.Fatal("overwrote evidence run")
	}
	if err := os.Rename(filepath.Join(source, "main.ts"), filepath.Join(source, "main.go")); err != nil {
		t.Fatal(err)
	}
	opts.ArtifactDir = filepath.Join(parent, "replay")
	replay, err := RunDiscovery(context.Background(), opts)
	if err != nil || len(replay.Candidates) != 0 || replay.Cases[0].TreeSHA256 == report.Cases[0].TreeSHA256 {
		t.Fatalf("stale replay: %+v %v", replay, err)
	}
}

func TestDiscoveryStabilityAndPartialExclusion(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "main.ts", "first\n")
	policy := DiscoveryPolicy{Version: 1, Rules: []DiscoveryRule{{Key: "ts", Title: "TypeScript", Kind: discoveryScannerExtension, Matches: []string{".ts"}, Analyzer: "hiss"}}}
	observation, err := ObserveCapabilities(context.Background(), root, policy)
	if err != nil {
		t.Fatal(err)
	}
	item := DiscoveryCase{ID: "case-001", Status: "failed", Discovery: observation}
	writeDiscoveryFile(t, root, "main.ts", "second\n")
	if err := finishDiscoveryCase(context.Background(), root, &item, nil); err == nil || item.Discovery != nil {
		t.Fatalf("changed source admitted: %+v %v", item, err)
	}
	for _, status := range []string{"partial", "failed", "skipped_due_to_context", "planned"} {
		item = DiscoveryCase{Status: status, TreeSHA256: observation.TreeSHA256, Discovery: observation}
		if got := aggregateDiscovery([]DiscoveryCase{item}); len(got) != 0 {
			t.Fatalf("%s admitted: %+v", status, got)
		}
	}
}

func TestDiscoveryAggregationRanksRepositories(t *testing.T) {
	observation := func(keys ...string) *CapabilityDiscovery {
		result := &CapabilityDiscovery{Status: "observed"}
		for _, key := range keys {
			result.Observations = append(result.Observations, CapabilityObservation{Key: key, Status: "unsupported", EvidenceCount: 10})
		}
		result.Observations = append(result.Observations, CapabilityObservation{Key: "unknown", Status: "unknown", EvidenceCount: 100})
		return result
	}
	cases := []DiscoveryCase{
		{ID: "case-001", Repository: "https://github.com/org/one", Status: "observed", TreeSHA256: "one", Discovery: observation("a", "b", "c")},
		{ID: "case-002", Repository: "https://github.com/org/two", Status: "observed", TreeSHA256: "two", Discovery: observation("b")},
	}
	got := aggregateDiscovery(cases)
	if len(got) != 3 || got[0].Key != "b" || got[0].RepositoryCount != 2 || got[1].Key != "a" || got[2].Key != "c" {
		t.Fatalf("rank/unknown: %+v", got)
	}
}
