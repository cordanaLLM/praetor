package gomanifest

import "testing"

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
	oldPath, newPath, newVersion, ok := ParseReplaceDirective("old.example/a => new.example/a v1.2.3")
	if !ok || oldPath != "old.example/a" || newPath != "new.example/a" || newVersion != "v1.2.3" {
		t.Fatalf("versioned: old=%q new=%q ver=%q ok=%v", oldPath, newPath, newVersion, ok)
	}

	oldPath, newPath, newVersion, ok = ParseReplaceDirective("old.example/a v1.0.0 => ../local/a")
	if !ok || oldPath != "old.example/a" || newPath != "../local/a" || newVersion != "" {
		t.Fatalf("local: old=%q new=%q ver=%q ok=%v", oldPath, newPath, newVersion, ok)
	}
}

func TestParseReplaceDirective_Negative_NoArrow(t *testing.T) {
	if _, _, _, ok := ParseReplaceDirective("old.example/a new.example/a v1.2.3"); ok {
		t.Fatal("a line without '=>' must be refused")
	}
}

func TestParseReplaceDirective_Boundary_EmptySides(t *testing.T) {
	if _, _, _, ok := ParseReplaceDirective(" => new.example/a v1.0.0"); ok {
		t.Fatal("an empty left side must be refused")
	}
	if _, _, _, ok := ParseReplaceDirective("old.example/a => "); ok {
		t.Fatal("an empty right side must be refused")
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
