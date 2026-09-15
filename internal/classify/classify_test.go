package classify

import (
	"os"
	"path/filepath"
	"testing"
)

// repoWith builds a working tree containing exactly the given markers.
func repoWith(t *testing.T, markers ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, marker := range markers {
		full := filepath.Join(dir, filepath.FromSlash(marker))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", marker, err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", marker, err)
		}
	}
	return dir
}

// TestByMarkers_Positive_EveryMarkerResolves pins the whole table, one marker at a time.
//
// This matrix is the evidence the reconciliation was built on. Measured before the change, the
// adoption table and the flavor path produced different answers for requirements.txt,
// Dockerfile, Chart.yaml, kustomization.yaml and harness.json, and agreed on the other nine.
// Pinning every row means a future edit that reintroduces a hole fails here rather than in
// whichever downstream repository adopts next.
func TestByMarkers_Positive_EveryMarkerResolves(t *testing.T) {
	cases := map[string]string{
		"harness.json":       "framework",
		"Chart.yaml":         "container-image",
		"kustomization.yaml": "container-image",
		"helmfile.yaml":      "container-image",
		"meson.build":        "native-gpu-systems",
		"core/meson.build":   "native-gpu-systems",
		"CMakeLists.txt":     "native-gpu-systems",
		"Cargo.toml":         "native-gpu-systems",
		"go.mod":             "framework",
		"pubspec.yaml":       "app-service",
		"pom.xml":            "app-service",
		"build.gradle":       "app-service",
		"build.gradle.kts":   "app-service",
		"package.json":       "app-service",
		"pyproject.toml":     "app-service",
		"requirements.txt":   "app-service",
		"Dockerfile":         "container-image",
	}
	for marker, want := range cases {
		got := ByMarkers(repoWith(t, marker))
		if got.Archetype != want || got.Source != SourceMarkers {
			t.Errorf("ByMarkers(%s) = %q from %q, want %q from %q", marker, got.Archetype, got.Source, want, SourceMarkers)
		}
	}
}

// TestByMarkers_Negative_NothingMatchesSaysSo is the defect this package exists to remove. A
// repository the table does not recognise must report that it was not recognised, rather than
// returning a plausible archetype that a caller will act on.
func TestByMarkers_Negative_NothingMatchesSaysSo(t *testing.T) {
	for name, repo := range map[string]string{
		"empty tree":     repoWith(t),
		"unknown marker": repoWith(t, "Rakefile"),
		"absent path":    "",
	} {
		got := ByMarkers(repo)
		if got.Matched() {
			t.Errorf("%s: got %q from %q, want no match", name, got.Archetype, got.Source)
		}
		if got.Or(FallbackArchetype) != FallbackArchetype {
			t.Errorf("%s: an unmatched result must fall back only when the caller asks", name)
		}
	}
}

// TestByMarkers_Boundary_MostSpecificMarkerWins covers the one thing the two tables genuinely
// disagreed about: which rule wins when several markers are present.
func TestByMarkers_Boundary_MostSpecificMarkerWins(t *testing.T) {
	cases := []struct {
		name    string
		markers []string
		want    string
	}{
		// The shape measured on cordanaLLM/imago, where the two tables split.
		{"go and python", []string{"go.mod", "pyproject.toml"}, "framework"},
		// A Dockerfile says only that the repository ships in a container.
		{"go and docker", []string{"go.mod", "Dockerfile"}, "framework"},
		{"python and docker", []string{"pyproject.toml", "Dockerfile"}, "app-service"},
		// A Helm chart says what the repository is for, even next to a build system.
		{"chart and go", []string{"Chart.yaml", "go.mod"}, "container-image"},
		// An agent harness outranks everything.
		{"harness and chart", []string{"harness.json", "Chart.yaml"}, "framework"},
		// Dockerfile alone still classifies; it is last, not ignored.
		{"docker alone", []string{"Dockerfile"}, "container-image"},
	}
	for _, tc := range cases {
		if got := ByMarkers(repoWith(t, tc.markers...)); got.Archetype != tc.want {
			t.Errorf("%s: ByMarkers(%v) = %q, want %q", tc.name, tc.markers, got.Archetype, tc.want)
		}
	}
}

// TestByMetadata_Positive_LanguageAndKeywords covers the path used where there is no working
// tree, only a remote repository's language and description.
func TestByMetadata_Positive_LanguageAndKeywords(t *testing.T) {
	cases := []struct {
		lang, desc, want string
	}{
		{"C", "A high-performance Vulkan GPU pre-encode filter engine", "native-gpu-systems"},
		{"C++", "CUDA kernels for image processing", "native-gpu-systems"},
		{"Python", "Kubernetes cluster GitOps deployment with ArgoCD", "gitops-infra"},
		{"Go", "Composable Go framework with opt-in fx modules", "framework"},
		{"ASTRO", "", "pages-site"},
		{"Go", "", "app-service"},
	}
	for _, tc := range cases {
		if got := ByMetadata(tc.lang, tc.desc); got.Archetype != tc.want {
			t.Errorf("ByMetadata(%q, %q) = %q, want %q", tc.lang, tc.desc, got.Archetype, tc.want)
		}
	}
}

