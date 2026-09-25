package gomanifest

import (
	"strings"
	"testing"
)

const praetorManifest = "module github.com/cordanaLLM/praetor\n\ngo 1.27\n\nrequire gopkg.in/yaml.v3 v3.0.1\n"

// Positive: the directive is what every toolchain pin is compared against, so it must be
// read exactly as written, patch level included.
func TestGoDirective_Positive_ReadsTheDeclaredVersion(t *testing.T) {
	version, declared := GoDirective([]byte(praetorManifest))
	if !declared || version != "1.27" {
		t.Fatalf("GoDirective = %q, %v; want 1.27, true", version, declared)
	}
	patched := strings.Replace(praetorManifest, "go 1.27", "go 1.27.1", 1)
	if version, declared = GoDirective([]byte(patched)); !declared || version != "1.27.1" {
		t.Fatalf("patch level: GoDirective = %q, %v; want 1.27.1, true", version, declared)
	}
}

// Negative: a manifest that declares no directive reports so rather than an empty version
// a caller could mistake for one.
func TestGoDirective_Negative_ReportsAManifestWithoutADirective(t *testing.T) {
	cases := []string{
		"module github.com/cordanaLLM/praetor\n",
		"module x\n\nrequire (\n\tgo.uber.org/zap v1.27.0\n\tgolang.org/x/mod v0.21.0\n)\n",
		"",
		"go\n",
	}
	for _, manifest := range cases {
		if version, declared := GoDirective([]byte(manifest)); declared {
			t.Errorf("%q: reported directive %q", manifest, version)
		}
	}
}

// Boundary: the forms a manifest may legally carry around the directive -- a trailing
// comment, CRLF line endings, a commented-out directive above the real one, and a
// toolchain line below it -- all resolve to the one version.
func TestGoDirective_Boundary_TrailingCommentsCRLFAndToolchainLines(t *testing.T) {
	cases := map[string]string{
		"trailing comment": "module x\n\ngo 1.27 // pinned by ADR-0005\n",
		"crlf":             "module x\r\n\r\ngo 1.27\r\n",
		"commented out":    "module x\n\n// go 1.24\ngo 1.27\n",
		"toolchain below":  "module x\n\ngo 1.27\n\ntoolchain go1.27.1\n",
		"extra spacing":    "module x\n\n  go   1.27  \n",
	}
	for name, manifest := range cases {
		version, declared := GoDirective([]byte(manifest))
		if !declared || version != "1.27" {
			t.Errorf("%s: GoDirective = %q, %v; want 1.27, true", name, version, declared)
		}
	}
}

func TestReplaceLine_Positive_SingleAndBlock(t *testing.T) {
	inBlock := false
	line, ok := ReplaceLine("replace old.example/a => new.example/a v1.2.3", &inBlock)
	if !ok || line != "old.example/a => new.example/a v1.2.3" {
		t.Fatalf("single-line replace: line=%q ok=%v", line, ok)
	}
	if inBlock {
		t.Fatal("single-line replace must not open a block")
	}

	inBlock = false
	if _, ok := ReplaceLine("replace (", &inBlock); ok || !inBlock {
		t.Fatalf("block opener must open the block and report no directive line, inBlock=%v", inBlock)
	}
	line, ok = ReplaceLine("\told.example/b v1.0.0 => new.example/b v2.0.0", &inBlock)
	if !ok || line != "old.example/b v1.0.0 => new.example/b v2.0.0" {
		t.Fatalf("block-body replace: line=%q ok=%v", line, ok)
	}
	if _, ok := ReplaceLine(")", &inBlock); ok || inBlock {
		t.Fatalf("block closer must close the block, inBlock=%v", inBlock)
	}
}

func TestReplaceLine_Negative_NilStateAndUnrelatedLine(t *testing.T) {
	if line, ok := ReplaceLine("replace old => new v1.0.0", nil); ok || line != "" {
		t.Fatalf("nil state must be refused, got line=%q ok=%v", line, ok)
	}
	inBlock := false
	if _, ok := ReplaceLine("require old.example/a v1.0.0", &inBlock); ok {
		t.Fatal("a require line outside a replace block must not be reported as a replace directive")
	}
}

func TestReplaceLine_Boundary_EmptyAndComment(t *testing.T) {
	inBlock := false
	if line, ok := ReplaceLine("", &inBlock); ok || line != "" {
		t.Fatalf("empty line: line=%q ok=%v", line, ok)
	}
	if line, ok := ReplaceLine("  // a comment", &inBlock); ok || line != "" {
		t.Fatalf("comment line: line=%q ok=%v", line, ok)
	}
}

func TestParseReplaceDirective_Positive_VersionedAndLocal(t *testing.T) {
	got, ok := ParseReplaceDirective("old.example/a => new.example/a v1.2.3")
	want := ReplaceDirective{OldPath: "old.example/a", NewPath: "new.example/a", NewVersion: "v1.2.3"}
	if !ok || got != want {
		t.Fatalf("versioned: got %+v ok=%v, want %+v", got, ok, want)
	}

	got, ok = ParseReplaceDirective("old.example/a v1.0.0 => ../local/a")
	want = ReplaceDirective{OldPath: "old.example/a", OldVersion: "v1.0.0", NewPath: "../local/a"}
	if !ok || got != want {
		t.Fatalf("local: got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestParseReplaceDirective_Negative_NoArrow(t *testing.T) {
	if _, ok := ParseReplaceDirective("old.example/a new.example/a v1.2.3"); ok {
		t.Fatal("a line without '=>' must be refused")
	}
}

func TestParseReplaceDirective_Boundary_EmptySidesAndBothVersions(t *testing.T) {
	if _, ok := ParseReplaceDirective(" => new.example/a v1.0.0"); ok {
		t.Fatal("an empty left side must be refused")
	}
	if _, ok := ParseReplaceDirective("old.example/a => "); ok {
		t.Fatal("an empty right side must be refused")
	}
	// The left-hand version scopes the directive and must survive parsing.
	got, ok := ParseReplaceDirective("old.example/a v1.0.0 => new.example/a v2.0.0 // pinned")
	want := ReplaceDirective{OldPath: "old.example/a", OldVersion: "v1.0.0", NewPath: "new.example/a", NewVersion: "v2.0.0"}
	if !ok || got != want {
		t.Fatalf("both versions: got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestModulePath_Positive(t *testing.T) {
	path, ok := ModulePath("module github.com/cordanaLLM/praetor")
	if !ok || path != "github.com/cordanaLLM/praetor" {
		t.Fatalf("path=%q ok=%v", path, ok)
	}
}

func TestModulePath_Negative_NotAModuleLine(t *testing.T) {
	if path, ok := ModulePath("go 1.27"); ok || path != "" {
		t.Fatalf("non-module line must be refused, got path=%q ok=%v", path, ok)
	}
}

func TestModulePath_Boundary_EmptyPath(t *testing.T) {
	if path, ok := ModulePath("module "); ok || path != "" {
		t.Fatalf("a module directive with no path must be refused, got path=%q ok=%v", path, ok)
	}
	if path, ok := ModulePath(""); ok || path != "" {
		t.Fatalf("empty line: path=%q ok=%v", path, ok)
	}
}
