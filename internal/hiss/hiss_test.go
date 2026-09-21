package hiss

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanFixture(t *testing.T, root string, opts ScanOptions) *ScanReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rep, err := Scan(ctx, root, opts)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	return rep
}

type expectedViolation struct {
	rule string
	file string
	line int
}

func assertViolations(t *testing.T, rep *ScanReport, want []expectedViolation) {
	t.Helper()
	got := make(map[expectedViolation]int)
	for _, v := range rep.Violations {
		got[expectedViolation{v.RuleID, filepath.ToSlash(v.FilePath), v.LineNumber}]++
	}
	for _, w := range want {
		if got[w] == 0 {
			t.Errorf("missing violation %s at %s:%d; got %+v", w.rule, w.file, w.line, rep.Violations)
		}
	}
	if len(rep.Violations) != len(want) {
		t.Errorf("violation count = %d, want %d: %+v", len(rep.Violations), len(want), rep.Violations)
	}
	if rep.TotalInfractions != len(rep.Violations) {
		t.Errorf("TotalInfractions = %d, want %d", rep.TotalInfractions, len(rep.Violations))
	}
}

func goFuncOfLOC(name string, loc int) string {
	var sb strings.Builder
	sb.WriteString("func " + name + "() {\n")
	for i := 0; i < loc-2; i++ {
		sb.WriteString("\tprintln(\"line\")\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// ---------------------------------------------------------------------------
// ignore lists (F259, F264)
// ---------------------------------------------------------------------------

func TestShouldIgnorePath(t *testing.T) {
	tests := []struct {
		path   string
		ignore bool
	}{
		{"vendor/foo/bar.go", true},
		{"node_modules/pkg/index.js", true},
		{".git/config", true},
		{"core/build/output.o", true},
		{".harvest/bundle.json", true},
		{"internal/testdata/fixture.go", true},
		{"a/b/c/build-release/x.c", true},
		{"src/main.go", false},
		{"internal/util/util.go", false},
		// Project-shaped names are scanned: exemptions are segment-anchored and universal.
		{"harvest/office-kcromm/script.py", false},
		{"internal/model/router.go", false},
		{"pkg/compat/shim.go", false},
		// A file name resembling an ignored directory is still a file.
		{"pkg/build_helpers.go", false},
		{"build_helpers.go", false},
		{"pkg/build-tools.go", false},
		// Substrings never match.
		{"src/modeling/x.go", false},
		{"src/vendored/x.go", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ShouldIgnorePath(tt.path); got != tt.ignore {
			t.Errorf("ShouldIgnorePath(%q) = %v, want %v", tt.path, got, tt.ignore)
		}
	}
}

func TestShouldIgnoreDir(t *testing.T) {
	tests := []struct {
		name, rel string
		ignore    bool
	}{
		{"vendor", "vendor", true},
		{"VENDOR", "VENDOR", true},
		{"node_modules", "web/node_modules", true},
		{"build-debug", "core/build-debug", true},
		{"build_x", "build_x", true},
		{"x", "target/x", true},
		{"testdata", "internal/hiss/testdata", true},
		{".git", ".git", true},
		{"model", "internal/model", false},
		{"compat", "compat", false},
		{"harvest", "harvest", false},
		{"builder", "builder", false},
		{"internal", "internal", false},
		{"x", "", false},
	}
	for _, tt := range tests {
		if got := ShouldIgnoreDir(tt.name, tt.rel); got != tt.ignore {
			t.Errorf("ShouldIgnoreDir(%q, %q) = %v, want %v", tt.name, tt.rel, got, tt.ignore)
		}
	}
}

func TestScan_IgnoreDirsOptionAndSkipAccounting(t *testing.T) {
	root := t.TempDir()
	body := "package m\n\nfunc f() {\n\tpanic(\"x\")\n}\n"
	writeFixture(t, root, "internal/model/x.go", body)
	writeFixture(t, root, "vendor/dep/y.go", body)
	writeFixture(t, root, ".git/hooks/z.go", body)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "internal/model/x.go", 4}})
	if rep.Skips.DirCount != 2 || len(rep.Skips.Dirs) != 2 {
		t.Errorf("expected 2 skipped dirs, got %+v", rep.Skips)
	}

	rep = scanFixture(t, root, ScanOptions{IgnoreDirs: []string{" Model "}})
	assertViolations(t, rep, nil)
	if rep.Skips.DirCount != 3 {
		t.Errorf("expected model/ to be skipped via IgnoreDirs, got %+v", rep.Skips)
	}
	found := false
	for _, d := range rep.Skips.Dirs {
		if d == "internal/model" {
			found = true
		}
	}
	if !found {
		t.Errorf("skipped dirs should list internal/model, got %v", rep.Skips.Dirs)
	}
}

// ---------------------------------------------------------------------------
// per-language rule attribution (F266, F671)
// ---------------------------------------------------------------------------

func TestScan_CleanFixtureYieldsZero(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "clean.go", "package clean\n\nimport \"os\"\n\nfunc Read(p string) ([]byte, error) {\n\tdata, err := os.ReadFile(p)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\t_, statErr := os.Stat(p)\n\tif statErr != nil {\n\t\treturn nil, statErr\n\t}\n\tfor i := 0; i < 3; i++ {\n\t\tprintln(i)\n\t}\n\treturn data, nil\n}\n")
	writeFixture(t, root, "clean.py", "def retrieval(n):\n    try:\n        return n\n    except ValueError:\n        return 0\n\n\nwhile_true_name = 1\n")
	writeFixture(t, root, "clean.rs", "fn main() -> Result<(), String> {\n    let v: Option<i32> = Some(1);\n    match v {\n        Some(x) => println!(\"{}\", x),\n        None => {}\n    }\n    // SAFETY: the pointer is valid for the whole call.\n    unsafe { touch() }\n    Ok(())\n}\n")
	writeFixture(t, root, "clean.c", "#include <stdio.h>\n\nint main(void) {\n    char buf[8];\n    fgets(buf, sizeof buf, stdin);\n    snprintf(buf, sizeof buf, \"x\");\n    strncpy(buf, \"y\", 2);\n    return 0;\n}\n")

	rep := scanFixture(t, root, ScanOptions{})
	if rep.TotalInfractions != 0 || len(rep.Violations) != 0 {
		t.Fatalf("clean fixture must yield 0 violations, got %+v", rep.Violations)
	}
	if rep.Truncated {
		t.Error("clean scan must not be truncated")
	}
}

