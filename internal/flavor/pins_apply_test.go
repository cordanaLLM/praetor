package flavor_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// pinnedGoModule is a repository the markers read as go-service, declaring app-service.
func pinnedGoModule(t *testing.T, pins string) string {
	t.Helper()
	repo := declaringRepo(t, "app-service", goServiceFiles())
	writeManifest(t, repo, manifestWithPins("app-service", pins))
	return repo
}

// applyPlan plans an auto apply, which writes nothing.
func applyPlan(t *testing.T, repo string) (*flavor.ApplyReport, error) {
	t.Helper()
	return flavor.ApplyFlavorWith(t.Context(), repo, "auto", flavor.ApplyOptions{TemplatesOnly: true, DryRun: true})
}

// TestApply_Positive_AutoFollowsTheRootPin: go.mod plus cmd/ detect as go-service, and the pin
// says go-library. Apply used to scaffold go-service while the audit measured go-library.
func TestApply_Positive_AutoFollowsTheRootPin(t *testing.T) {
	repo := pinnedGoModule(t, "flavors:\n  - name: go-library\n")
	report, err := applyPlan(t, repo)
	if err != nil || report.Flavor != "go-library" {
		t.Fatalf("apply planned %v, %v; want go-library from the pin", report, err)
	}
	reports, err := flavor.AuditTargetsContext(t.Context(), repo)
	if err != nil || reports[0].Flavor != report.Flavor {
		t.Fatalf("audit measured %+v, %v; apply and audit must agree", reports, err)
	}
}

// TestApply_Negative_AutoRefusesPinsItCannotScaffold: a directory-scoped pin or two pins name no
// one root flavor, so an auto apply is refused and an explicit --flavor still works.
func TestApply_Negative_AutoRefusesPinsItCannotScaffold(t *testing.T) {
	for name, pins := range map[string]string{
		"scoped":   "flavors:\n  - name: go-service\n    path: cmd\n",
		"multiple": "flavors:\n  - name: go-service\n  - name: go-library\n    path: cmd\n",
	} {
		repo := pinnedGoModule(t, pins)
		if _, err := applyPlan(t, repo); !errors.Is(err, flavor.ErrPinNotScaffoldable) {
			t.Errorf("%s: err = %v; want ErrPinNotScaffoldable", name, err)
		}
		report, err := flavor.ApplyFlavorWith(t.Context(), repo, "go-service", flavor.ApplyOptions{TemplatesOnly: true, DryRun: true})
		if err != nil || report.Flavor != "go-service" {
			t.Errorf("%s: explicit flavor = %v, %v; want it honoured", name, report, err)
		}
	}
}

// TestSingleRootFlavor_Boundary pins the edges: no target, one root target, one scoped target.
func TestSingleRootFlavor_Boundary(t *testing.T) {
	if _, err := flavor.SingleRootFlavor(nil); !errors.Is(err, flavor.ErrPinNotScaffoldable) {
		t.Errorf("no targets: err = %v", err)
	}
	if got, err := flavor.SingleRootFlavor([]flavor.Target{{Flavor: "go-library", Path: "."}}); err != nil || got != "go-library" {
		t.Errorf("one root target = %q, %v", got, err)
	}
	if _, err := flavor.SingleRootFlavor([]flavor.Target{{Flavor: "go-library", Path: "api"}}); !errors.Is(err, flavor.ErrPinNotScaffoldable) {
		t.Errorf("one scoped target: err = %v", err)
	}
}

// TestPlannedWorkflows_PinDecidesTheFlavor: the planned workflows follow the flavor an apply
// scaffolds, so a root pin lists go-library's workflow only and a scoped pin lists none.
func TestPlannedWorkflows_PinDecidesTheFlavor(t *testing.T) {
	root := pinnedGoModule(t, "flavors:\n  - name: go-library\n")
	planned, err := flavor.PlannedWorkflows(t.Context(), root, "app-service")
	if err != nil || len(planned) != 1 || planned[0].Path != ".github/workflows/ci.yml" {
		t.Fatalf("root pin planned %+v, %v; want go-library's ci.yml only", planned, err)
	}
	scoped := pinnedGoModule(t, "flavors:\n  - name: go-service\n    path: cmd\n")
	if planned, err := flavor.PlannedWorkflows(t.Context(), scoped, "app-service"); err != nil || len(planned) != 0 {
		t.Fatalf("scoped pin planned %+v, %v; want none", planned, err)
	}
	if _, err := os.Stat(filepath.Join(scoped, ".github")); err == nil {
		t.Error("planning wrote files")
	}
}

// TestResolveTargetsForProfile_PinBeatsProfile: the profile-driven resolver adoption uses honours
// the pin first and falls back to the profile's flavor without one.
func TestResolveTargetsForProfile_PinBeatsProfile(t *testing.T) {
	pinned := pinnedGoModule(t, "flavors:\n  - name: go-library\n")
	if targets, err := flavor.ResolveTargetsForProfile(pinned, "app-service"); err != nil || targets[0].Flavor != "go-library" {
		t.Fatalf("pinned = %+v, %v", targets, err)
	}
	free := declaringRepo(t, "app-service", goServiceFiles())
	if targets, err := flavor.ResolveTargetsForProfile(free, "app-service"); err != nil || targets[0].Flavor != "go-service" {
		t.Fatalf("unpinned = %+v, %v", targets, err)
	}
	if _, err := flavor.ResolveTargetsForProfile(free, ""); !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("empty profile err = %v; want ErrNoFlavorMatched", err)
	}
}
