package gating

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// goFuncOfLOC returns a Go file holding one function the HISS scanner measures at exactly
// loc lines: the signature and closing brace count, so the body carries loc-2 lines.
func goFuncOfLOC(loc int) string {
	var b strings.Builder
	b.WriteString("package fixture\n\nfunc long() {\n")
	for i := 0; i < loc-2; i++ {
		b.WriteString("\t_ = 0\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// strictManifest declares a repository without a lock whose overrides cap functions at 50
// lines, tighter than the 60-line default the scanner applies on its own.
const strictManifest = "version: 1\nrepository:\n  owner: example\n  name: demo\n" +
	"overrides:\n  complexity:\n    max_func_loc: 50\n"

func hissStageFixture(t *testing.T, manifest string, loc int) *stageConfig {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "long.go"), goFuncOfLOC(loc))
	if manifest != "" {
		writeFile(t, filepath.Join(root, ".standards.yaml"), manifest)
	}
	cfg, _ := newTestConfig(t, root, false)
	return cfg
}

// Positive: the gate scans with the manifest's function-length limit, as the audit does, so
// a 55-line function under a 50-line manifest limit is a new violation (BUG-638). Before the
// fix the gate scanned at the 60-line package default and admitted it.
func TestRunHissStage_Positive_ManifestLimitApplies(t *testing.T) {
	cfg := hissStageFixture(t, strictManifest, 55)
	_, err := runHissStage(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "hiss ratchet failed") {
		t.Fatalf("a 55-line function under a 50-line manifest limit must fail the ratchet, got %v", err)
	}
}

// Negative: without a manifest the gate keeps the HISS ceiling, so the same function passes
// and the stage message states the limit it scanned with.
func TestRunHissStage_Negative_NoManifestKeepsCeiling(t *testing.T) {
	cfg := hissStageFixture(t, "", 55)
	msg, err := runHissStage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("a 55-line function without a manifest must pass at the 60-line ceiling, got %v", err)
	}
	if !strings.Contains(msg, "function length limit 60") {
		t.Errorf("stage message must state the resolved limit, got %q", msg)
	}
}

// Boundary: a function at exactly the manifest limit passes and one line over fails.
func TestRunHissStage_Boundary_ExactlyAtManifestLimit(t *testing.T) {
	msg, err := runHissStage(context.Background(), hissStageFixture(t, strictManifest, 50))
	if err != nil || !strings.Contains(msg, "function length limit 50") {
		t.Fatalf("a function at exactly the 50-line limit must pass, got msg=%q err=%v", msg, err)
	}
	if _, err := runHissStage(context.Background(), hissStageFixture(t, strictManifest, 51)); err == nil {
		t.Fatal("a function one line over the 50-line limit must fail")
	}
}

// Negative: a manifest that does not parse scans at the ceiling, and the stage message
// carries the resolver's warning instead of hiding that the policy was not read.
func TestRunHissStage_Negative_UnresolvedPolicyIsReported(t *testing.T) {
	msg, err := runHissStage(context.Background(), hissStageFixture(t, "version: [\n", 10))
	if err != nil {
		t.Fatalf("an unresolvable manifest must still scan at the ceiling, got %v", err)
	}
	if !strings.Contains(msg, "repository policy unresolved") {
		t.Errorf("stage message must carry the unresolved-policy warning, got %q", msg)
	}
}

// Negative: a cancelled context stops before the scan instead of reporting a policy.
func TestRunHissStage_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runHissStage(ctx, hissStageFixture(t, "version: [\n", 10)); err == nil {
		t.Fatal("a cancelled context must fail the stage")
	}
}

// goFuncOfCyclomatic returns a Go file holding one function of cyclomatic complexity n.
func goFuncOfCyclomatic(n int) string {
	var b strings.Builder
	b.WriteString("package fixture\n\nfunc branchy(x int) int {\n")
	for i := 1; i < n; i++ {
		b.WriteString("\tif x > 0 {\n\t\tx++\n\t}\n")
	}
	b.WriteString("\treturn x\n}\n")
	return b.String()
}

// complexityStage runs the HISS stage over one branchy function under manifest and returns
// the report it filled in.
func complexityStage(t *testing.T, manifest string, cyclomatic int) *PipelineReport {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "branchy.go"), goFuncOfCyclomatic(cyclomatic))
	if manifest != "" {
		writeFile(t, filepath.Join(root, ".standards.yaml"), manifest)
	}
	cfg, _ := newTestConfig(t, root, false)
	if _, err := runHissStage(context.Background(), cfg); err != nil {
		t.Fatalf("a complexity measurement must never fail the stage: %v", err)
	}
	return cfg.rep
}

// Positive, negative and boundary: the stage measures under the repository's resolved limits
// and carries the measurements in the report, never in its verdict. Without a manifest the
// HISS-04 ceiling admits cyclomatic 10 and measures 11; a manifest tightening the limit to 8
// measures 9.
func TestRunHissStage_ComplexityIsReportedUnderResolvedLimits(t *testing.T) {
	tight := "version: 1\nrepository:\n  owner: example\n  name: demo\n" +
		"overrides:\n  complexity:\n    max_cyclomatic: 8\n"
	for _, tc := range []struct {
		name       string
		manifest   string
		cyclomatic int
		measured   int
	}{
		{"ceiling at the limit", "", 10, 0},
		{"ceiling over the limit", "", 11, 1},
		{"tightened limit", tight, 9, 1},
	} {
		rep := complexityStage(t, tc.manifest, tc.cyclomatic)
		if rep.Complexity == nil || len(rep.Complexity.Measurements) != tc.measured {
			t.Errorf("%s: complexity = %+v, want %d measurements", tc.name, rep.Complexity, tc.measured)
			continue
		}
		if tc.measured > 0 && rep.Complexity.Measurements[0].Value != tc.cyclomatic {
			t.Errorf("%s: measured %+v", tc.name, rep.Complexity.Measurements[0])
		}
	}
}
