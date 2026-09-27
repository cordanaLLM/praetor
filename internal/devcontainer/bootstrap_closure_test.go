package devcontainer

import (
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// goFile renders a parseable Go file of package pkg with the given import specs.
func goFile(pkg string, imports ...string) string {
	var s strings.Builder
	s.WriteString("package " + pkg + "\n")
	for _, spec := range imports {
		s.WriteString("\nimport " + spec + "\n")
	}
	return s.String()
}

func capturedNames(t *testing.T, root string) []string {
	t.Helper()
	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
	}
	return names
}

// TestBootstrapClosureCapturesOnlyTheBuildImports covers the closure in both directions: the
// packages cmd/standardsctl reaches, directly or transitively, through any import form and
// through a file behind a build constraint, are captured; a package only another command
// imports, and the command itself, are not (#501). The sibling-prefix module and the import
// cycle are the boundary: neither is followed twice or mistaken for a module package.
func TestBootstrapClosureCapturesOnlyTheBuildImports(t *testing.T) {
	root := bootstrapSourceFixture(t)
	for name, data := range map[string]string{
		"cmd/standardsctl/main.go": goFile("main", `"fmt"`, `app "github.com/cordanaLLM/praetor/internal/a"`, `"github.com/cordanaLLM/praetorx/sibling"`) + "\nfunc main() { fmt.Println(app.A) }\n",
		"internal/a/a.go":          goFile("a", `_ "github.com/cordanaLLM/praetor/internal/b"`) + "\nconst A = 1\n",
		"internal/b/b.go":          goFile("b", `. "github.com/cordanaLLM/praetor/internal/a"`),
		"internal/b/b_windows.go":  "//go:build windows\n\n" + goFile("b", `"github.com/cordanaLLM/praetor/internal/c"`),
		"internal/c/c.go":          goFile("c", `"github.com/cordanaLLM/praetor"`),
		"root.go":                  goFile("praetor"),
		"cmd/other/main.go":        goFile("main", `"github.com/cordanaLLM/praetor/internal/unrelated"`) + "\nfunc main() {}\n",
		"internal/unrelated/u.go":  goFile("unrelated"),
		"internal/unrelated/u2.go": goFile("unrelated", `"github.com/cordanaLLM/praetor/internal/missing"`),
		"internal/a/a_test.go":     goFile("a", `"github.com/cordanaLLM/praetor/internal/unrelated"`),
		"internal/a/testdata/t.go": goFile("t", `"github.com/cordanaLLM/praetor/internal/unrelated"`),
	} {
		writeBootstrapFile(t, root, name, data)
	}
	want := []string{
		"LICENSE",
		"cmd/standardsctl/main.go",
		"go.mod",
		"go.sum",
		"internal/a/a.go",
		"internal/b/b.go",
		"internal/b/b_windows.go",
		"internal/c/c.go",
		"root.go",
	}
	if got := capturedNames(t, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("captured set = %q, want %q", got, want)
	}
}

// TestBootstrapClosureRefusesAnImportTheCaptureCannotBuild covers the negative direction: an
// import of a module package without captured Go source, absent or test-only, would fail go
// build in the image, so capture refuses it and names the importing file.
func TestBootstrapClosureRefusesAnImportTheCaptureCannotBuild(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"absent package": {
			"cmd/standardsctl/main.go": goFile("main", `_ "github.com/cordanaLLM/praetor/internal/missing"`),
		},
		"test-only package": {
			"cmd/standardsctl/main.go":    goFile("main", `_ "github.com/cordanaLLM/praetor/internal/testonly"`),
			"internal/testonly/x_test.go": goFile("testonly"),
		},
		"transitive absent package": {
			"cmd/standardsctl/main.go": goFile("main", `_ "github.com/cordanaLLM/praetor/internal/a"`),
			"internal/a/a.go":          goFile("a", `_ "github.com/cordanaLLM/praetor/internal/gone"`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := bootstrapSourceFixture(t)
			for path, data := range files {
				writeBootstrapFile(t, root, path, data)
			}
			_, err := captureBootstrapSource(t.Context(), root)
			if err == nil || !strings.Contains(err.Error(), "which has no captured Go source") {
				t.Fatalf("capture = %v, want the missing-package refusal", err)
			}
		})
	}
}

