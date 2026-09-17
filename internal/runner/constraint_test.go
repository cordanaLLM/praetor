package runner

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestResolveRunner_Negative_DarwinMissRefusesTheLinuxFallback is the defect: a routing
// table without a darwin entry sent a macOS build to the default self-hosted ARC pool,
// which is Linux, because the fallback never faced the constraint the found-entry path
// enforces.
func TestResolveRunner_Negative_DarwinMissRefusesTheLinuxFallback(t *testing.T) {
	linuxOnly := config.RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]config.RunnerSpec{
			"linux/amd64": {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-linux-amd64"}},
		},
	}
	for _, arch := range []string{"arm64", "amd64", "sparc"} {
		spec, err := ResolveRunner(&linuxOnly, "darwin", arch, false)
		if err == nil {
			t.Errorf("darwin/%s has no routing entry and must not fall back to a Linux pool, got %+v", arch, spec)
			continue
		}
		if !strings.Contains(err.Error(), "platform constraint violation") {
			t.Errorf("darwin/%s: expected the platform constraint error, got %v", arch, err)
		}
		if spec.Type != "" || len(spec.RunsOn) != 0 {
			t.Errorf("darwin/%s: a refused resolution must return no spec, got %+v", arch, spec)
		}
	}
	// macos and osx normalise to darwin, so they are refused identically.
	if _, err := ResolveRunner(&linuxOnly, "macos", "arm64", false); err == nil {
		t.Error("macos normalises to darwin and must be refused as well")
	}
}

// TestResolveRunner_Positive_DarwinRouteIsHonoured keeps the refusal narrow: a policy that
// does route darwin still resolves, on a hit and through the default table.
func TestResolveRunner_Positive_DarwinRouteIsHonoured(t *testing.T) {
	routed := config.RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]config.RunnerSpec{
			"darwin/arm64": {Type: "github-hosted", RunsOn: []string{"macos-14"}, Ephemeral: true},
		},
	}
	spec, err := ResolveRunner(&routed, "darwin", "arm64", false)
	if err != nil {
		t.Fatalf("a routed darwin target must resolve: %v", err)
	}
	if spec.Type != "github-hosted" || spec.RunsOn[0] != "macos-14" {
		t.Errorf("unexpected darwin spec: %+v", spec)
	}

	defaults := config.DefaultRunnerPolicy()
	if _, err := ResolveRunner(&defaults, "darwin", "amd64", false); err != nil {
		t.Errorf("the default policy routes darwin/amd64 and must resolve: %v", err)
	}
}

// TestResolveRunner_Boundary_OneEntryTable covers the smallest routing table, on the hit
// and on the miss, for a target the constraint does not apply to.
func TestResolveRunner_Boundary_OneEntryTable(t *testing.T) {
	single := config.RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]config.RunnerSpec{
			"linux/amd64": {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-linux-amd64"}},
		},
	}
	hit, err := ResolveRunner(&single, "linux", "amd64", false)
	if err != nil || hit.RunsOn[0] != "arc-runner-set-linux-amd64" {
		t.Fatalf("the single entry must resolve, got %+v err=%v", hit, err)
	}
	miss, err := ResolveRunner(&single, "linux", "arm64", false)
	if err != nil {
		t.Fatalf("a linux miss is still allowed on the Linux default pool: %v", err)
	}
	if miss.RunsOn[0] != single.Default {
		t.Errorf("a linux miss falls back to the default pool, got %+v", miss)
	}

	empty := config.RunnerPolicy{Default: "arc-runner-set-linux-amd64"}
	if _, err := ResolveRunner(&empty, "darwin", "arm64", false); err == nil {
		t.Error("an empty routing table refuses darwin rather than scheduling it on Linux")
	}
}
