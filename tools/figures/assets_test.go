// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package figures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// repositoryOnly lists every tracked file under Directory that adoption never writes
// (docs/adr/0016-figures-for-adopters.md, section 2): this package's own Go files, the npm
// manifest and lock the player is built from, the player sources, the bundler, the smoke test,
// the tests and their fixtures, and the vendored upstream files the render does not import.
var repositoryOnly = []string{
	"assets.go",
	"assets_test.go",
	"package.json",
	"package-lock.json",
	"tsconfig.json",
	"loader.ts",
	"player.tsx",
	"keyboard.ts",
	"bundle.mjs",
	"smoke.mjs",
	"testkit.mjs",
	"figures.test.mjs",
	"checks.test.mjs",
	"astro.test.mjs",
	"test_mkdocs_hook.py",
	"fence-fixtures.json",
	"markup-fixtures.json",
	"exclude-fixtures.json",
	"third_party/interfig/upstream/README.md",
	"third_party/interfig/upstream/package.json",
	"third_party/interfig/upstream/scripts/figure-loader.mjs",
	"third_party/interfig/upstream/scripts/figure-svg.mjs",
	"third_party/interfig/upstream/src/index.tsx",
	"third_party/interfig/upstream/src/node-figures.ts",
	"third_party/interfig/upstream/src/figure-svg.test.ts",
	"third_party/interfig/upstream/src/geometry.test.ts",
	"third_party/interfig/upstream/src/model.test.ts",
	"third_party/interfig/upstream/src/svg.test.ts",
}

// distFiles is the complete committed player (DIST_FILES in bundle.mjs).
var distFiles = []string{"dist/THIRD-PARTY-LICENSES.txt", "dist/loader.js", "dist/player.js"}

// trackedFiles lists every file under Directory that git tracks or would track, relative to
// Directory, sorted. ok is false outside a Git checkout, where there is no listing to compare.
func trackedFiles(t *testing.T) (names []string, ok bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root := filepath.Join("..", "..")
	present, err := util.GitWorktreePresent(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		return nil, false
	}
	result, err := util.RunGitBytes(ctx, root, 1<<20, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Split(string(result.Stdout), "\x00") {
		if name != "" {
			names = append(names, strings.TrimPrefix(name, Directory+"/"))
		}
	}
	slices.Sort(names)
	return names, true
}

// Positive: the inventory is exactly the tracked tree minus the repository-only files, every
// repository-only name is still tracked, and the committed player is exactly its three files.
func TestAssetInventoryMatchesTrackedTree(t *testing.T) {
	tracked, ok := trackedFiles(t)
	if !ok {
		t.Skip("not a Git checkout: git ls-files has nothing to compare the inventory with")
	}
	var want []string
	for _, name := range tracked {
		if !slices.Contains(repositoryOnly, name) {
			want = append(want, name)
		}
	}
	got := slices.Sorted(slices.Values(Names()))
	if !slices.Equal(got, want) {
		t.Fatalf("embedded inventory = %q\ntracked minus repository-only = %q\nlist a new file in assetNames or in repositoryOnly", got, want)
	}
	for _, name := range repositoryOnly {
		if !slices.Contains(tracked, name) {
			t.Errorf("repositoryOnly names %s, which git does not track; drop it from the list", name)
		}
	}
	var dist []string
	for _, name := range tracked {
		if strings.HasPrefix(name, "dist/") {
			dist = append(dist, name)
		}
	}
	if !slices.Equal(dist, distFiles) {
		t.Fatalf("dist/ holds %q, want exactly %q", dist, distFiles)
	}
}

// Negative: no repository-only file, npm install tree or path outside the inventory is embedded
// or readable, whatever the go:embed patterns match.
func TestAssetInventoryRefusesRepositoryOnlyFiles(t *testing.T) {
	forbidden := regexp.MustCompile(`(^|/)(node_modules|package(-lock)?\.json$|tsconfig\.json$)|\.test\.|(^|/)test_|\.tsx$|fixtures\.json$`)
	for _, name := range Names() {
		if forbidden.MatchString(name) || slices.Contains(repositoryOnly, name) {
			t.Errorf("the inventory lists %s, which adoption must never write", name)
		}
	}
	refused := append(slices.Clone(repositoryOnly), "", ".", "dist", "../assets.go", "node_modules/react/index.js", "/core.mjs", "dist/loader.js/")
	for _, name := range refused {
		if _, err := Read(name); err == nil || !strings.Contains(err.Error(), "unknown figures asset") {
			t.Errorf("Read(%q) = %v, want an unknown-asset refusal", name, err)
		}
		if _, err := fs.Stat(FS(), name); err == nil && name != "." && name != "dist" {
			t.Errorf("the embedded tree holds %s", name)
		}
	}
}

// Boundary: the inventory fills its bound exactly, every accessor returns a private copy, the
// embed directive in assets.go lists the inventory in order, and every asset is LF text that
// the checkout policy pins to LF on every platform.
func TestAssetInventoryBoundary(t *testing.T) {
	names := Names()
	if len(names) != MaxAssets || len(assetNames) != MaxAssets {
		t.Fatalf("inventory holds %d names (%d declared), want exactly MaxAssets=%d", len(names), len(assetNames), MaxAssets)
	}
	names[0] = "mutated"
	if Names()[0] != "core.mjs" {
		t.Fatal("Names exposed the inventory for mutation")
	}
	source, err := os.ReadFile("assets.go")
	if err != nil {
		t.Fatal(err)
	}
	if directive := "//go:embed " + strings.Join(Names(), " "); !slices.Contains(strings.Split(string(source), "\n"), directive) {
		t.Fatalf("assets.go does not carry the directive %q", directive)
	}
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 || strings.Contains(string(data), "\r") {
			t.Fatalf("embedded figure asset %s is empty or carries a carriage return", name)
		}
		data[0] ^= 0xff
		again, err := Read(name)
		if err != nil || again[0] == data[0] {
			t.Fatalf("Read(%s) exposed embedded storage for mutation: %v", name, err)
		}
	}
	attributes, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Split(string(attributes), "\n"), Directory+"/** text eol=lf") {
		t.Fatalf(".gitattributes does not pin %s/** to LF", Directory)
	}
}

