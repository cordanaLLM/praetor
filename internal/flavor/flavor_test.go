package flavor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

func TestDetectFlavor_Positive(t *testing.T) {
	tmp := t.TempDir()

	// 1. Go service detection
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test/svc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "cmd", "svc"), 0755); err != nil {
		t.Fatal(err)
	}
	detected := flavor.DetectFlavor(tmp)
	if detected != "go-service" {
		t.Fatalf("expected go-service, got %s", detected)
	}

	// 2. Svelte detection
	tmpSvelte := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpSvelte, "package.json"), []byte("{\"name\": \"test\"}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpSvelte, "svelte.config.js"), []byte("// config"), 0644); err != nil {
		t.Fatal(err)
	}
	detectedSvelte := flavor.DetectFlavor(tmpSvelte)
	if detectedSvelte != "frontend-svelte" {
		t.Fatalf("expected frontend-svelte, got %s", detectedSvelte)
	}
}

func TestAuditFlavor_PositiveAndScoring(t *testing.T) {
	tmp := t.TempDir()
	// Scaffold minimal files for go-library
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test/lib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "internal"), 0755); err != nil {
		t.Fatal(err)
	}

	report, err := flavor.AuditFlavor(tmp, "go-library")
	if err != nil {
		t.Fatalf("audit flavor failed: %v", err)
	}
	if report.Flavor != "go-library" {
		t.Fatalf("expected flavor go-library, got %s", report.Flavor)
	}
	if report.TemplatesTotal == 0 {
		t.Fatal("expected non-zero required templates")
	}
	// The fixture carries none of the required templates or settings, so the score is
	// exactly 0. The assertion used to be 0 <= Score <= 100, which present/total*100
	// cannot violate: it passed for every possible formula.
	if report.Score != 0.0 {
		t.Fatalf("a repository carrying no required file scores 0.0, got %f", report.Score)
	}
	if report.Passed {
		t.Fatalf("a repository missing every template must not pass: %+v", report)
	}
}

func TestAuditFlavor_Negative_UnknownFlavor(t *testing.T) {
	tmp := t.TempDir()
	_, err := flavor.AuditFlavor(tmp, "non-existent-flavor-xyz")
	if err == nil {
		t.Fatal("expected error for unknown flavor, got nil")
	}
}

func TestApplyFlavor_PositiveAndBoundary(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()

	// 1. Initial apply
	report, err := flavor.ApplyFlavor(ctx, tmp, "go-service", false)
	if err != nil {
		t.Fatalf("apply flavor failed: %v", err)
	}
	if len(report.CreatedTemplates) == 0 {
		t.Fatal("expected created templates")
	}
	if !report.WorkingDirCreated {
		t.Fatal("expected workingdir to be created")
	}

	// Verify .workingdir/STATE.md exists
	if _, err := os.Stat(filepath.Join(tmp, ".workingdir", "STATE.md")); err != nil {
		t.Fatalf("STATE.md not created: %v", err)
	}

	// 2. Second apply with force=false (boundary: should skip existing)
	report2, err := flavor.ApplyFlavor(ctx, tmp, "go-service", false)
	if err != nil {
		t.Fatalf("apply flavor second run failed: %v", err)
	}
	if len(report2.SkippedTemplates) == 0 {
		t.Fatal("expected skipped templates on non-force re-apply")
	}

	// 3. Third apply with force=true (boundary: should overwrite/re-create)
	report3, err := flavor.ApplyFlavor(ctx, tmp, "go-service", true)
	if err != nil {
		t.Fatalf("apply flavor force run failed: %v", err)
	}
	if len(report3.CreatedTemplates) == 0 {
		t.Fatal("expected recreated templates on force re-apply")
	}
}

func TestApplyFlavor_Negative_UnknownFlavor(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()
	_, err := flavor.ApplyFlavor(ctx, tmp, "non-existent-flavor-xyz", false)
	if err == nil {
		t.Fatal("expected error for unknown flavor, got nil")
	}
}

