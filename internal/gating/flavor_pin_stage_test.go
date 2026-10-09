package gating

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// appServiceWithPin declares app-service, which go-library does not implement, so only a pin
// can reach it (#1103).
func appServiceWithPin(pin string) string {
	return "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - app-service\n" + pin
}

// TestRunFlavorStage_Positive_PinnedFlavorReachesTheGate: gate run has no --flavor, so the
// manifest pin is the only way to name the flavor. Without it the stage fails nothing-matched.
func TestRunFlavorStage_Positive_PinnedFlavorReachesTheGate(t *testing.T) {
	unpinned := goLibraryRepo(t, map[string]string{".standards.yaml": appServiceWithPin("")})
	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: unpinned})
	if !errors.Is(err, flavor.ErrNoFlavorMatched) || !strings.Contains(err.Error(), "flavors entry in .standards.yaml") {
		t.Fatalf("unpinned err = %v; want nothing-matched naming the pin setting", err)
	}
	pinned := goLibraryRepo(t, map[string]string{".standards.yaml": appServiceWithPin("flavors:\n  - name: go-library\n")})
	if msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: pinned}); err != nil || msg != "" {
		t.Fatalf("a pinned conforming repository must pass the stage, got %q, %v", msg, err)
	}
}

// TestRunFlavorStage_Negative_UnknownPinFailsTheGate: a pin naming no flavor is a failure, not
// a skip and not a fall back to detection.
func TestRunFlavorStage_Negative_UnknownPinFailsTheGate(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{".standards.yaml": appServiceWithPin("flavors:\n  - name: no-such-flavor\n")})
	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err == nil || !strings.Contains(err.Error(), "no-such-flavor") {
		t.Fatalf("err = %v; want a failure naming the unknown flavor", err)
	}
	if skip, skipped := errors.AsType[*stageSkip](err); skipped {
		t.Fatalf("an unknown pin was skipped as %s: %s", skip.status, skip.reason)
	}
}

// TestRunFlavorStage_Boundary_ScopedPinNamesTheFailingComponent: a pin scoped to a directory
// audits that directory only and the verdict names it.
func TestRunFlavorStage_Boundary_ScopedPinNamesTheFailingComponent(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{
		".standards.yaml": appServiceWithPin("flavors:\n  - name: go-library\n    path: svc\n"),
		"svc/go.mod":      "module example.com/svc\n",
	})
	_, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo})
	if err == nil || !strings.Contains(err.Error(), "for go-library at svc") {
		t.Fatalf("err = %v; want the failing component named", err)
	}
}

// TestRunFlavorStage_Positive_ConformingScopedPinPasses: a conforming repository with the pin
// scoped to svc/ passes the stage. The repository-level files (workflow, lock, ruleset, hooks)
// are read at the root and only the stack template below svc/ (#1103).
func TestRunFlavorStage_Positive_ConformingScopedPinPasses(t *testing.T) {
	repo := goLibraryRepo(t, map[string]string{
		".standards.yaml":   appServiceWithPin("flavors:\n  - name: go-library\n    path: svc\n"),
		"svc/go.mod":        "module example.com/svc\n",
		"svc/.golangci.yml": "version: \"2\"\n",
	})
	if msg, err := runFlavorStage(context.Background(), &stageConfig{repoDir: repo}); err != nil || msg != "" {
		t.Fatalf("a conforming scoped pin must pass the stage, got %q, %v", msg, err)
	}
}