// TestBootstrapClosureCapturesReachedEmbedAssets covers every managed asset family under the
// closure: a reached embedding source brings every declared asset, an unreached one brings
// neither itself nor its assets, and an undeclared go:embed outside the closure no longer
// fails the capture, because go build never reads it.
func TestBootstrapClosureCapturesReachedEmbedAssets(t *testing.T) {
	families := managedasset.Families()
	if len(families) == 0 {
		t.Fatal("no managed asset family is declared")
	}
	for _, family := range families {
		t.Run(family.Name, func(t *testing.T) { checkClosureFamilyAssets(t, family) })
	}
}

func checkClosureFamilyAssets(t *testing.T, family managedasset.Family) {
	t.Helper()
	assets := family.AssetPaths()
	if len(assets) == 0 || len(assets) != len(family.Names()) {
		t.Fatalf("declared %s assets %q do not match their paths %q", family.Name, family.Names(), assets)
	}
	fixture := func(t *testing.T, mainImports ...string) string {
		t.Helper()
		root := bootstrapSourceFixture(t)
		writeBootstrapFile(t, root, "cmd/standardsctl/main.go", goFile("main", mainImports...))
		writeBootstrapFile(t, root, family.Source, goFile(path.Base(family.Directory), `"embed"`)+"\n"+family.EmbedDirective()+"\nvar assets embed.FS\n")
		for _, asset := range assets {
			writeBootstrapFile(t, root, asset, "{}\n")
		}
		writeBootstrapFile(t, root, "internal/unreached/embed.go", goFile("unreached", `"embed"`)+"\n//go:embed data.txt\nvar data string\n")
		writeBootstrapFile(t, root, "internal/unreached/data.txt", "undeclared asset\n")
		return root
	}
	t.Run("reached family", func(t *testing.T) {
		got := capturedNames(t, fixture(t, `_ "`+praetorModulePath+"/"+family.Directory+`"`))
		want := append([]string{"LICENSE", "cmd/standardsctl/main.go", "go.mod", "go.sum", family.Source}, assets...)
		slices.Sort(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("captured set = %q, want %q", got, want)
		}
	})
	t.Run("unreached family", func(t *testing.T) {
		got := capturedNames(t, fixture(t))
		want := []string{"LICENSE", "cmd/standardsctl/main.go", "go.mod", "go.sum"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("captured set = %q, want %q", got, want)
		}
	})
	t.Run("reached undeclared embed", func(t *testing.T) {
		root := fixture(t, `_ "github.com/cordanaLLM/praetor/internal/unreached"`)
		if _, err := captureBootstrapSource(t.Context(), root); err == nil || !strings.Contains(err.Error(), "explicit asset capture is required") {
			t.Fatalf("capture = %v, want the undeclared go:embed refusal", err)
		}
	})
}