// moduleSpecifier matches the specifier of a static import or re-export (`from '…'`), a
// side-effect import (`import '…'`) and a literal dynamic import (`import('…')`). A specifier
// holds no space, quote or template placeholder, so `from "${edge.from}"` in a message is not one.
var moduleSpecifier = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(?\s*)["']([\w@./:-]+)["']`)

// maxScannedLines bounds the comment strip of one asset (HISS-02).
const maxScannedLines = 20000

// codeWithoutLineComments drops the lines of a JavaScript or TypeScript file that are comments
// only, so an example in a header comment is not read as an import.
func codeWithoutLineComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for index := 0; index < len(lines) && index < maxScannedLines; index++ {
		trimmed := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "/*") {
			kept = append(kept, lines[index])
		}
	}
	return strings.Join(kept, "\n")
}

// importViolations reports every module a script in files imports that a repository holding only
// the inventory cannot load: a relative specifier that resolves outside the inventory, and a bare
// package specifier other than a node: builtin or a type-only import, which Node erases. It also
// returns every import edge it resolved, as "from -> to".
func importViolations(files map[string]string, inventory []string) (violations, edges []string) {
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(name, ".mjs") && !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".ts") {
			continue
		}
		code := codeWithoutLineComments(files[name])
		for _, match := range moduleSpecifier.FindAllStringSubmatchIndex(code, -1) {
			specifier := code[match[2]:match[3]]
			switch {
			case strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../"):
				target := path.Join(path.Dir(name), specifier)
				if !slices.Contains(inventory, target) {
					violations = append(violations, name+" imports "+specifier+", which adoption does not write")
					continue
				}
				edges = append(edges, name+" -> "+target)
			case strings.HasPrefix(specifier, "node:"):
			case strings.HasPrefix(strings.TrimSpace(lineAt(code, match[0])), "import type "):
			default:
				violations = append(violations, name+" imports the package "+specifier+", which an adopter does not install")
			}
		}
	}
	return violations, edges
}

// lineAt returns the line of text holding offset.
func lineAt(text string, offset int) string {
	start := strings.LastIndex(text[:offset], "\n") + 1
	end := strings.Index(text[offset:], "\n")
	if end < 0 {
		return text[start:]
	}
	return text[start : offset+end]
}

// embeddedScripts reads every inventory asset into a name -> text map.
func embeddedScripts(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	return files
}