func TestScan_GoRulesAreAttributed(t *testing.T) {
	root := t.TempDir()
	src := strings.Join([]string{
		"package p",                  // 1
		"",                           // 2
		"import \"unsafe\"",          // 3
		"",                           // 4
		"func a() {",                 // 5
		"\tfor {",                    // 6
		"\t}",                        // 7
		"\tfor i := 0; ; i++ {",      // 8
		"\t}",                        // 9
		"\tfor i := 0; i < 2; i++ {", // 10
		"\t}",                        // 11
		"\t_ = a",                    // 12
		"\t_, _ = b()",               // 13
		"\t_, err := b()",            // 14
		"\tif err != nil {",          // 15
		"\t}",                        // 16
		"\tif err != nil { return }", // 17
		"\tpanic(\"x\")",             // 18
		"\tgoto end",                 // 19
		"end:",                       // 20
		"\tp := unsafe.Pointer(nil)", // 21
		"\t// SAFETY: proof",         // 22
		"\tq := unsafe.Pointer(nil)", // 23
		"\t_, _ = p, q",              // 24
		"\ts := \"{ panic( for { \"", // 25
		"\t_ = s",                    // 26
		"}",                          // 27
		"",                           // 28
		"func b() (int, error) { return 0, nil }", // 29
		"",
	}, "\n")
	writeFixture(t, root, "p.go", src)

	rep := scanFixture(t, root, ScanOptions{})
	// Lines 12, 24 and 26 discard a function value, two pointers and a string. None is a
	// call and none can carry an error, so HISS-07 does not report them; asserting that it
	// did encoded three false positives as the contract.
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "p.go", 6},
		{"HISS-02", "p.go", 8},
		{"HISS-07", "p.go", 13},
		{"HISS-07", "p.go", 15},
		{"HISS-07", "p.go", 18},
		{"HISS-01", "p.go", 19},
		{"HISS-09", "p.go", 21},
	})
}

