package bump

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for i := 0; i < MaxManifestLines+10; i++ {
		b.WriteString("\tfiller/pkg v0.0.1\n")
	}
	b.WriteString("\ttail/pkg v9.9.9\n)\n")
	writeGoMod(t, dir, b.String())

	// The scan stops at the bound rather than reading an unbounded manifest, so the
	// package past the cap is reported as absent instead of hanging the scan.
	if _, _, err := CurrentGoModVersion(t.Context(), dir, "tail/pkg"); !errors.Is(err, ErrPackageNotRequired) {
		t.Errorf("expected the line bound to cut the scan short, got %v", err)
	}
}