// Positive: the inventory is import-closed, so a repository holding only these files renders,
// checks and plays figures with plain Node; the known edges prove the scan read the imports.
func TestAssetInventoryIsImportClosed(t *testing.T) {
	violations, edges := importViolations(embeddedScripts(t), Names())
	for _, violation := range violations {
		t.Error(violation)
	}
	for _, edge := range []string{
		"build.mjs -> checks.mjs",
		"core.mjs -> third_party/interfig/upstream/src/svg.ts",
		"third_party/interfig/upstream/src/svg.ts -> third_party/interfig/upstream/src/model.ts",
		"astro.mjs -> serve.mjs",
		"dist/loader.js -> dist/player.js",
	} {
		if !slices.Contains(edges, edge) {
			t.Errorf("the import scan missed %s; it found %q", edge, edges)
		}
	}
}

// Negative and boundary: an import of a repository-only module, one escaping the tree and a
// runtime package import are reported; a node: builtin, a type-only import and an import in a
// comment are not.
func TestImportViolationsNegativeAndBoundary(t *testing.T) {
	inventory := []string{"core.mjs", "build.mjs", "types.ts"}
	files := map[string]string{
		"build.mjs": "import { bundle } from './bundle.mjs';\nimport { core } from './core.mjs';\nimport { x } from '../outside.mjs';\n",
		"core.mjs":  "import { createHash } from 'node:crypto';\nimport React from 'react';\nconst lazy = () => import('./player.js');\n",
		"types.ts":  "// import figures from './tools/figures/astro.mjs';\n * import('./nowhere.js')\nimport type { ReactNode } from 'react';\n",
	}
	violations, edges := importViolations(files, inventory)
	want := []string{
		"build.mjs imports ./bundle.mjs, which adoption does not write",
		"build.mjs imports ../outside.mjs, which adoption does not write",
		"core.mjs imports the package react, which an adopter does not install",
		"core.mjs imports ./player.js, which adoption does not write",
	}
	if !slices.Equal(violations, want) {
		t.Fatalf("violations = %q\nwant %q", violations, want)
	}
	if !slices.Equal(edges, []string{"build.mjs -> core.mjs"}) {
		t.Fatalf("edges = %q", edges)
	}
	if violations, _ := importViolations(map[string]string{"README.md": "import x from './gone.mjs'"}, nil); len(violations) != 0 {
		t.Fatalf("a non-script asset was scanned: %q", violations)
	}
}

// lfDigest spells a digest the way PriorDigests keys are spelled, computed here rather than
// through the code under test.
func lfDigest(data []byte) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(data), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// Positive: every earlier text names one of the family's assets by a lowercase SHA-256, and the
// outgoing build.mjs this repository replaced is listed.
func TestPriorDigestsNameManagedAssets(t *testing.T) {
	digests := PriorDigests()
	for digest, rel := range digests {
		name, below := strings.CutPrefix(rel, Directory+"/")
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size || strings.ToLower(digest) != digest {
			t.Errorf("prior digest %q is not a lowercase SHA-256", digest)
		}
		if !below || !slices.Contains(Names(), name) {
			t.Errorf("prior digest %s names %s, which is not a managed asset", digest, rel)
		}
	}
	if digests["f75975e7f7cbb147b156c3a8f4a00bffef985aa18fa4ee67f6505479fd8e730c"] != Directory+"/build.mjs" {
		t.Fatal("the build.mjs text before the no-spec skip is not a prior text")
	}
}

// Negative: no current asset text is listed as an earlier one, and PriorDigests hands out a copy.
func TestPriorDigestsExcludeCurrentTexts(t *testing.T) {
	digests := PriorDigests()
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, listed := digests[lfDigest(data)]; listed {
			t.Fatalf("the current text of %s is listed as an earlier text", name)
		}
	}
	clear(digests)
	if len(PriorDigests()) == 0 {
		t.Fatal("PriorDigests exposed its map for mutation")
	}
}

// Boundary: a current text differing by one trailing byte is not an earlier text, and the CRLF
// form of a text reduces to the same digest as its LF form.
func TestPriorDigestsBoundary(t *testing.T) {
	data, err := Read("build.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, listed := PriorDigests()[lfDigest(append(data, '\n'))]; listed {
		t.Fatal("a text one byte longer than the current build.mjs is listed")
	}
	if lfDigest([]byte(strings.ReplaceAll(string(data), "\n", "\r\n"))) != lfDigest(data) {
		t.Fatal("the CRLF form of a text does not reduce to its LF digest")
	}
}
