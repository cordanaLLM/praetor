package bump

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverGoModulesCheckedRejectsIncompleteDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "go.work"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := DiscoverGoModulesChecked(root); err == nil || len(got) != 0 {
		t.Fatalf("invalid go.work accepted: %v, %v", got, err)
	}
	if got := DiscoverGoModules(root); len(got) != 0 {
		t.Fatalf("legacy discovery returned partial modules: %v", got)
	}
}

func TestDiscoverGoModulesCheckedConfinesWorkspaceModules(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{repo, outside} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeGoMod(t, outside, "module example.com/outside\n")
	if err := os.WriteFile(filepath.Join(repo, "go.work"), []byte("use ../outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := DiscoverGoModulesChecked(repo); err == nil || len(got) != 0 {
		t.Fatalf("escaping module accepted: %v, %v", got, err)
	}
}

func TestDiscoverGoModulesCheckedFindsBoundedModules(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"module", ".hidden", "vendor", "node_modules"} {
		dir := filepath.Join(repo, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeGoMod(t, dir, "module example.com/fixture\n")
	}
	got, err := DiscoverGoModulesChecked(repo)
	if err != nil || len(got) != 1 || got[0] != "module" {
		t.Fatalf("modules=%v, err=%v", got, err)
	}
}

func TestDecodeGoModulesRejectsPartialAndOversizedReports(t *testing.T) {
	valid := `{"Path":"example.com/dep","Version":"v1.0.0","Update":{"Version":"v1.1.0"}}`
	opts := ScanOptions{IncludePrerelease: true}
	if got, err := decodeGoModules(valid, ".", opts); err != nil || len(got) != 1 {
		t.Fatalf("valid report=%v, %v", got, err)
	}
	for _, input := range []string{valid + "{", strings.Repeat(valid, 10001)} {
		if got, err := decodeGoModules(input, ".", opts); err == nil || len(got) != 0 {
			t.Fatalf("incomplete report returned %d candidates: %v", len(got), err)
		}
	}
	if got, err := decodeGoModules("", ".", opts); err != nil || len(got) != 0 {
		t.Fatalf("empty complete report=%v, %v", got, err)
	}
}

func TestScanDependenciesPropagatesInvalidManifests(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scan := range []func(context.Context, string, bool) error{
		func(ctx context.Context, path string, pre bool) error {
			_, err := ScanDependencies(ctx, path, pre)
			return err
		},
		func(ctx context.Context, path string, pre bool) error {
			_, err := AuditCodebaseVersions(ctx, path, pre)
			return err
		},
	} {
		if err := scan(t.Context(), repo, false); err == nil {
			t.Fatal("malformed package.json accepted")
		}
	}
}

func TestScanGoDependenciesHonorsCancellation(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, "module example.com/app\nrequire example.com/pkg v1.0.0\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := ScanGoDependencies(ctx, repo, ScanOptions{}); !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("cancelled scan=%v, %v", got, err)
	}
}

func TestFallbackScanReportsLineAndScannerBounds(t *testing.T) {
	for _, body := range []string{strings.Repeat("// filler\n", MaxManifestLines+1), strings.Repeat("x", 70000)} {
		repo := t.TempDir()
		writeGoMod(t, repo, body)
		if got, err := scanGoModFallback(repo, ".", ScanOptions{}); err == nil || len(got) != 0 {
			t.Fatalf("truncated fallback accepted %d records: %v", len(got), err)
		}
	}
}

func TestApplyUpdateRejectsEscapingModuleAndManifestSymlink(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	original := []byte("module example.com/outside\nrequire example.com/pkg v1.0.0\n")
	if err := os.WriteFile(filepath.Join(outside, "go.mod"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "go.mod"), filepath.Join(repo, "go.mod")); err != nil {
		t.Fatal(err)
	}
	candidate := UpgradeCandidate{Package: "example.com/pkg", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", ManifestType: "go.mod"}
	if err := fallbackGoModEdit(t.Context(), repo, candidate); err == nil {
		t.Fatal("escaping go.mod symlink accepted")
	}
	candidate.ModuleDir = outside
	if err := ApplyUpdate(t.Context(), repo, candidate); err == nil {
		t.Fatal("absolute module directory accepted")
	}
	data, err := os.ReadFile(filepath.Join(outside, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatal("outside manifest changed")
	}
}

func TestCanaryRejectsWhitespaceCommand(t *testing.T) {
	result := &CanaryResult{}
	if err := executeCanaryTest(t.Context(), t.TempDir(), " \t ", t.TempDir(), result); !errors.Is(err, ErrCanaryFailed) {
		t.Fatalf("empty test command error lost: %v", err)
	}
	if result.Success || result.CanaryCertified || result.ExecutionLog == "" {
		t.Fatalf("empty command accepted: %+v", result)
	}
}

func TestToolchainProbeActuallyChecksGoBin(t *testing.T) {
	bin, goPath := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.Mkdir(filepath.Join(goPath, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' '" + goPath + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := probeToolchain(t.Context(), "gosec"); err == nil {
		t.Fatal("working go binary hid missing gosec")
	}
	if err := os.WriteFile(filepath.Join(goPath, "bin", "gosec"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := probeToolchain(t.Context(), "gosec"); err != nil {
		t.Fatalf("installed GOPATH tool unavailable: %v", err)
	}
}

func TestWorkflowScanRejectsUnreadableSource(t *testing.T) {
	repo := t.TempDir()
	workflow := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(workflow, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(workflow, "ci.yml")); err != nil {
		t.Fatal(err)
	}
	if got, _, err := ScanWorkflowActions(repo); err == nil || len(got) != 0 {
		t.Fatalf("unreadable workflow accepted: %v, %v", got, err)
	}
}