// TestAuditFlavor_Boundary_EmptyDir asserts that a repository matching nothing is refused rather
// than audited against a guess.
//
// This test previously required the opposite: that an empty directory audits successfully. It
// did, by falling through to go-library, and then scored the directory against the templates,
// settings and toolchains of a flavor that described nothing about it. That is the defect, so
// the expectation is inverted rather than the behaviour preserved.
func TestAuditFlavor_Boundary_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	report, err := flavor.AuditFlavor(tmp, "auto")
	if !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("an unmatched repository must be refused, got report=%v err=%v", report, err)
	}
	if report != nil {
		t.Errorf("a refused audit must not also return a report, got %v", report)
	}
	// An explicit flavor still audits, so the refusal is recoverable rather than a dead end.
	explicit, err := flavor.AuditFlavor(tmp, "go-library")
	if err != nil {
		t.Fatalf("an explicit flavor must still audit: %v", err)
	}
	if explicit.Score != 0.0 {
		t.Fatalf("an empty directory carries none of the 9 required files, so the score is 0.0, got %f", explicit.Score)
	}
	if explicit.SettingsValid != 0 || explicit.SettingsTotal != 2 {
		t.Fatalf("expected 0 of 2 go-library settings, got %d of %d", explicit.SettingsValid, explicit.SettingsTotal)
	}
}

func TestDetectFlavor_ExpandedArchetypes(t *testing.T) {
	// 1. Rust systems detection
	tmpRust := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpRust, "Cargo.toml"), []byte("[package]\nname = \"rg\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := flavor.DetectFlavor(tmpRust); got != "rust-systems" {
		t.Fatalf("expected rust-systems, got %s", got)
	}

	// 2. TypeScript Node detection
	tmpTS := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpTS, "package.json"), []byte("{\"name\": \"svc\"}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpTS, "tsconfig.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := flavor.DetectFlavor(tmpTS); got != "typescript-node" {
		t.Fatalf("expected typescript-node, got %s", got)
	}

	// 3. JVM Service detection
	tmpJVM := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpJVM, "pom.xml"), []byte("<project></project>"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := flavor.DetectFlavor(tmpJVM); got != "jvm-service" {
		t.Fatalf("expected jvm-service, got %s", got)
	}

	// 4. Mobile Flutter detection
	tmpFlutter := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpFlutter, "pubspec.yaml"), []byte("name: app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := flavor.DetectFlavor(tmpFlutter); got != "mobile-flutter" {
		t.Fatalf("expected mobile-flutter, got %s", got)
	}
}

func TestListFlavors_CompleteRegistry(t *testing.T) {
	flavors := flavor.List()
	if len(flavors) < 11 {
		t.Fatalf("expected at least 11 registered flavors, got %d", len(flavors))
	}

	expected := map[string]bool{
		"go-service":         false,
		"go-library":         false,
		"native-gpu-systems": false,
		"frontend-svelte":    false,
		"python-ml":          false,
		"infra-k8s":          false,
		"agentic-autonomous": false,
		"rust-systems":       false,
		"typescript-node":    false,
		"jvm-service":        false,
		"mobile-flutter":     false,
	}

	for _, f := range flavors {
		expected[f.Name()] = true
		if f.Description() == "" {
			t.Errorf("flavor %s has empty description", f.Name())
		}
		if f.HISSProfile() == "" {
			t.Errorf("flavor %s has empty HISS profile", f.Name())
		}
		// A flavor may rest on settings alone (infra-k8s does), but one requiring no file
		// at all would pass every repository and check nothing.
		if len(f.RequiredTemplates())+len(f.RequiredSettings()) == 0 {
			t.Errorf("flavor %s requires no template and no setting", f.Name())
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected flavor %s to be registered", name)
		}
	}
}

func TestApplyFlavor_NewArchetypes(t *testing.T) {
	ctx := context.Background()
	targets := []struct {
		flavorName   string
		expectedFile string
	}{
		{"rust-systems", "rustfmt.toml"},
		{"typescript-node", "tsconfig.json"},
		{"jvm-service", "checkstyle.xml"},
		{"mobile-flutter", "analysis_options.yaml"},
	}

	for _, tc := range targets {
		tmp := t.TempDir()
		rep, err := flavor.ApplyFlavor(ctx, tmp, tc.flavorName, false)
		if err != nil {
			t.Fatalf("failed applying flavor %s: %v", tc.flavorName, err)
		}
		if rep.Flavor != tc.flavorName {
			t.Fatalf("expected report flavor %s, got %s", tc.flavorName, rep.Flavor)
		}
		targetPath := filepath.Join(tmp, tc.expectedFile)
		if _, err := os.Stat(targetPath); err != nil {
			t.Fatalf("expected template %s to be created for flavor %s: %v", tc.expectedFile, tc.flavorName, err)
		}
	}
}

// =========================================================================
// Detection honesty (BUG-935, BUG-938, BUG-939, BUG-946)
// =========================================================================

