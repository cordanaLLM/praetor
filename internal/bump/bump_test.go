package bump

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestClassifyChannel_Positive(t *testing.T) {
	cases := []struct {
		ver      string
		expected ReleaseChannel
	}{
		{"v1.2.3", ChannelStable},
		{"v2.0.0-rc.1", ChannelRC},
		{"v1.0.0-beta.2", ChannelBeta},
		{"v0.5.0-alpha.1", ChannelAlpha},
		{"v0.1.0-nightly.20260901", ChannelNightly},
		{"v3.0.0-preview.1", ChannelNightly},
	}

	for _, tc := range cases {
		actual := ClassifyChannel(tc.ver)
		if actual != tc.expected {
			t.Errorf("for version %s: expected %s, got %s", tc.ver, tc.expected, actual)
		}
	}
}

func TestScanDependencies_Positive_GoMod(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	goModContent := `module test-service

go 1.24

require (
	github.com/gin-gonic/gin v1.9.1
	github.com/spf13/cobra v1.8.0-rc.1
	github.com/stretchr/testify v1.9.0-beta.1
)
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := ScanDependencies(ctx, tmpDir, true)
	if err != nil {
		t.Fatalf("ScanDependencies failed: %v", err)
	}

	if report.TotalCandidates != 3 {
		t.Fatalf("expected 3 candidates, got: %d", report.TotalCandidates)
	}
	if len(report.Stables) != 1 {
		t.Fatalf("expected 1 stable candidate, got: %d", len(report.Stables))
	}
	if len(report.Prereleases) != 2 {
		t.Fatalf("expected 2 prerelease candidates, got: %d", len(report.Prereleases))
	}
}

func TestScanDependencies_Positive_PackageJSON(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	pkgJSON := `{
  "name": "web-app",
  "dependencies": {
    "react": "^19.0.0-rc.1",
    "lodash": "^4.17.21"
  }
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := ScanDependencies(ctx, tmpDir, true)
	if err != nil {
		t.Fatalf("ScanDependencies failed: %v", err)
	}

	if report.TotalCandidates != 2 {
		t.Fatalf("expected 2 candidates, got: %d", report.TotalCandidates)
	}
	if len(report.Prereleases) != 1 || report.Prereleases[0].Package != "react" {
		t.Fatalf("expected react in prereleases, got: %v", report.Prereleases)
	}
}

func TestRunCanary_Positive_DryRun(t *testing.T) {
	ctx := context.Background()
	opts := CanaryOptions{
		RepoPath: "/tmp",
		Candidate: UpgradeCandidate{
			Package:        "github.com/spf13/cobra",
			CurrentVersion: "v1.8.0-rc.1",
			TargetVersion:  "v1.8.0-rc.2",
			Channel:        ChannelRC,
			ManifestType:   "go.mod",
		},
		DryRun: true,
	}

	res, err := RunCanary(ctx, opts)
	if err != nil {
		t.Fatalf("RunCanary dry-run failed: %v", err)
	}
	if !res.Success || !res.CanaryCertified {
		t.Fatal("expected successful certified result in dry-run")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestScanDependencies_Negative_NilContext(t *testing.T) {
	_, err := ScanDependencies(nil, "/tmp", true)
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestRunCanary_Negative_NilContext(t *testing.T) {
	opts := CanaryOptions{RepoPath: "/tmp"}
	_, err := RunCanary(nil, opts)
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestApplyBump_Negative_NilContext(t *testing.T) {
	cand := UpgradeCandidate{Package: "foo"}
	err := ApplyBump(nil, "/tmp", cand, "")
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestScanDependencies_Boundary_EmptyManifests(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module empty\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}\n"), 0644)

	rep, err := ScanDependencies(ctx, tmpDir, true)
	if err != nil {
		t.Fatalf("ScanDependencies on empty manifests failed: %v", err)
	}
	if rep.TotalCandidates != 0 {
		t.Fatalf("expected 0 candidates, got: %d", rep.TotalCandidates)
	}
}

func TestClassifyChannel_Boundary_EmptyAndComplex(t *testing.T) {
	ch1 := ClassifyChannel("")
	if ch1 != ChannelStable {
		t.Fatalf("expected stable for empty string, got: %s", ch1)
	}

	ch2 := ClassifyChannel("v1.0.0-rc.1+build.123")
	if ch2 != ChannelRC {
		t.Fatalf("expected rc channel for complex metadata, got: %s", ch2)
	}
}
