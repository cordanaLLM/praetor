package needs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const migrateReadGoMod = "module example.com/big\n\ngo 1.24\n\nrequire github.com/a/b v1.0.0\n"

// paddedGoMod returns a go.mod of exactly size bytes: migrateReadGoMod followed by comment
// lines short enough for the line scanner.
func paddedGoMod(size int) string {
	line := "// " + strings.Repeat("x", 76) + "\n"
	var b strings.Builder
	b.WriteString(migrateReadGoMod)
	for i := 0; i < size && size-b.Len() >= len(line)+3; i++ {
		b.WriteString(line)
	}
	b.WriteString("//" + strings.Repeat("x", size-b.Len()-3) + "\n")
	return b.String()
}

// Positive: a regular Go source and go.mod below the root are read through the bounded,
// root-anchored reader, so planning finds the import and the go.mod rewrite applies.
func TestMigrationRead_Positive_RegularFiles(t *testing.T) {
	dir := t.TempDir()
	src := writeFixture(t, dir, "main.go", "package main\n\nimport \"github.com/a/b\"\n")
	replacements := map[string]string{"github.com/a/b": "example.com/acme/kit/b"}
	if got := scanFileForReplacements(dir, src, replacements, sortedReplacementKeys(replacements)); len(got) != 1 {
		t.Fatalf("actions = %+v, want the one import", got)
	}
	goMod := writeFixture(t, dir, "go.mod", migrateReadGoMod)
	if err := updateGoMod(dir, goMod, []string{"example.com/acme/kit v0.8.0"}, []string{"github.com/a/b"}); err != nil {
		t.Fatalf("updateGoMod: %v", err)
	}
	data, err := os.ReadFile(goMod)
	if err != nil || strings.Contains(string(data), "github.com/a/b") || !strings.Contains(string(data), "example.com/acme/kit v0.8.0") {
		t.Fatalf("go.mod after rewrite = %q, %v", data, err)
	}
}

// Negative: a go.mod linked to a file outside the repository is refused before it is read or
// rewritten, and a FIFO named like a Go source or go.mod is refused within the deadline
// instead of blocking the migration (BUG-857).
func TestMigrationRead_Negative_EscapeAndFIFO(t *testing.T) {
	outside := writeFixture(t, t.TempDir(), "go.mod", migrateReadGoMod)
	repo := t.TempDir()
	link := filepath.Join(repo, "go.mod")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := updateGoMod(repo, link, []string{"example.com/acme/kit v0.8.0"}, nil); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping go.mod = %v, want ErrPathEscapesRoot", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != migrateReadGoMod {
		t.Errorf("file outside the repository changed: %q, %v", data, err)
	}

	fifoRepo := t.TempDir()
	fifoSource, fifoGoMod := filepath.Join(fifoRepo, "main.go"), filepath.Join(fifoRepo, "go.mod")
	testsupport.MakeFIFO(t, fifoSource)
	testsupport.MakeFIFO(t, fifoGoMod)
	replacements := map[string]string{"github.com/a/b": "example.com/acme/kit/b"}
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		if got := scanFileForReplacements(fifoRepo, fifoSource, replacements, sortedReplacementKeys(replacements)); got != nil {
			return errors.New("a FIFO source yielded replacement actions")
		}
		return updateGoMod(fifoRepo, fifoGoMod, nil, nil)
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO go.mod = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: a go.mod of exactly maxMigrationFileBytes is rewritten; one byte more is refused
// and left untouched.
func TestMigrationRead_Boundary_ByteBound(t *testing.T) {
	dir := t.TempDir()
	exact := writeFixture(t, dir, "go.mod", paddedGoMod(maxMigrationFileBytes))
	if info, err := os.Stat(exact); err != nil || info.Size() != maxMigrationFileBytes {
		t.Fatalf("fixture size = %v, %v; want %d", info, err, maxMigrationFileBytes)
	}
	if err := updateGoMod(dir, exact, []string{"example.com/acme/kit v0.8.0"}, nil); err != nil {
		t.Fatalf("go.mod at the bound = %v, want it rewritten", err)
	}

	overDir := t.TempDir()
	body := paddedGoMod(maxMigrationFileBytes + 1)
	over := writeFixture(t, overDir, "go.mod", body)
	if err := updateGoMod(overDir, over, []string{"example.com/acme/kit v0.8.0"}, nil); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("go.mod one byte past the bound = %v, want ErrFileTooLarge", err)
	}
	if data, err := os.ReadFile(over); err != nil || string(data) != body {
		t.Fatal("an oversize go.mod must not be rewritten")
	}
}