func TestScan_GoTestFilesAreExemptFromErrorAndLengthRules(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\n" + goFuncOfLOC("TestLong", 90) + "\nfunc helper() {\n\t_ = 1\n\tpanic(\"x\")\n\tgoto end\nend:\n}\n"
	writeFixture(t, root, "p_test.go", src)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-01", "p_test.go", 97}})
}

func TestScan_GoSyntaxErrorScansPartialAST(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "broken.go", "package p\n\nfunc ok() {\n\tpanic(\"x\")\n}\n\nfunc broken( {\n")
	writeFixture(t, root, "notgo.go", "this is not go at all\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "broken.go", 4}})
}

func TestScan_PythonRulesAreAttributed(t *testing.T) {
	root := t.TempDir()
	src := strings.Join([]string{
		"def loop():",        // 1
		"    while True:",    // 2
		"        pass",       // 3
		"",                   // 4
		"def dyn(s):",        // 5
		"    eval(s)",        // 6
		"    exec(s)",        // 7
		"    retrieval(s)",   // 8
		"    x = 1",          // 9
		"    try:",           // 10
		"        x = 2",      // 11
		"    except:",        // 12
		"        pass",       // 13
		"    except: # bare", // 14
		"        pass",       // 15
		"",
	}, "\n")
	writeFixture(t, root, "m.py", src)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "m.py", 2},
		{"HISS-08", "m.py", 6},
		{"HISS-08", "m.py", 7},
		{"HISS-07", "m.py", 12},
		{"HISS-07", "m.py", 14},
	})
}

func TestScan_RustRulesAreAttributed(t *testing.T) {
	root := t.TempDir()
	src := strings.Join([]string{
		"fn main() {",                        // 1
		"    loop {",                         // 2
		"    }",                              // 3
		"    let a = opt.unwrap();",          // 4
		"    let b = opt.expect(\"x\");",     // 5
		"    unsafe {",                       // 6
		"    }",                              // 7
		"    // SAFETY: ptr is non-null.",    // 8
		"    unsafe {",                       // 9
		"    }",                              // 10
		"    let s = \"{ unsafe { loop {\";", // 11
		"}",                                  // 12
		"",                                   // 13
		"// SAFETY: caller upholds the contract.", // 14
		"unsafe fn raw() {}",                      // 15
		"unsafe fn bare() {}",                     // 16
		"",
	}, "\n")
	writeFixture(t, root, "src/main.rs", src)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "src/main.rs", 2},
		{"HISS-07", "src/main.rs", 4},
		{"HISS-07", "src/main.rs", 5},
		{"HISS-09", "src/main.rs", 6},
		{"HISS-09", "src/main.rs", 16},
	})
}

func TestScan_RustTestExemptionFollowsCargoConventions(t *testing.T) {
	root := t.TempDir()
	unwrap := "fn f() {\n    let v = opt.unwrap();\n    let w = opt.expect(\"x\");\n}\n"
	writeFixture(t, root, "src/attestation.rs", unwrap)
	writeFixture(t, root, "src/latest.rs", unwrap)
	writeFixture(t, root, "crates/protest/src/lib.rs", unwrap)
	writeFixture(t, root, "tests/integration.rs", unwrap)
	writeFixture(t, root, "benches/bench.rs", unwrap)
	writeFixture(t, root, "src/unit_test.rs", unwrap)
	writeFixture(t, root, "src/cfg.rs", "fn prod() {\n    let v = opt.unwrap();\n}\n\n#[cfg(test)]\nmod tests {\n    fn t() {\n        let v = opt.unwrap();\n    }\n}\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "src/attestation.rs", 2}, {"HISS-07", "src/attestation.rs", 3},
		{"HISS-07", "src/latest.rs", 2}, {"HISS-07", "src/latest.rs", 3},
		{"HISS-07", "crates/protest/src/lib.rs", 2}, {"HISS-07", "crates/protest/src/lib.rs", 3},
		{"HISS-07", "src/cfg.rs", 2},
	})
}