// repoWithFiles builds a repository containing exactly the named files.
func repoWithFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestDetect_Positive_PythonMLRequiresAnMLDependency pins the corrected predicate. python-ml
// must describe a machine-learning pipeline, not every repository that contains Python.
func TestDetect_Positive_PythonMLRequiresAnMLDependency(t *testing.T) {
	ml := repoWithFiles(t, map[string]string{"pyproject.toml": "[project]\ndependencies = [\"torch>=2.0\"]\n"})
	if got, ok := flavor.Detect(ml); !ok || got != "python-ml" {
		t.Errorf("a declared torch dependency must detect python-ml, got %q ok=%v", got, ok)
	}
	req := repoWithFiles(t, map[string]string{"requirements.txt": "openvino==2025.1\n"})
	if got, ok := flavor.Detect(req); !ok || got != "python-ml" {
		t.Errorf("requirements.txt naming openvino must detect python-ml, got %q ok=%v", got, ok)
	}
}

// TestDetect_Negative_PlainPythonIsNotAnMLPipeline is the measured defect. cordanaLLM/nucleus is
// a Linux kernel build forge and cordanaLLM/imago an OS image forge; both carried a
// pyproject.toml and both were reported as PyTorch/OpenVINO pipelines, then audited against ML
// tooling they had no reason to install.
func TestDetect_Negative_PlainPythonIsNotAnMLPipeline(t *testing.T) {
	plain := repoWithFiles(t, map[string]string{
		"pyproject.toml":   "[project]\nname = \"kernel-forge\"\ndependencies = [\"click\", \"pyyaml\"]\n",
		"requirements.txt": "pytest\nruff\n",
	})
	if got, ok := flavor.Detect(plain); ok {
		t.Errorf("a Python project with no ML dependency must not detect python-ml, got %q", got)
	}
}

// TestDetect_Negative_AdoptionArtifactsDoNotDecideAFlavor covers both markers that adoption
// itself writes. A flavor detected from the governance tool's own output describes the tool, not
// the repository, and agentic-autonomous had no other evidence.
func TestDetect_Negative_AdoptionArtifactsDoNotDecideAFlavor(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"scaffolded agents dir":     {".agents/agents/repo-auditor.md": "# auditor\n"},
		"scaffolded paperclip file": {".paperclip/harness.json": "{}\n"},
		"both":                      {".agents/agents/x.md": "x\n", ".paperclip/harness.json": "{}\n"},
	} {
		if got, ok := flavor.Detect(repoWithFiles(t, files)); ok {
			t.Errorf("%s: adoption output must not decide a flavor, got %q", name, got)
		}
	}
	// It remains selectable by name, so the flavor is not unreachable.
	if _, err := flavor.Get("agentic-autonomous"); err != nil {
		t.Errorf("agentic-autonomous must stay selectable explicitly: %v", err)
	}
}

// TestDetect_Boundary_NoMatchIsDistinguishableFromGoLibrary is the whole point of the second
// return value. Both of these used to report go-library: one because it genuinely matched
// nothing, the other because it genuinely is a Go library.
func TestDetect_Boundary_NoMatchIsDistinguishableFromGoLibrary(t *testing.T) {
	if got, ok := flavor.Detect(repoWithFiles(t, map[string]string{"Rakefile": "task :default\n"})); ok {
		t.Errorf("an unrecognised repository must report no match, got %q", got)
	}
	real := repoWithFiles(t, map[string]string{"go.mod": "module x\n", "internal/doc.go": "package internal\n"})
	if got, ok := flavor.Detect(real); !ok || got != "go-library" {
		t.Errorf("a genuine Go library must still detect, got %q ok=%v", got, ok)
	}
	// DetectFlavor keeps the old shape for callers that must name something, but the
	// substitution is now named rather than hidden.
	if got := flavor.DetectFlavor(repoWithFiles(t, map[string]string{"Rakefile": "x\n"})); got != flavor.FallbackFlavor {
		t.Errorf("DetectFlavor must substitute the named fallback, got %q", got)
	}
}

// TestList_Boundary_OrderIsStable pins the ordering that used to come from map iteration, so the
// catalog the CLI prints and the precedence detection uses are one thing and are reproducible.
func TestList_Boundary_OrderIsStable(t *testing.T) {
	first := flavor.List()
	if len(first) < 2 {
		t.Fatalf("expected a populated catalog, got %d", len(first))
	}
	for i := 0; i < 5; i++ {
		next := flavor.List()
		if len(next) != len(first) {
			t.Fatalf("catalog length changed between calls: %d then %d", len(first), len(next))
		}
		for j := range first {
			if first[j].Name() != next[j].Name() {
				t.Fatalf("catalog order changed at %d: %q then %q", j, first[j].Name(), next[j].Name())
			}
		}
	}
}

