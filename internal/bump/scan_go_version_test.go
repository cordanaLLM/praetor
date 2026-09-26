package bump

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func TestCurrentGoModVersion_Positive_ReadsRequiredVersion(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, `module example.com/app

go 1.27

require (
	github.com/spf13/cobra v1.8.1
	gopkg.in/yaml.v3 v3.0.1
)
`)

	version, moduleDir, err := CurrentGoModVersion(t.Context(), dir, "github.com/spf13/cobra")
	if err != nil {
		t.Fatalf("CurrentGoModVersion: %v", err)
	}
	if version != "v1.8.1" {
		t.Errorf("expected v1.8.1, got %q", version)
	}
	if moduleDir != "." {
		t.Errorf("expected the root module dir, got %q", moduleDir)
	}
}

func TestCurrentGoModVersion_Positive_SingleLineRequire(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/app\n\nrequire gopkg.in/yaml.v3 v3.0.1\n")

	version, _, err := CurrentGoModVersion(t.Context(), dir, "gopkg.in/yaml.v3")
	if err != nil {
		t.Fatalf("CurrentGoModVersion: %v", err)
	}
	if version != "v3.0.1" {
		t.Errorf("expected v3.0.1, got %q", version)
	}
}

func TestCurrentGoModVersion_Negative_PackageAbsent(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/app\n\nrequire gopkg.in/yaml.v3 v3.0.1\n")

	_, _, err := CurrentGoModVersion(t.Context(), dir, "github.com/does/not-exist")
	if !errors.Is(err, ErrPackageNotRequired) {
		t.Fatalf("expected ErrPackageNotRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "github.com/does/not-exist") {
		t.Errorf("the error must name the package, got %v", err)
	}
}

func TestCurrentGoModVersion_Boundary_NoManifestAndHugeManifest(t *testing.T) {
	empty := t.TempDir()
	if _, _, err := CurrentGoModVersion(t.Context(), empty, "any/pkg"); !errors.Is(err, ErrPackageNotRequired) {
		t.Errorf("a directory with no go.mod must report ErrPackageNotRequired, got %v", err)
	}

	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("module example.com/app\n\nrequire (\n")
	// Short comment lines keep the manifest under contextopt.MaxSourceBytes, so the line
	// bound, not the byte cap, is what this case reaches.
	for i := 0; i < MaxManifestLines+10; i++ {
		b.WriteString("\t//\n")
	}
	b.WriteString("\ttail/pkg v9.9.9\n)\n")
	if b.Len() > contextopt.MaxSourceBytes {
		t.Fatalf("line-bound fixture is %d bytes, above the %d-byte cap", b.Len(), contextopt.MaxSourceBytes)
	}
	writeGoMod(t, dir, b.String())

	// The scan stops at the bound rather than reading an unbounded manifest, so the
	// package past the cap is reported as absent instead of hanging the scan.
	if _, _, err := CurrentGoModVersion(t.Context(), dir, "tail/pkg"); !errors.Is(err, ErrPackageNotRequired) {
		t.Errorf("expected the line bound to cut the scan short, got %v", err)
	}
}

// TestCurrentGoModVersion_Boundary_ByteCapMatchesTheWriter pins the lookup to the rule the
// writer enforces: a go.mod of exactly contextopt.MaxSourceBytes is read, and one byte more is
// refused and reported rather than scanned for a version the update could never write.
func TestCurrentGoModVersion_Boundary_ByteCapMatchesTheWriter(t *testing.T) {
	exact := t.TempDir()
	writeGoMod(t, exact, goModOfSize(t, contextopt.MaxSourceBytes))
	version, _, err := CurrentGoModVersion(t.Context(), exact, "example.com/pkg")
	if err != nil || version != "v1.0.0" {
		t.Fatalf("go.mod of exactly %d bytes: version %q, err %v", contextopt.MaxSourceBytes, version, err)
	}

	over := t.TempDir()
	writeGoMod(t, over, goModOfSize(t, contextopt.MaxSourceBytes+1))
	version, _, err = CurrentGoModVersion(t.Context(), over, "example.com/pkg")
	if err == nil || version != "" {
		t.Fatalf("go.mod of %d bytes: version %q accepted, err %v", contextopt.MaxSourceBytes+1, version, err)
	}
	if errors.Is(err, ErrPackageNotRequired) {
		t.Errorf("a refused go.mod must be reported as refused, not as not requiring the package: %v", err)
	}
}

// TestCurrentGoModVersion_Negative_SymlinkedManifestIsRefused covers the confinement half of
// the shared rule. The link target requires the package, so a lookup that followed the link
// would return its version; the writer refuses the same link.
func TestCurrentGoModVersion_Negative_SymlinkedManifestIsRefused(t *testing.T) {
	dir := t.TempDir()
	body := "module example.com/app\n\nrequire example.com/pkg v1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "real.mod"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.mod", filepath.Join(dir, "go.mod")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	version, _, err := CurrentGoModVersion(t.Context(), dir, "example.com/pkg")
	if err == nil || version != "" {
		t.Fatalf("symlinked go.mod was followed: version %q, err %v", version, err)
	}
}

// TestCurrentGoModVersion_Negative_CallerContext proves the read runs under the caller's
// context: a cancelled caller gets its cancellation, and a nil context is refused.
func TestCurrentGoModVersion_Negative_CallerContext(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/app\n\nrequire example.com/pkg v1.0.0\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if version, _, err := CurrentGoModVersion(ctx, dir, "example.com/pkg"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: version %q, err %v, want context.Canceled", version, err)
	}
	var nilCtx context.Context
	if _, _, err := CurrentGoModVersion(nilCtx, dir, "example.com/pkg"); err == nil {
		t.Fatal("nil context accepted")
	}
}

// TestCurrentGoModVersion_Negative_ReplaceIsNotARequirement pins the shared require-block
// parser: a replace or exclude directive names the package and a version but does not
// require it.
func TestCurrentGoModVersion_Negative_ReplaceIsNotARequirement(t *testing.T) {
	for _, directives := range []string{
		"replace (\n\texample.com/pkg v1.0.0 => example.com/fork v1.2.0\n)\n",
		"exclude (\n\texample.com/pkg v1.0.0\n)\n",
		"exclude example.com/pkg v1.0.0\n",
	} {
		dir := t.TempDir()
		writeGoMod(t, dir, "module example.com/app\n\n"+directives)
		if version, _, err := CurrentGoModVersion(t.Context(), dir, "example.com/pkg"); !errors.Is(err, ErrPackageNotRequired) {
			t.Fatalf("%q read as a requirement: version %q, err %v", directives, version, err)
		}
	}
}

// TestCurrentGoModVersion_Positive_NestedModuleUnderTheRepoRoot reads a discovered module's
// go.mod through the repository-root confinement and reports that module's directory.
func TestCurrentGoModVersion_Positive_NestedModuleUnderTheRepoRoot(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "tools")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	writeGoMod(t, nested, "module example.com/tools\n\nrequire example.com/pkg v1.4.0 // indirect\n")
	version, moduleDir, err := CurrentGoModVersion(t.Context(), dir, "example.com/pkg")
	if err != nil || version != "v1.4.0" || moduleDir != "tools" {
		t.Fatalf("nested module: version %q, dir %q, err %v", version, moduleDir, err)
	}
}
