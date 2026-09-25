package devcontainer

import (
	"encoding/base64"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// bootstrapHeadroomPercent is the share of each capture bound the repository's own source
// may occupy. Crossing it is a warning shot: BUG-985 reached 96% of the frame cap before a
// single added test failed every bootstrap, and nothing reported the approach.
const bootstrapHeadroomPercent = 80

// TestBootstrapSourceExcludesGoTestSurface covers the positive and boundary cases of the
// capture: _test.go files and testdata directories at any depth, tracked or untracked, stay
// out, while names that merely resemble them stay in.
func TestBootstrapSourceExcludesGoTestSurface(t *testing.T) {
	root := bootstrapSourceFixture(t)
	for _, name := range []string{
		"cmd/standardsctl/main_test.go",
		"internal/x/x_test.go",
		"internal/x/testdata/nested/deep.go",
		"testdata/top.go",
		"internal/x/x.go",
		"internal/x/x_test_helper.go",
		"internal/x/testdata.go",
		"internal/mytestdata/m.go",
	} {
		writeBootstrapFile(t, root, name, "package x\n")
	}
	// Half the fixture is tracked so both --cached and --others are exercised.
	if _, err := runSourceGit(t.Context(), root, "add", "--", "internal/x", "cmd/standardsctl/main_test.go"); err != nil {
		t.Fatal(err)
	}
	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		names = append(names, file.Name)
	}
	want := []string{
		"LICENSE",
		"cmd/standardsctl/main.go",
		"go.mod",
		"go.sum",
		"internal/mytestdata/m.go",
		"internal/x/testdata.go",
		"internal/x/x.go",
		"internal/x/x_test_helper.go",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("captured set = %q, want %q", names, want)
	}
}

// refusedBootstrapTestPaths are test-only paths at the root, nested and combined.
var refusedBootstrapTestPaths = []string{"main_test.go", "cmd/standardsctl/main_test.go", "_test.go", "testdata/top.go", "a/testdata/b/c.go", "testdata/x_test.go"}

// TestBootstrapSourceNameRefusesTestSurface covers the name rule in both directions: every
// test-only path is refused, and names that only resemble one are admitted.
func TestBootstrapSourceNameRefusesTestSurface(t *testing.T) {
	for _, name := range refusedBootstrapTestPaths {
		err := validateBootstrapSourceName(name)
		if err == nil || !strings.Contains(err.Error(), "test-only") {
			t.Errorf("validateBootstrapSourceName(%q) = %v, want a test-only refusal", name, err)
		}
	}
	for _, name := range []string{"x_test_helper.go", "testdata.go", "a/mytestdata/b.go", "a/testdatas/b.go", "test.go", "go.mod"} {
		if err := validateBootstrapSourceName(name); err != nil {
			t.Errorf("validateBootstrapSourceName(%q) refused a build input: %v", name, err)
		}
	}
}

// TestBootstrapSourceSetRefusesInjectedTests covers the negative direction past capture: a
// test-only member passed straight to the set rule, or carried in an archive, is refused.
func TestBootstrapSourceSetRefusesInjectedTests(t *testing.T) {
	files, err := captureBootstrapSource(t.Context(), bootstrapSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range refusedBootstrapTestPaths {
		t.Run(name, func(t *testing.T) {
			injected := append(append([]bootstrapSourceFile(nil), files...), bootstrapSourceFile{Name: name, Data: []byte("package x\n")})
			if err := validateBootstrapSourceSet(injected); err == nil {
				t.Fatal("set rule accepted a test-only member")
			}
			archive, err := encodeBootstrapArchive(injected)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeBootstrapArchive(t.Context(), archive); err == nil || !strings.Contains(err.Error(), "test-only") {
				t.Fatalf("decode accepted a test-only entry: %v", err)
			}
		})
	}
}

// TestBootstrapArchiveFitsAtTheFrameCap pins the one frame-capacity bound: an archive whose
// base64 form fills the four frames exactly is accepted and framed, one byte more is not.
func TestBootstrapArchiveFitsAtTheFrameCap(t *testing.T) {
	capacity := maxBootstrapParts * bootstrapPartBytes
	atCap := base64.StdEncoding.DecodedLen(capacity)
	if base64.StdEncoding.EncodedLen(atCap) != capacity {
		t.Fatalf("fixture is not at the cap: %d encodes to %d", atCap, base64.StdEncoding.EncodedLen(atCap))
	}
	if err := checkBootstrapArchiveFits(atCap); err != nil {
		t.Fatalf("archive exactly at the cap refused: %v", err)
	}
	parts, err := frameBootstrapArchive(make([]byte, atCap))
	if err != nil || len(parts) != maxBootstrapParts || len(parts[maxBootstrapParts-1].Content) != bootstrapPartBytes {
		t.Fatalf("archive exactly at the cap did not fill four frames: %d parts, %v", len(parts), err)
	}
	for _, size := range []int{atCap + 1, 0, -1} {
		err := checkBootstrapArchiveFits(size)
		if err == nil || !strings.Contains(err.Error(), "four bounded archive frames hold") {
			t.Errorf("checkBootstrapArchiveFits(%d) = %v, want the frame-cap refusal", size, err)
		}
	}
}

// TestRepositoryBootstrapSourceKeepsHeadroom measures this repository's own bootstrap
// capture against every capture bound, so the next cliff fails here, with numbers, before
// it fails an unrelated change's dogfood or MCP test.
func TestRepositoryBootstrapSourceKeepsHeadroom(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	present, err := util.GitWorktreePresent(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skipf("%s is not a Git checkout; the bootstrap capture inventories Git and cannot be measured from a source export", root)
	}
	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	raw := 0
	for _, file := range files {
		raw += len(file.Data)
	}
	for _, bound := range []struct {
		name        string
		used, limit int
	}{
		{"base64 archive frames", base64.StdEncoding.EncodedLen(len(archive)), maxBootstrapParts * bootstrapPartBytes},
		{"uncompressed source bytes", raw, maxBootstrapSourceBytes},
		{"source files", len(files), maxBootstrapFiles},
	} {
		t.Logf("%s: %d of %d (%d%%)", bound.name, bound.used, bound.limit, bound.used*100/bound.limit)
		if bound.used*100 > bound.limit*bootstrapHeadroomPercent {
			t.Errorf("%s use %d of %d, past the %d%% headroom threshold; shrink the capture before the cap fails every bootstrap", bound.name, bound.used, bound.limit, bootstrapHeadroomPercent)
		}
	}
}