func TestScan_NativeRulesAreAttributed(t *testing.T) {
	root := t.TempDir()
	src := strings.Join([]string{
		"#include <string.h>",                  // 1
		"",                                     // 2
		"void f(char *dst, const char *src) {", // 3
		"    while (1) {",                      // 4
		"    }",                                // 5
		"    for (;;) {",                       // 6
		"    }",                                // 7
		"    strcpy(dst, src);",                // 8
		"    gets(dst);",                       // 9
		"    fgets(dst, 1, stdin);",            // 10
		"    sprintf(dst, \"x\");",             // 11
		"    snprintf(dst, 1, \"x\");",         // 12
		"    goto out;",                        // 13
		"out:",                                 // 14
		"    return;",                          // 15
		"}",                                    // 16
		"",
	}, "\n")
	writeFixture(t, root, "f.c", src)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "f.c", 4},
		{"HISS-02", "f.c", 6},
		{"HISS-08", "f.c", 8},
		{"HISS-08", "f.c", 9},
		{"HISS-08", "f.c", 11},
		{"HISS-01", "f.c", 13},
	})
}

func TestHissScanRules(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "test.py", "def loop_func():\n    while True:\n        pass\n\ndef error_func():\n    try:\n        x = 1\n    except:\n        pass\n")
	writeFixture(t, root, "test.go", "package test\n\nfunc infinite() {\n\tfor {\n\t\twork()\n\t}\n}\n\nfunc panicky() {\n\tpanic(\"crash\")\n}\n")
	writeFixture(t, root, "test.rs", "fn do_something() {\n    let opt = Some(1);\n    let val = opt.unwrap();\n    unsafe {\n        println!(\"unsafe\");\n    }\n}\n")
	writeFixture(t, root, "test.c", "#include <string.h>\n\nvoid allocate(char *dst, const char *src) {\n    start:\n    strcpy(dst, src);\n    goto start;\n}\n")

	report := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60, Cap: 100})
	for _, r := range []string{"HISS-01", "HISS-02", "HISS-07", "HISS-08", "HISS-09"} {
		if report.Breakdown[r] == 0 {
			t.Errorf("expected rule %s to be flagged; breakdown: %+v", r, report.Breakdown)
		}
	}

	converted := ConvertToBaseline(report.Violations)
	if len(converted) != len(report.Violations) {
		t.Fatalf("ConvertToBaseline count mismatch: got %d, want %d", len(converted), len(report.Violations))
	}
	for i, v := range report.Violations {
		c := converted[i]
		if c.RuleID != v.RuleID || c.FilePath != v.FilePath || c.LineNumber != v.LineNumber ||
			c.Symbol != v.Symbol || c.Message != v.Message || c.Fingerprint != "" {
			t.Errorf("ConvertToBaseline[%d] = %+v, want fields of %+v", i, c, v)
		}
	}
}

// ---------------------------------------------------------------------------
// HISS-04 length: boundaries and desync regressions (F267, F269, F270)
// ---------------------------------------------------------------------------

func TestScan_LOCBoundaryPerLanguage(t *testing.T) {
	pyFunc := func(loc int) string {
		var sb strings.Builder
		sb.WriteString("def f():\n")
		for i := 0; i < loc-1; i++ {
			sb.WriteString("    x = 1\n")
		}
		return sb.String()
	}
	braceFunc := func(header string, loc int) string {
		var sb strings.Builder
		sb.WriteString(header + " {\n")
		for i := 0; i < loc-2; i++ {
			sb.WriteString("    x = 1;\n")
		}
		sb.WriteString("}\n")
		return sb.String()
	}
	cases := []struct {
		file string
		at   string
		over string
	}{
		{"f.go", "package p\n\n" + goFuncOfLOC("f", 60), "package p\n\n" + goFuncOfLOC("f", 61)},
		{"f.py", pyFunc(60), pyFunc(61)},
		{"f.rs", braceFunc("fn f()", 60), braceFunc("fn f()", 61)},
		{"f.c", braceFunc("void f(void)", 60), braceFunc("void f(void)", 61)},
	}
	for _, c := range cases {
		root := t.TempDir()
		writeFixture(t, root, c.file, c.at)
		if rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60}); rep.Breakdown["HISS-04"] != 0 {
			t.Errorf("%s: exactly 60 LOC must pass, got %+v", c.file, rep.Violations)
		}
		root = t.TempDir()
		writeFixture(t, root, c.file, c.over)
		rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
		if rep.Breakdown["HISS-04"] != 1 || rep.Violations[0].Symbol != "f" || rep.Violations[0].LineNumber != strings.Count(c.over[:strings.Index(c.over, "f(")], "\n")+1 {
			t.Errorf("%s: 61 LOC must fail HISS-04 on the header line, got %+v", c.file, rep.Violations)
		}
	}
}

