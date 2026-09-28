package flavor_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// torchProject is a pyproject.toml declaring a PyTorch dependency, which python-ml claims.
const torchProject = "[project]\ndependencies = [\"torch>=2.0\"]\n"

// TestResolve_Positive_DeclaredProfileDrivesTheFlavor is BUG-940's measured case: a native GPU
// engine with a PyTorch verification suite. python-ml precedes native-gpu-systems in the catalog,
// so detection across the whole catalog named python-ml, and flavor apply scaffolded it although
// the repository declares native-gpu-systems.
func TestResolve_Positive_DeclaredProfileDrivesTheFlavor(t *testing.T) {
	repo := declaringRepo(t, "native-gpu-systems", map[string]string{
		"CMakeLists.txt": "project(engine)\n", "pyproject.toml": torchProject,
	})
	if got, err := flavor.Resolve(repo); err != nil || got != "native-gpu-systems" {
		t.Fatalf("Resolve = %q, %v; want the declared profile's native-gpu-systems flavor", got, err)
	}
	report, err := flavor.ApplyFlavor(t.Context(), repo, "auto", false)
	if report == nil || report.Flavor != "native-gpu-systems" {
		t.Fatalf("apply scaffolded %+v (err %v); want native-gpu-systems", report, err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "ruff.toml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("python-ml's ruff.toml was scaffolded into a native-gpu-systems repository: %v", statErr)
	}
	// The declaration also outranks the markers: this tree classifies as framework by its go.mod,
	// and the repository declares app-service, so its Node stack names the flavor.
	web := declaringRepo(t, "app-service", map[string]string{
		"go.mod": "module x\n", "cmd/tool/main.go": "package main\n", "package.json": `{"name": "web"}`,
	})
	if got, err := flavor.Resolve(web); err != nil || got != "typescript-node" {
		t.Fatalf("Resolve = %q, %v; the declared app-service must outrank the go.mod", got, err)
	}
}

// TestResolve_Positive_UndeclaredRepositoryFollowsItsClassification covers a repository with no
// manifest: the profile its markers classify decides the flavor, which is the profile adoption
// then records. A Go service whose package.json only holds commit tooling classifies as framework,
// so it resolves to go-service, not to typescript-node.
func TestResolve_Positive_UndeclaredRepositoryFollowsItsClassification(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"go service with commit tooling": {files: goRepoWithCommitTooling(), want: "go-service"},
		"gpu engine with torch suite":    {files: map[string]string{"meson.build": "project('e')\n", "pyproject.toml": torchProject}, want: "native-gpu-systems"},
		"torch pipeline":                 {files: map[string]string{"pyproject.toml": torchProject}, want: "python-ml"},
	}
	for name, tc := range cases {
		if got, err := flavor.Resolve(repoWithFiles(t, tc.files)); err != nil || got != tc.want {
			t.Errorf("%s: Resolve = %q, %v; want %q", name, got, err, tc.want)
		}
	}
}

// TestResolve_Negative_DeclaredProfileWithoutFlavorIsNotApplicable: a repository declaring
// gitops-infra gets no python-ml templates for its PyTorch dependency, from apply or from the
// audit, and the refusal is not-applicable rather than nothing-matched.
func TestResolve_Negative_DeclaredProfileWithoutFlavorIsNotApplicable(t *testing.T) {
	repo := declaringRepo(t, "gitops-infra", map[string]string{"pyproject.toml": torchProject})
	_, err := flavor.Resolve(repo)
	if !errors.Is(err, flavor.ErrFlavorNotApplicable) || errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("Resolve err = %v; want ErrFlavorNotApplicable alone", err)
	}
	report, err := flavor.ApplyFlavor(t.Context(), repo, "auto", false)
	if !errors.Is(err, flavor.ErrFlavorNotApplicable) || report != nil {
		t.Fatalf("apply = %+v, %v; want a not-applicable refusal and no report", report, err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "ruff.toml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused apply wrote ruff.toml: %v", statErr)
	}
}

// TestResolve_Boundary_ProfileEdges pins the edges of ResolveForProfile: no profile, a profile
// whose flavors all fail to match, surrounding whitespace, and an explicit flavor still winning.
func TestResolve_Boundary_ProfileEdges(t *testing.T) {
	goLib := repoWithFiles(t, map[string]string{"go.mod": "module x\n", "internal/doc.go": "package internal\n"})
	if _, err := flavor.ResolveForProfile(goLib, ""); !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Errorf("an empty profile must match nothing, got %v", err)
	}
	// os-image has a flavor, but its markers are absent; the go.mod must not pull in a Go flavor.
	_, err := flavor.ResolveForProfile(goLib, "os-image")
	if !errors.Is(err, flavor.ErrNoFlavorMatched) || errors.Is(err, flavor.ErrFlavorNotApplicable) {
		t.Errorf("an unmatched profile with flavors must be nothing-matched, got %v", err)
	}
	if got, err := flavor.ResolveForProfile(goLib, " framework "); err != nil || got != "go-library" {
		t.Errorf("a padded profile must resolve like the bare one, got %q, %v", got, err)
	}
	declared := declaringRepo(t, "gitops-infra", map[string]string{"go.mod": "module x\n", "internal/doc.go": "package internal\n"})
	if report, err := flavor.ApplyFlavor(t.Context(), declared, "go-library", false); err != nil || report.Flavor != "go-library" {
		t.Errorf("an explicit flavor must override the declaration: %+v, %v", report, err)
	}
}
