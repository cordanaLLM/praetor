// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

func findFigureFamily(t *testing.T) managedasset.Family {
	t.Helper()
	for _, f := range managedasset.Families() {
		if f.Name == "Figure engine" {
			return f
		}
	}
	t.Fatal("figure engine family not found")
	return managedasset.Family{}
}

func TestPreMoveFigureDirectiveMatchesGolden(t *testing.T) {
	figureFamily := findFigureFamily(t)
	preMoveFam, ok := preMoveBootstrapFamily(figureFamily)
	if !ok {
		t.Fatal("failed to derive pre-move figure family")
	}
	goldenPath := filepath.Join("testdata", "managed-family", "pre-move-figures-directive.txt")
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden directive: %v", err)
	}
	wantDirective := strings.TrimRight(string(goldenData), "\r\n")
	if preMoveFam.directive != wantDirective {
		t.Fatalf("derived directive differs from golden:\ngot:  %s\nwant: %s", preMoveFam.directive, wantDirective)
	}
	for _, rel := range []string{"dist/loader.js", "dist/player.js", "dist/THIRD-PARTY-LICENSES.txt"} {
		wantPath := figureFamily.Directory + "/" + rel
		if !slices.Contains(preMoveFam.assets, wantPath) {
			t.Errorf("pre-move assets missing expected path %s", wantPath)
		}
	}
}

func figurePreMoveFixtureFiles(t *testing.T) []bootstrapSourceFile {
	t.Helper()
	figureFamily := findFigureFamily(t)
	preMoveFam, ok := preMoveBootstrapFamily(figureFamily)
	if !ok {
		t.Fatal("failed to derive pre-move figure family")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	assetsGoData, err := os.ReadFile(filepath.Join(repoRoot, figureFamily.Source))
	if err != nil {
		t.Fatal(err)
	}
	preMoveAssetsGo := bytes.Replace(assetsGoData, []byte(figureFamily.EmbedDirective()), []byte(preMoveFam.directive), 1)
	files := []bootstrapSourceFile{
		{Name: "go.mod", Data: []byte("module github.com/cordanaLLM/praetor\n\ngo 1.27\n")},
		{Name: "go.sum", Data: []byte("")},
		{Name: "LICENSE", Data: []byte("Synthetic test license\n")},
		{Name: "cmd/standardsctl/main.go", Data: []byte("package main\n\nfunc main() {}\n")},
		{Name: figureFamily.Source, Data: preMoveAssetsGo},
	}
	for _, assetRel := range figureFamily.Names() {
		if assetRel == "eslint.figures.mjs" {
			continue
		}
		data, err := figureFamily.Read(assetRel)
		if err != nil {
			t.Fatalf("read %s: %v", assetRel, err)
		}
		repoPath := figureFamily.AssetPath(assetRel)
		if strings.HasPrefix(assetRel, "player/") {
			distRel := "dist/" + strings.TrimPrefix(assetRel, "player/")
			repoPath = figureFamily.Directory + "/" + distRel
		}
		files = append(files, bootstrapSourceFile{Name: repoPath, Data: data})
	}
	return files
}

func TestFigurePreMoveFixtureArchive_Positive(t *testing.T) {
	files := figurePreMoveFixtureFiles(t)
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatalf("encode fixture archive: %v", err)
	}
	decoded, err := decodeBootstrapArchive(t.Context(), archive)
	if err != nil {
		t.Fatalf("positive fixture archive failed verification: %v", err)
	}
	if len(decoded) != len(files) {
		t.Fatalf("decoded %d files, want %d", len(decoded), len(files))
	}
}

func TestFigurePreMoveFixtureArchive_Negative_UndeclaredEmbed(t *testing.T) {
	files := figurePreMoveFixtureFiles(t)
	for i := range files {
		if files[i].Name == "cmd/standardsctl/main.go" {
			files[i].Data = []byte("//go:embed undeclared.txt\npackage main\nfunc main() {}\n")
			break
		}
	}
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatalf("encode fixture archive: %v", err)
	}
	_, err = decodeBootstrapArchive(t.Context(), archive)
	if err == nil {
		t.Fatal("decode accepted archive with undeclared embed directive")
	}
	if !strings.Contains(err.Error(), "explicit asset capture is required") {
		t.Errorf("error %q does not mention explicit asset capture", err.Error())
	}
	if !strings.Contains(err.Error(), "praetorctl devcontainer generate --source-root <reviewed Praetor checkout> --force") {
		t.Errorf("error %q does not name the remedy", err.Error())
	}
}