func TestHissScanLongFunction(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "long.go", "package test\n\n"+goFuncOfLOC("veryLongFunc", 72))
	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	if rep.Breakdown["HISS-04"] != 1 {
		t.Errorf("expected one HISS-04 violation for a 72 LOC function, got %+v", rep.Violations)
	}
}

func TestScan_GoBracesInLiteralsDoNotDesync(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\nimport \"strings\"\n\nfunc header(line string) bool {\n\treturn strings.Contains(line, \"{\") && line != \"'}'\"\n}\n\n" + goFuncOfLOC("appended", 80)
	writeFixture(t, root, "rules.go", src)
	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "rules.go", 9}})
}

func TestScan_NativeAndRustBracesInLiteralsDoNotDesync(t *testing.T) {
	root := t.TempDir()
	long := func(header string) string {
		var sb strings.Builder
		sb.WriteString(header + " {\n")
		for i := 0; i < 70; i++ {
			sb.WriteString("    x = 1;\n")
		}
		sb.WriteString("}\n")
		return sb.String()
	}
	writeFixture(t, root, "a.c", "int header(const char *s) {\n    return s[0] == '{' || strcmp(s, \"{\") == 0; // {\n}\n\n"+long("void appended(void)"))
	writeFixture(t, root, "a.rs", "fn header<'a>(s: &'a str) -> bool {\n    s == \"{\" || s.starts_with('{') // {\n}\n\n"+long("fn appended()"))
	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "a.c", 5}, {"HISS-04", "a.rs", 5}})
}

func TestScan_NativeNonFunctionBlocksAreNotFunctions(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		prefix string
		line   string
		suffix string
	}{
		{"anonymous namespace", "namespace.cpp", "namespace {\n", "int value;\n", "}\n"},
		{"C linkage block", "linkage.cpp", "extern \"C\" {\n", "void exported(void);\n", "}\n"},
		{"file-scope array initializer", "options.c", "static const int options[] = {\n", "    0,\n", "};\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, tc.file, tc.prefix+strings.Repeat(tc.line, 76)+tc.suffix)
			rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
			if rep.Breakdown["HISS-04"] != 0 {
				t.Fatalf("non-function block must not be measured as a function: %+v", rep.Violations)
			}
		})
	}
}

func TestScan_NativeNonFunctionBlocksRetainFunctionChecks(t *testing.T) {
	root := t.TempDir()
	longFunction := "void long_function(void) {\n" + strings.Repeat("    value = 1;\n", 59) + "}\n"
	writeFixture(t, root, "nested.cpp", "namespace {\n"+longFunction+"}\n")

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "nested.cpp", 2}})
	if rep.Violations[0].Symbol != "long_function" {
		t.Fatalf("long nested function attributed to %q", rep.Violations[0].Symbol)
	}
}

func TestScan_NativeAllmanFunctionOwnsNestedControlBlocks(t *testing.T) {
	root := t.TempDir()
	body := "void allman(void)\n{\n    int value = 0;\n    while (value < 100) {\n" +
		strings.Repeat("        value += 1;\n", 59) + "    }\n}\n"
	writeFixture(t, root, "allman.cpp", body)

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "allman.cpp", 1}})
	if rep.Violations[0].Symbol != "allman" {
		t.Fatalf("Allman-style function attributed to %q", rep.Violations[0].Symbol)
	}
}

func TestScan_NativeMacroCallDoesNotBecomePendingFunction(t *testing.T) {
	root := t.TempDir()
	prefix := "#define fail(err) \\\n    throw std::runtime_error(err); \\\n\nclass Parser {\n"
	longFunction := "void parse(void) {\n" + strings.Repeat("    value = 1;\n", 59) + "}\n"
	writeFixture(t, root, "macro.cpp", prefix+longFunction+"};\n")

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "macro.cpp", strings.Count(prefix, "\n") + 1}})
	if rep.Violations[0].Symbol != "parse" {
		t.Fatalf("function after continued macro attributed to %q", rep.Violations[0].Symbol)
	}
}

