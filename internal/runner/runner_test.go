package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestResolveRunner_Positive(t *testing.T) {
	policy := config.DefaultRunnerPolicy()

	// Darwin arm64 -> github-hosted
	spec, err := ResolveRunner(&policy, "darwin", "arm64", false)
	if err != nil {
		t.Fatalf("expected darwin/arm64 resolution to succeed: %v", err)
	}
	if spec.Type != "github-hosted" || len(spec.RunsOn) == 0 || spec.RunsOn[0] != "macos-26" {
		t.Errorf("unexpected darwin/arm64 spec: %+v", spec)
	}

	// Linux amd64 -> self-hosted-arc
	linuxSpec, err := ResolveRunner(&policy, "linux", "amd64", false)
	if err != nil {
		t.Fatalf("expected linux/amd64 resolution to succeed: %v", err)
	}
	if linuxSpec.Type != "self-hosted-arc" || linuxSpec.RunsOn[0] != "arc-runner-set-linux-amd64" {
		t.Errorf("unexpected linux/amd64 spec: %+v", linuxSpec)
	}

	// Linux GPU -> self-hosted-arc GPU
	gpuSpec, err := ResolveRunner(&policy, "linux", "amd64", true)
	if err != nil {
		t.Fatalf("expected linux/gpu resolution to succeed: %v", err)
	}
	if gpuSpec.RunsOn[0] != "arc-runner-set-gpu-xpu" {
		t.Errorf("unexpected gpu runner: %+v", gpuSpec)
	}
}

func TestResolveRunner_Negative_And_Boundary(t *testing.T) {
	// Nil policy
	_, nilErr := ResolveRunner(nil, "linux", "amd64", false)
	if nilErr == nil {
		t.Error("expected error for nil policy, got nil")
	}

	// Negative: Darwin configured on self-hosted-arc violating platform constraints
	invalidPolicy := config.RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]config.RunnerSpec{
			"darwin/arm64": {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-linux-amd64"}},
		},
	}
	_, err := ResolveRunner(&invalidPolicy, "darwin", "arm64", false)
	if err == nil {
		t.Error("expected platform constraint violation error for darwin on self-hosted-arc, got nil")
	}

	// Boundary: Unknown architecture falls back to default
	fallbackSpec, err := ResolveRunner(&invalidPolicy, "solaris", "sparc", false)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if fallbackSpec.RunsOn[0] != invalidPolicy.Default {
		t.Errorf("expected fallback to default %s, got %s", invalidPolicy.Default, fallbackSpec.RunsOn[0])
	}
}

func TestCascadingRunnerConfig_3D(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config")
	orgDir := filepath.Join(configDir, "orgs")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 1. Fleet config
	fleetYAML := "runners:\n  default: arc-fleet-default\n"
	if err := os.WriteFile(filepath.Join(configDir, "fleet.yaml"), []byte(fleetYAML), 0644); err != nil {
		t.Fatalf("write fleet.yaml failed: %v", err)
	}

	// 2. Org config override
	orgYAML := "runners:\n  default: arc-org-cordana\n"
	if err := os.WriteFile(filepath.Join(orgDir, "cordanaLLM.yaml"), []byte(orgYAML), 0644); err != nil {
		t.Fatalf("write org config failed: %v", err)
	}

	// 3. Repo config override
	repoYAML := "runners:\n  default: arc-repo-custom\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".standards.yaml"), []byte(repoYAML), 0644); err != nil {
		t.Fatalf("write repo config failed: %v", err)
	}

	policy, err := config.LoadCascadingRunnerConfig(tmpDir, "cordanaLLM")
	if err != nil {
		t.Fatalf("cascading load failed: %v", err)
	}

	if policy.Default != "arc-repo-custom" {
		t.Errorf("expected repo-level default 'arc-repo-custom', got '%s'", policy.Default)
	}
}
