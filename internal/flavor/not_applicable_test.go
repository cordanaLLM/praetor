package flavor_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// TestIsNotApplicable covers #1111's one decision: nothing matched and no flavor at all are the
// skip; a nil error, an unrelated error and a pin refusal are not.
func TestIsNotApplicable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nothing matched", fmt.Errorf("w: %w", flavor.ErrNoFlavorMatched), true},
		{"profile without flavors", flavor.ErrFlavorNotApplicable, true},
		{"nil", nil, false},
		{"unrelated", errors.New("read failed"), false},
		{"pin not scaffoldable", flavor.ErrPinNotScaffoldable, false},
		{"no profile classifies", fmt.Errorf("w: %w: %w", flavor.ErrNoProfile, flavor.ErrNoFlavorMatched), false},
	}
	for _, c := range cases {
		if got := flavor.IsNotApplicable(c.err); got != c.want {
			t.Errorf("%s: IsNotApplicable = %v; want %v", c.name, got, c.want)
		}
	}
}

// TestResolveTargets_Positive_NoMarkersIsNotApplicableUnpinned: a native-gpu-systems repository
// without meson, CMake or Rust markers resolves to the skip, and the same repository pinned
// resolves to its pin (the pin wins).
func TestResolveTargets_Positive_NoMarkersIsNotApplicableUnpinned(t *testing.T) {
	files := map[string]string{"patches/0001.patch": "--- a\n+++ b\n", "tools/build.sh": "#!/bin/sh\n"}
	repo := declaringRepo(t, "native-gpu-systems", files)
	if _, err := flavor.ResolveTargets(repo); !flavor.IsNotApplicable(err) {
		t.Fatalf("err = %v; want the not-applicable decision", err)
	}
	files[".standards.yaml"] = manifestWithPins("native-gpu-systems", "flavors:\n  - name: native-gpu-systems\n")
	targets, err := flavor.ResolveTargets(repoWithFiles(t, files))
	if err != nil || len(targets) != 1 || targets[0].Flavor != "native-gpu-systems" {
		t.Fatalf("targets = %+v, %v; want the pin to win over the skip", targets, err)
	}
}

// TestSingleRootFlavor_Negative_ScopedPinRemedyListsRootAndPathFiles: a scoped pin gets no
// scaffold, and the refusal says which files belong at the root and which under the path.
func TestSingleRootFlavor_Negative_ScopedPinRemedyListsRootAndPathFiles(t *testing.T) {
	_, err := flavor.SingleRootFlavor([]flavor.Target{{Flavor: "go-service", Path: "api"}})
	if !errors.Is(err, flavor.ErrPinNotScaffoldable) {
		t.Fatalf("err = %v; want ErrPinNotScaffoldable", err)
	}
	for _, want := range []string{"get no scaffold", ".gitleaks.toml", ".paperclip/", ".github/", "repository root", "each pinned path", "go.mod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the remedy lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "--flavor") {
		t.Errorf("the remedy must not suggest --flavor: %v", err)
	}
	if _, err := flavor.SingleRootFlavor([]flavor.Target{{Flavor: "go-service", Path: "."}}); err != nil {
		t.Errorf("a root pin must scaffold: %v", err)
	}
}

// TestAuditTargets_Boundary_ScopedPinReadsGitleaksAtTheRoot: gitleaks reads the root
// configuration, so a pin scoped to src/ finds .gitleaks.toml at the root and not below.
func TestAuditTargets_Boundary_ScopedPinReadsGitleaksAtTheRoot(t *testing.T) {
	pins := manifestWithPins("native-gpu-systems", "flavors:\n  - name: native-gpu-systems\n    path: src\n")
	const gitleaks = "[extend]\nuseDefault = true\n"
	hasGitleaksMissing := func(files map[string]string) bool {
		reports, err := flavor.AuditTargetsContext(t.Context(), repoWithFiles(t, files))
		if err != nil || len(reports) != 1 {
			t.Fatalf("audit = %+v, %v", reports, err)
		}
		for _, m := range reports[0].MissingTemplates {
			if m.Path == ".gitleaks.toml" {
				return true
			}
		}
		return false
	}
	if hasGitleaksMissing(map[string]string{".standards.yaml": pins, "src/main.c": "int main(){}\n", ".gitleaks.toml": gitleaks}) {
		t.Error("a root .gitleaks.toml was not read for a scoped pin")
	}
	if !hasGitleaksMissing(map[string]string{".standards.yaml": pins, "src/main.c": "int main(){}\n", "src/.gitleaks.toml": gitleaks}) {
		t.Error("a .gitleaks.toml below the pin path satisfied the root requirement")
	}
}

// TestResolveTargets_Negative_NoProfileIsNotASkip: a missing directory and an empty one have no
// profile, so the audit keeps refusing them (fail closed); only a classified profile with no
// matching flavor is the skip.
func TestResolveTargets_Negative_NoProfileIsNotASkip(t *testing.T) {
	for name, dir := range map[string]string{"missing": "/nonexistent/path", "empty": t.TempDir()} {
		_, err := flavor.ResolveTargets(dir)
		if err == nil || flavor.IsNotApplicable(err) || !errors.Is(err, flavor.ErrNoProfile) {
			t.Errorf("%s: err = %v; want a refusal wrapping ErrNoProfile, not the skip", name, err)
		}
		if !errors.Is(err, flavor.ErrNoFlavorMatched) {
			t.Errorf("%s: err = %v; must still wrap ErrNoFlavorMatched for nothing-matched callers", name, err)
		}
	}
}

// TestIsScaffoldSkip: skips are the audit's decision, no profile and an unscaffoldable pin; a
// bad pin and nil are not.
func TestIsScaffoldSkip(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"not applicable": {flavor.ErrFlavorNotApplicable, true},
		"no profile":     {flavor.ErrNoProfile, true},
		"scoped pin":     {flavor.ErrPinNotScaffoldable, true},
		"bad pin":        {errors.New("flavors[0] in .standards.yaml: unknown flavor"), false},
		"nil":            {nil, false},
	}
	for name, c := range cases {
		if got := flavor.IsScaffoldSkip(c.err); got != c.want {
			t.Errorf("%s: IsScaffoldSkip = %v; want %v", name, got, c.want)
		}
	}
}