func TestScan_NativeFunctionAtLOCBoundaryInsideLinkageBlock(t *testing.T) {
	root := t.TempDir()
	exactFunction := "void exact_limit(void) {\n" + strings.Repeat("    value = 1;\n", 58) + "}\n"
	writeFixture(t, root, "boundary.cpp", "extern \"C\" {\n"+exactFunction+"}\n")

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	if rep.Breakdown["HISS-04"] != 0 {
		t.Fatalf("function at exact LOC limit must pass: %+v", rep.Violations)
	}
}

func TestScan_WrappedSignaturesAreTracked(t *testing.T) {
	root := t.TempDir()
	body := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString("\tx = 1\n")
		}
		return sb.String()
	}
	writeFixture(t, root, "w.go", "package p\n\nfunc Process(\n\tctx int,\n\tcfg int,\n) error {\n"+body(70)+"\treturn nil\n}\n")
	writeFixture(t, root, "w.rs", "trait T {\n    fn declared(&self) -> i32;\n}\n\npub fn process(\n    ctx: i32,\n    cfg: i32,\n) -> i32 {\n"+strings.ReplaceAll(body(70), "x = 1", "let x = 1;")+"    0\n}\n")
	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "w.go", 3}, {"HISS-04", "w.rs", 5}})
	for _, v := range rep.Violations {
		if v.Symbol != "Process" && v.Symbol != "process" {
			t.Errorf("wrapped signature attributed to %q", v.Symbol)
		}
	}
}

func TestScan_PythonTrailingBlanksAndNestedDefs(t *testing.T) {
	root := t.TempDir()
	var trailing strings.Builder
	trailing.WriteString("def f():\n")
	for i := 0; i < 57; i++ {
		trailing.WriteString("    x = 1\n")
	}
	trailing.WriteString("\n\n\n\n# comment\nprint(1)\n")
	writeFixture(t, root, "a.py", trailing.String())

	var nested strings.Builder
	nested.WriteString("def outer():\n    def inner():\n        pass\n")
	for i := 0; i < 100; i++ {
		nested.WriteString("    x = 1\n")
	}
	writeFixture(t, root, "b.py", nested.String())

	writeFixture(t, root, "c.py", "class K:\n    def m(self):\n        return 1\n\n    def n(self):\n        return 2\n")

	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "b.py", 1}})
	if rep.Violations[0].Symbol != "outer" {
		t.Errorf("nested def must not end the outer function, got %+v", rep.Violations[0])
	}
}

// ---------------------------------------------------------------------------
// Scan negative and boundary paths (F255, F257, F258, F261, F275)
// ---------------------------------------------------------------------------

func TestScan_NonexistentRootIsAnError(t *testing.T) {
	rep, err := Scan(context.Background(), filepath.Join(t.TempDir(), "missing"), ScanOptions{})
	if err == nil || rep != nil {
		t.Fatalf("expected error for a nonexistent root, got rep=%+v err=%v", rep, err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error must wrap the root stat failure, got %v", err)
	}
}

func TestScan_UnreadableSubtreeIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced for this user")
	}
	root := t.TempDir()
	writeFixture(t, root, "ok.go", "package p\n")
	locked := filepath.Join(root, "locked")
	writeFixture(t, root, "locked/hidden.go", "package p\n\nfunc f() {\n\tpanic(\"x\")\n}\n")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Error(err)
		}
	})
	if _, err := Scan(context.Background(), root, ScanOptions{}); err == nil {
		t.Fatal("an unreadable subtree must fail the scan instead of reporting a clean tree")
	}
}

func TestScan_ContextGuards(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package p\n")

	var nilCtx context.Context
	if _, err := Scan(nilCtx, root, ScanOptions{}); err == nil {
		t.Error("nil context must be rejected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, root, ScanOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context must surface context.Canceled, got %v", err)
	}

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := Scan(expired, root, ScanOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expired deadline must surface DeadlineExceeded, got %v", err)
	}
}

