package hiss

import (
	"path/filepath"
	"strings"
	"testing"
)

// rustFnOfLOC returns a Rust function spanning loc lines under the given header prefix,
// for example "pub(super) fn" or `extern "C" fn`.
func rustFnOfLOC(prefix, name string, loc int) string {
	var sb strings.Builder
	sb.WriteString(prefix + " " + name + "(mut n: i32) -> i32 {\n")
	for i := 0; i < loc-2; i++ {
		sb.WriteString("    n += 1;\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

func TestRustTestScope_Positive_ProductionCodeAfterATestItemIsChecked(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/lib.rs", strings.Join([]string{
		"#[cfg(test)]",                         // 1
		"mod tests {",                          // 2
		"    fn t() { let v = opt.unwrap(); }", // 3
		"}",                                    // 4
		"",                                     // 5
		"fn helper() {",                        // 6
		"    let v = opt.unwrap();",            // 7
		"}",                                    // 8
		"#[test]",                              // 9
		"fn top_level_test() { opt.expect(\"x\"); }", // 10
		"fn after() { opt.expect(\"y\"); }",          // 11
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "src/lib.rs", 7}, {"HISS-07", "src/lib.rs", 11}})
}

func TestRustTestScope_Negative_EveryLineOfATestItemIsExempt(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/lib.rs", strings.Join([]string{
		"#[cfg( test )]",
		"#[allow(dead_code)]",
		"mod tests {",
		"    use super::*;",
		"    const BRACE: &str = \"}}}\";",
		"    fn nested() {",
		"        if true { let v = opt.unwrap(); }",
		"    }",
		"    #[tokio::test(flavor = \"multi_thread\")]",
		"    async fn t() { opt.expect(\"x\"); }",
		"    fn last() { let v = opt.unwrap(); }",
		"}",
		"",
	}, "\n"))
	writeFixture(t, root, "src/inner.rs", "#![cfg(test)]\nfn a() { opt.unwrap(); }\nfn b() { opt.expect(\"x\"); }\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("test items must be exempt in full: %+v", rep.Violations)
	}
}

func TestRustTestScope_Boundary_OneLineAndBodylessItems(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/lib.rs", strings.Join([]string{
		"#[cfg(test)] fn fixture() { opt.unwrap(); }", // 1 one-line item, exempt
		"fn prod() { opt.unwrap(); }",                 // 2 the item closed on line 1
		"#[cfg(test)]",                                // 3
		"use crate::fixtures;",                        // 4 a bodyless item ends the attribute
		"fn prod2() { opt.unwrap(); }",                // 5
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "src/lib.rs", 2}, {"HISS-07", "src/lib.rs", 5}})
}

func TestRustFnHeader_Positive_EveryQualifiedHeaderIsMeasured(t *testing.T) {
	headers := []string{
		"pub(super) fn", "const fn", "pub(crate) unsafe fn", `extern "C" fn`, `pub extern "C" fn`,
		"pub const unsafe fn", "async unsafe fn", "pub(in crate::a) fn", "default fn", "pub(crate) async fn",
		"#[inline] pub fn", "#[must_use] #[cfg(feature = \"x\")] const fn",
	}
	for i, prefix := range headers {
		t.Run(prefix, func(t *testing.T) {
			root := t.TempDir()
			name := "measured_" + string(rune('a'+i))
			src := "// SAFETY: no unsafe operation; keeps HISS-09 out of the result.\n" + rustFnOfLOC(prefix, name, 12)
			writeFixture(t, root, "src/lib.rs", src)
			rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 10})
			if rep.Breakdown["HISS-04"] != 1 {
				t.Fatalf("%q must be measured: %+v", prefix, rep.Violations)
			}
			for _, v := range rep.Violations {
				if v.RuleID == "HISS-04" && v.Symbol != name {
					t.Fatalf("%q named %q, want %q", prefix, v.Symbol, name)
				}
			}
		})
	}
}

func TestRustFnHeader_Negative_FnTypesAndLiteralsAreNotHeaders(t *testing.T) {
	for _, line := range []string{
		"let f: fn(i32) -> i32 = g;",
		"type Callback = fn();",
		"let s = \"fn fake() {\";",
		"// fn commented() {",
		"fnord();",
		"self.fn_table.len()",
		"#[cfg(test)] mod tests {",
		"#[derive(Debug)] struct S { f: fn() }",
	} {
		if name, ok := rustFnHeaderName(strings.TrimSpace((&literalStripper{syn: cLikeSyntax}).strip(line))); ok {
			t.Fatalf("%q read as header %q", line, name)
		}
	}
}

func TestRustFnHeader_Boundary_QualifiedHeaderAtTheCap(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/at_cap.rs", rustFnOfLOC("pub(super) const fn", "at_cap", 10))
	writeFixture(t, root, "src/over_cap.rs", rustFnOfLOC("pub(super) const fn", "over_cap", 11))
	writeFixture(t, root, "src/raw.rs", rustFnOfLOC("pub fn", "r#match", 11))

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 10})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "src/over_cap.rs", 1}, {"HISS-04", "src/raw.rs", 1}})
	for _, v := range rep.Violations {
		if filepath.ToSlash(v.FilePath) == "src/raw.rs" && v.Symbol != "match" {
			t.Fatalf("raw identifier named %q, want match", v.Symbol)
		}
	}
}