func TestFigurePreMoveFixtureArchive_Negative_BadReleaseDigest(t *testing.T) {
	files := figurePreMoveFixtureFiles(t)
	target := "tools/figures/dist/loader.js"
	found := false
	for i := range files {
		if files[i].Name == target {
			files[i].Data = []byte("tampered content matching no release digest\n")
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("target %s not found in fixture files", target)
	}
	archive, err := encodeBootstrapArchive(files)
	if err != nil {
		t.Fatalf("encode fixture archive: %v", err)
	}
	_, err = decodeBootstrapArchive(t.Context(), archive)
	if err == nil {
		t.Fatal("decode accepted archive with tampered dist member")
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error %q does not name the tampered file %s", err.Error(), target)
	}
	if !strings.Contains(err.Error(), "bytes match no release digest") {
		t.Errorf("error %q does not mention release digest mismatch", err.Error())
	}
}

func TestFigurePreMoveFixtureArchive_Negative_MixPreMoveAndPlayer(t *testing.T) {
	files := figurePreMoveFixtureFiles(t)
	mixed := append(files, bootstrapSourceFile{
		Name: "tools/figures/player/loader.js",
		Data: []byte("unexpected player loader\n"),
	})
	archive, err := encodeBootstrapArchive(mixed)
	if err != nil {
		t.Fatalf("encode fixture archive: %v", err)
	}
	_, err = decodeBootstrapArchive(t.Context(), archive)
	if err == nil {
		t.Fatal("decode accepted mixed archive (pre-move directive + player/ member)")
	}
	if !strings.Contains(err.Error(), "tools/figures/player/loader.js") {
		t.Errorf("error %q does not name the unselected member", err.Error())
	}
	if !strings.Contains(err.Error(), "does not belong to the selected") {
		t.Errorf("error %q does not describe declaration mismatch", err.Error())
	}
}

func TestFigurePreMoveFixtureArchive_Boundary_GenerateNeverEmitsPreMove(t *testing.T) {
	root := bootstrapSourceFixture(t)
	figureFamily := findFigureFamily(t)
	preMoveFam, ok := preMoveBootstrapFamily(figureFamily)
	if !ok {
		t.Fatal("failed to derive pre-move figure family")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	assetsGoData, err := os.ReadFile(filepath.Join(repoRoot, figureFamily.Source))
	if err != nil {
		t.Fatal(err)
	}
	writeBootstrapFile(t, root, "internal/util/util.go", "package util\n")
	writeBootstrapFile(t, root, figureFamily.Source, string(assetsGoData))
	for _, name := range figureFamily.Names() {
		data, err := figureFamily.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		writeBootstrapFile(t, root, figureFamily.AssetPath(name), string(data))
	}
	writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "package main\nimport _ \"github.com/cordanaLLM/praetor/tools/figures\"\nfunc main() {}\n")

	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatalf("capture bootstrap source: %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name, "tools/figures/dist/") {
			t.Errorf("generate emitted pre-move asset %s", f.Name)
		}
		if f.Name == figureFamily.Source && strings.Contains(string(f.Data), preMoveFam.directive) {
			t.Errorf("generate emitted pre-move embed directive")
		}
	}
	preMoveAssetsGo := bytes.Replace(assetsGoData, []byte(figureFamily.EmbedDirective()), []byte(preMoveFam.directive), 1)
	writeBootstrapFile(t, root, figureFamily.Source, string(preMoveAssetsGo))
	if _, err := captureBootstrapSource(t.Context(), root); err == nil {
		t.Fatal("capture accepted pre-move embed directive in source checkout")
	}
}

func TestPriorReleaseBundleVerifiesAgainstCurrentEngine(t *testing.T) {
	fixtureDir := filepath.Join("testdata", "prior-release-bundle")
	configPath := filepath.Join(fixtureDir, "devcontainer.json")
	data, actual, err := readBootstrapConfig(t.Context(), configPath)
	if err != nil {
		t.Fatalf("read fixture devcontainer config: %v", err)
	}
	if err := verifyRecordedBootstrap(t.Context(), configPath, data, actual, actual); err != nil {
		t.Fatalf("bundle recorded at previous engine release failed verification: %v", err)
	}
}