// TestByMetadata_Negative_LinuxKernelIsNotAGPUEngine pins the measured defect by name.
//
// cordanaLLM/nucleus builds the Linux kernel. The old table matched the bare substring "kernel"
// to native-gpu-systems, so a kernel build forge was classified as a GPU compute engine. The word
// is gone; a real GPU repository still matches on its own terms, which the positive test checks.
func TestByMetadata_Negative_LinuxKernelIsNotAGPUEngine(t *testing.T) {
	got := ByMetadata("Python",
		"Deterministic Linux kernel compilation forge, hardened kconfig fragments, and deb/UKI packaging.")
	if got.Archetype == "native-gpu-systems" {
		t.Error("a Linux kernel build forge must not classify as a GPU compute engine")
	}
}

// TestByMetadata_Boundary_WholeWordsOnly covers the matching rule itself. Substring matching is
// what made "kernel" fire inside unrelated words, so a keyword must not match as a fragment.
func TestByMetadata_Boundary_WholeWordsOnly(t *testing.T) {
	if got := ByMetadata("COBOL", "a barracuda fish database"); got.Matched() {
		t.Errorf("\"cuda\" inside \"barracuda\" must not match, got %q", got.Archetype)
	}
	if got := ByMetadata("COBOL", "accelerated with CUDA."); got.Archetype != "native-gpu-systems" {
		t.Errorf("a trailing full stop must not stop the word matching, got %q", got.Archetype)
	}
	if got := ByMetadata("COBOL", "legacy batch processing"); got.Matched() {
		t.Errorf("an unknown language with no keywords must not match, got %q", got.Archetype)
	}
}

// TestResolve_Positive_DeclarationOutranksEveryGuess is the precedence the engine was missing.
func TestResolve_Positive_DeclarationOutranksEveryGuess(t *testing.T) {
	repo := repoWith(t, "go.mod")
	got := Resolve(
		FromDeclaration([]string{"os-image"}),
		Explicit("framework"),
		ByMarkers(repo),
		ByMetadata("Go", ""),
	)
	if got.Archetype != "os-image" || got.Source != SourceDeclared {
		t.Errorf("declaration must win, got %q from %q", got.Archetype, got.Source)
	}
}

// TestResolve_Boundary_FallsThroughInOrder checks each rung of the chain in turn.
func TestResolve_Boundary_FallsThroughInOrder(t *testing.T) {
	repo := repoWith(t, "go.mod")
	cases := []struct {
		name       string
		candidates []Result
		want       Source
	}{
		{"explicit beats markers", []Result{FromDeclaration(nil), Explicit("app-service"), ByMarkers(repo)}, SourceExplicit},
		{"markers beat metadata", []Result{FromDeclaration(nil), Explicit(""), ByMarkers(repo), ByMetadata("Go", "")}, SourceMarkers},
		{"metadata is last", []Result{ByMarkers(repoWith(t)), ByMetadata("Go", "")}, SourceMetadata},
		{"nothing at all", []Result{ByMarkers(repoWith(t)), ByMetadata("", "")}, SourceNone},
		{"no candidates", nil, SourceNone},
	}
	for _, tc := range cases {
		if got := Resolve(tc.candidates...); got.Source != tc.want {
			t.Errorf("%s: got source %q, want %q", tc.name, got.Source, tc.want)
		}
	}
}

// TestFromDeclaration_Boundary_BlankProfilesAreNotADeclaration keeps whitespace from reading as
// a declared profile, which would outrank every real piece of evidence.
func TestFromDeclaration_Boundary_BlankProfilesAreNotADeclaration(t *testing.T) {
	for _, profiles := range [][]string{nil, {}, {""}, {"   "}, {"  ", "\t"}} {
		if got := FromDeclaration(profiles); got.Matched() {
			t.Errorf("FromDeclaration(%q) matched %q", profiles, got.Archetype)
		}
	}
	if got := FromDeclaration([]string{"  ", "os-image"}); got.Archetype != "os-image" {
		t.Errorf("a blank entry must not hide a real declaration, got %q", got.Archetype)
	}
}

// TestArchetypeFor_Negative_UnknownMarker keeps the lookup honest for callers checking their own
// detection against this table.
func TestArchetypeFor_Negative_UnknownMarker(t *testing.T) {
	if _, ok := ArchetypeFor("Rakefile"); ok {
		t.Error("an unknown marker must not report a mapping")
	}
	if got, ok := ArchetypeFor("go.mod"); !ok || got != "framework" {
		t.Errorf("ArchetypeFor(go.mod) = %q, %v", got, ok)
	}
}
