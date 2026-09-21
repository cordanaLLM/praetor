package markdownlint

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLockedAssetInventory(t *testing.T) {
	want := []string{"package.json", "package-lock.json", "markdownlint-cli2.yaml", "verify.mjs", "no-private-scratch-links.mjs"}
	got := Names()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("asset names = %v, want %v", got, want)
	}
	if _, err := Read("outside"); err == nil {
		t.Fatal("unknown asset accepted")
	}
}

func TestEmbeddedAssetCheckoutBytesPinnedToLF(t *testing.T) {
	attributes, err := os.ReadFile("../../.gitattributes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(attributes), "\ntools/markdownlint/* text eol=lf\n") {
		t.Fatal("embedded Markdown gate assets lack a platform-neutral LF checkout policy")
	}
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\r") {
			t.Fatalf("embedded Markdown gate asset %s contains a carriage return", name)
		}
	}
}

func TestPackageLockPinsEveryInstalledPackage(t *testing.T) {
	wantDirect := map[string]string{
		"markdownlint-cli2":         "0.23.2",
		"micromark":                 "4.0.2",
		"micromark-extension-mdxjs": "3.0.0",
		"parse5":                    "8.0.1",
	}
	manifestData, err := Read("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode package manifest: %v", err)
	}
	assertDirectDependencies(t, manifest.Dependencies, wantDirect)

	data, err := Read("package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version      string            `json:"version"`
			Resolved     string            `json:"resolved"`
			Integrity    string            `json:"integrity"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("decode package lock: %v", err)
	}
	if lock.LockfileVersion != 3 {
		t.Fatalf("lockfileVersion = %d, want 3", lock.LockfileVersion)
	}
	assertDirectDependencies(t, lock.Packages[""].Dependencies, wantDirect)
	for name, pkg := range lock.Packages {
		if name == "" {
			continue
		}
		if pkg.Version == "" || pkg.Resolved == "" || pkg.Integrity == "" {
			t.Fatalf("%s lacks exact version, source, or integrity", name)
		}
	}
	if lock.Packages["node_modules/markdownlint-cli2"].Version != "0.23.2" {
		t.Fatal("markdownlint-cli2 is not pinned to 0.23.2")
	}
	if lock.Packages["node_modules/micromark"].Version != "4.0.2" {
		t.Fatal("micromark is not pinned to 4.0.2")
	}
	if lock.Packages["node_modules/micromark-extension-mdx-jsx"].Version != "3.0.2" {
		t.Fatal("micromark-extension-mdx-jsx is not pinned to 3.0.2")
	}
	if lock.Packages["node_modules/micromark-extension-mdxjs"].Version != "3.0.0" {
		t.Fatal("micromark-extension-mdxjs is not pinned to 3.0.0")
	}
	if lock.Packages["node_modules/parse5"].Version != "8.0.1" {
		t.Fatal("parse5 is not pinned to 8.0.1")
	}
}

func assertDirectDependencies(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("direct dependencies = %v, want %v", got, want)
	}
	for name, version := range want {
		if got[name] != version {
			t.Fatalf("direct dependency %s = %q, want %q", name, got[name], version)
		}
	}
}

func TestRunnerUsesLockedInstallWithoutNpx(t *testing.T) {
	data, err := Read("verify.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"npm.cmd", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "spawnSync", "timeout",
		"MAX_CAPTURE_BYTES", "MAX_DIAGNOSTIC_OUTPUT_BYTES", "MAX_DIAGNOSTIC_OUTPUT_LINES", "emitBounded",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner lacks %q", required)
		}
	}
	if strings.Contains(text, "npx") {
		t.Fatal("runner must not resolve tools through npx")
	}
	for _, required := range []string{
		"const scratchFiles = inventory(root)",
		"const styleFiles = scratchFiles.filter(isStyleSelected)",
		"runScratchRule(root, temporary, scratchFiles, false)",
		"runMarkdownlint(root, temporary, styleFiles)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner does not keep privacy scanning broader than style lint: missing %q", required)
		}
	}
}

func TestPrivateLinkDiagnosticsAreGloballyBounded(t *testing.T) {
	data, err := Read("no-private-scratch-links.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"const MAX_FINDINGS = 64", "MAX_DIAGNOSTIC_FIELD_CHARS", "PRAETOR-MD002", "capFindings",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("private-link rule lacks bounded diagnostic contract %q", required)
		}
	}
}