// TestBootstrapArchiveFramesBeyondTheFormerCap covers every frame consumer for five to eight
// frames, the counts the former four-frame cap refused: framing, the Dockerfile's COPY lines,
// spec validation, companion reads, reassembly, and the image's `cat *.b64` order.
func TestBootstrapArchiveFramesBeyondTheFormerCap(t *testing.T) {
	for parts := 5; parts <= 8; parts++ {
		archive := make([]byte, (parts-1)*bootstrapPartBytes/4*3+1)
		spec := framedSpec(t, archive, parts)
		frames, err := frameBootstrapArchive(archive)
		if err != nil {
			t.Fatal(err)
		}
		dockerfile := renderBootstrapDockerfile(spec)
		dir := t.TempDir()
		names := make([]string, 0, parts)
		for i, frame := range frames {
			names = append(names, frame.Name)
			if !strings.Contains(dockerfile, `COPY ["`+frame.Name+`", "/tmp/praetor-source/`+frame.Name[len("praetor-source."):]+`"]`) {
				t.Fatalf("%d frames: Dockerfile does not copy frame %d:\n%s", parts, i, dockerfile)
			}
			writeBootstrapFile(t, dir, frame.Name, string(frame.Content))
		}
		if strings.Count(dockerfile, "COPY [") != parts || !slices.IsSorted(names) {
			t.Fatalf("%d frames: Dockerfile copies %d frames, names sorted %v", parts, strings.Count(dockerfile, "COPY ["), slices.IsSorted(names))
		}
		writeBootstrapFile(t, dir, bootstrapDockerfile, dockerfile)
		companions, err := readBootstrapCompanions(t.Context(), filepath.Join(dir, "devcontainer.json"), spec)
		if err != nil || len(companions) != parts+1 {
			t.Fatalf("%d frames: read %d companions, %v", parts, len(companions), err)
		}
		artifacts, err := bootstrapArtifactMap(companions)
		if err != nil {
			t.Fatal(err)
		}
		joined, err := collectBootstrapArchive(spec, artifacts)
		if err != nil || !slices.Equal(joined, archive) {
			t.Fatalf("%d frames: reassembly differs: %v", parts, err)
		}
	}
}

// framedSpec returns a ready spec recording archive in the given number of frames.
func framedSpec(t *testing.T, archive []byte, parts int) *BootstrapSpec {
	t.Helper()
	spec := &BootstrapSpec{Version: bootstrapVersion, State: BootstrapReady, BuilderImage: DefaultBuilderImage, BaseImage: DefaultBaseImage,
		SourceSHA256: bootstrapDigest([]byte("source")), ArchiveSHA256: bootstrapDigest(archive), ArchiveParts: parts}
	spec.DockerfileSHA256 = bootstrapDigest([]byte(renderBootstrapDockerfile(spec)))
	return spec
}

// TestBootstrapSpecPartCountBounds covers the recorded part count: one and eight frames are
// valid, zero and nine are refused whatever the Dockerfile digest says.
func TestBootstrapSpecPartCountBounds(t *testing.T) {
	for parts, valid := range map[int]bool{0: false, 1: true, 4: true, 5: true, 8: true, 9: false} {
		err := validateBootstrapSpec(framedSpec(t, []byte("archive"), parts))
		if (err == nil) != valid {
			t.Errorf("ArchiveParts %d: validation = %v, want valid %v", parts, err, valid)
		}
		if !valid && (err == nil || !strings.Contains(err.Error(), "part count exceeds bounds")) {
			t.Errorf("ArchiveParts %d: refusal = %v, want the part-count refusal", parts, err)
		}
	}
}

// TestBootstrapBundleRoundTripsInFiveFrames drives a source that needs more frames than the
// former cap of four allowed through preparation, writing and verification. Captured files
// must be UTF-8 text, so the bulk is uniformly random printable ASCII, which gzip cannot
// shrink below about 6.6 bits a byte: 2,000,000 such bytes need five frames.
func TestBootstrapBundleRoundTripsInFiveFrames(t *testing.T) {
	root := bootstrapSourceFixture(t)
	random := rand.New(rand.NewChaCha8([32]byte{5}))
	for _, name := range []string{"LICENSE", "go.sum"} {
		noise := make([]byte, 1000000)
		for i := range noise {
			noise[i] = byte(' ' + random.IntN('~'-' '+1))
		}
		if err := os.WriteFile(filepath.Join(root, name), noise, 0644); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if parts := bundle.Spec().ArchiveParts; parts != 5 {
		t.Fatalf("2,000,000 random printable bytes framed into %d parts, want 5", parts)
	}
	target := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), target, bundle, false); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), target, mustBaseContainer(t)); err != nil {
		t.Fatalf("five-frame bundle failed verification: %v", err)
	}
}