// =========================================================================
// The declared profile decides whether a flavor applies at all (BUG-952)
// =========================================================================

// declaringRepo builds a repository whose manifest declares one profile, plus any extra files.
func declaringRepo(t *testing.T, profile string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		".standards.yaml": "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - " + profile + "\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return repoWithFiles(t, files)
}

// TestAuditFlavor_Positive_DeclaredProfileNarrowsDetection covers the ordinary case. A declared
// profile restricts the candidates to the flavors that implement it, and detection picks among
// those, so a Go framework repository still resolves to the more specific of its two flavors.
func TestAuditFlavor_Positive_DeclaredProfileNarrowsDetection(t *testing.T) {
	repo := declaringRepo(t, "framework", map[string]string{
		"go.mod": "module fixture\n", "cmd/app/main.go": "package main\n", "internal/doc.go": "package internal\n",
	})
	report, err := flavor.AuditFlavor(repo, "auto")
	if err != nil {
		t.Fatalf("a declared profile with matching flavors must audit: %v", err)
	}
	if report.Flavor != "go-service" {
		t.Errorf("detection must pick the matching flavor within the profile, got %q", report.Flavor)
	}
}

// TestAuditFlavor_Negative_ProfileWithNoFlavorIsNotApplicable pins the not-applicable
// outcome. It used to use os-image, the profile cordanaLLM/imago declares, back when
// no flavor implemented it; os-image has one now, so the case moves to a profile that
// still has none. The outcome under test is unchanged: a declared profile nothing
// implements is not-applicable, never a marker guess.
func TestAuditFlavor_Negative_ProfileWithNoFlavorIsNotApplicable(t *testing.T) {
	repo := declaringRepo(t, "pages-site", map[string]string{
		"go.mod": "module fixture\n", "cmd/app/main.go": "package main\n",
	})
	report, err := flavor.AuditFlavor(repo, "auto")
	if !errors.Is(err, flavor.ErrFlavorNotApplicable) {
		t.Fatalf("a profile with no flavors must be not-applicable, got report=%v err=%v", report, err)
	}
	if errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Error("not-applicable must be distinguishable from nothing-matched; callers treat them differently")
	}
	// The markers alone would have matched go-service, which is exactly the wrong answer.
	if got, ok := flavor.Detect(repo); !ok || got != "go-service" {
		t.Errorf("precondition: bare marker detection should still say go-service, got %q ok=%v", got, ok)
	}
}

// TestAuditFlavor_Boundary_ExplicitFlavorAndAbsentManifest checks the two escapes. An explicit
// flavor overrides the declaration, and a repository with no manifest falls back to detection.
func TestAuditFlavor_Boundary_ExplicitFlavorAndAbsentManifest(t *testing.T) {
	declared := declaringRepo(t, "os-image", map[string]string{"go.mod": "module fixture\n", "internal/doc.go": "package internal\n"})
	if _, err := flavor.AuditFlavor(declared, "go-library"); err != nil {
		t.Errorf("an explicit flavor must override a not-applicable profile: %v", err)
	}
	undeclared := repoWithFiles(t, map[string]string{"go.mod": "module fixture\n", "internal/doc.go": "package internal\n"})
	report, err := flavor.AuditFlavor(undeclared, "auto")
	if err != nil {
		t.Fatalf("a repository with no manifest must fall back to detection: %v", err)
	}
	if report.Flavor != "go-library" {
		t.Errorf("expected detection to decide, got %q", report.Flavor)
	}
}

// TestAuditFlavor_Positive_DeclaredOSImageAuditsAgainstItsFlavor is the other half of the
// same defect: an image forge that declares os-image is audited against a flavor that
// describes what it builds, not against the Go tooling it happens to build with.
func TestAuditFlavor_Positive_DeclaredOSImageAuditsAgainstItsFlavor(t *testing.T) {
	repo := declaringRepo(t, "os-image", map[string]string{
		"packer/ubuntu.pkr.hcl": "source \"qemu\" \"ubuntu\" {}\n",
		"go.mod":                "module fixture\n",
		"cmd/imago/main.go":     "package main\n",
	})
	report, err := flavor.AuditFlavor(repo, "auto")
	if err != nil {
		t.Fatalf("a declared profile with a matching flavor must audit: %v", err)
	}
	if report.Flavor != "os-image" {
		t.Fatalf("audited as %q; the Go tooling outranked the product", report.Flavor)
	}
}