func TestScanContext_AppliesTimeoutPolicy(t *testing.T) {
	before := time.Now()
	ctx, cancel := scanContext(context.Background(), 0)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(before.Add(DefaultScanTimeout-time.Second)) || deadline.After(before.Add(DefaultScanTimeout+time.Second)) {
		t.Errorf("deadline-free context must receive DefaultScanTimeout, got %v ok=%v", deadline, ok)
	}

	explicit, cancelExplicit := scanContext(context.Background(), 2*time.Second)
	defer cancelExplicit()
	deadline, ok = explicit.Deadline()
	if !ok || deadline.After(before.Add(3*time.Second)) {
		t.Errorf("explicit Timeout must bound the scan, got %v ok=%v", deadline, ok)
	}

	parent, cancelParent := context.WithTimeout(context.Background(), time.Hour)
	defer cancelParent()
	kept, cancelKept := scanContext(parent, 0)
	defer cancelKept()
	parentDeadline, _ := parent.Deadline()
	if deadline, ok = kept.Deadline(); !ok || !deadline.Equal(parentDeadline) {
		t.Errorf("a caller deadline must be preserved, got %v want %v", deadline, parentDeadline)
	}

	explicitTimeout := time.Nanosecond
	tiny, cancelTiny := scanContext(parent, explicitTimeout)
	defer cancelTiny()
	<-tiny.Done()
	if _, err := Scan(tiny, t.TempDir(), ScanOptions{Timeout: explicitTimeout}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("an expired scan timeout must fail the scan, got %v", err)
	}
}

func TestScan_CapMarksTruncation(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.go", "package p\n\nfunc a() {\n\tpanic(1)\n\tpanic(2)\n\tpanic(3)\n}\n")
	writeFixture(t, root, "b.go", "package p\n\nfunc b() {\n\tpanic(1)\n\tpanic(2)\n}\n")

	rep := scanFixture(t, root, ScanOptions{Cap: 2})
	if !rep.Truncated || len(rep.Violations) != 2 || rep.TotalInfractions != 2 {
		t.Fatalf("cap must truncate and flag the report, got truncated=%v n=%d total=%d", rep.Truncated, len(rep.Violations), rep.TotalInfractions)
	}
	if rep.Breakdown["HISS-07"] != 2 {
		t.Errorf("breakdown must match the retained violations, got %+v", rep.Breakdown)
	}

	full := scanFixture(t, root, ScanOptions{Cap: 5})
	if full.Truncated || full.TotalInfractions != 5 {
		t.Errorf("a cap equal to the violation count is not truncation, got %+v", full)
	}
}

func TestScan_SymlinksAreNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	outside := t.TempDir()
	writeFixture(t, outside, "secret.go", "package p\n\nfunc s() {\n\tpanic(\"x\")\n}\n")
	root := t.TempDir()
	writeFixture(t, root, "ok.go", "package p\n")
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(root, "evil.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, nil)
	if rep.Skips.Symlinks != 1 {
		t.Errorf("the .go symlink must be counted as skipped, got %+v", rep.Skips)
	}
}

func TestScan_OversizeFilesAreSkipped(t *testing.T) {
	root := t.TempDir()
	big := "package p\n\nfunc f() {\n\tpanic(\"x\")\n}\n" + strings.Repeat("//"+strings.Repeat("x", 62)+"\n", MaxScanFileSize/64+1)
	writeFixture(t, root, "big.go", big)
	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, nil)
	if rep.Skips.Oversize != 1 {
		t.Errorf("oversize file must be counted, got %+v", rep.Skips)
	}

	data, err := readBounded(root, "big.go")
	if !errors.Is(err, errOversize) || data != nil {
		t.Errorf("readBounded must refuse oversize content, got len=%d err=%v", len(data), err)
	}
	exact := strings.Repeat("x", MaxScanFileSize)
	writeFixture(t, root, "exact.txt", exact)
	if data, err = readBounded(root, "exact.txt"); err != nil || len(data) != MaxScanFileSize {
		t.Errorf("readBounded must accept exactly MaxScanFileSize bytes, got len=%d err=%v", len(data), err)
	}
	if _, err = readBounded(root, "../escape.go"); err == nil {
		t.Error("readBounded must refuse paths outside the root")
	}
}
