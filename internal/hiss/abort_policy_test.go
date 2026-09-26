package hiss

import (
	"strings"
	"testing"
)

// The HISS-07 abort policy (owner decision Q-014) refuses the abort forms in library code
// and allows them in tests and binary entry points. Each language is pinned in all three
// dimensions: the forms that must be reported, the places that must stay silent, and the
// edges between them.

func TestAbortPolicy_Positive_RustAbortFormsInLibraryCode(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/lib.rs", strings.Join([]string{
		"pub fn f(x: u8) -> u8 {",                  // 1
		"    if x == 0 { panic!(\"zero\"); }",      // 2
		"    if x == 1 { todo!() }",                // 3
		"    if x == 2 { unimplemented!() }",       // 4
		"    if x == 3 { unreachable!() }",         // 5
		"    if x == 4 { std::process::exit(1); }", // 6
		"    if x == 5 { process :: abort(); }",    // 7
		"    if x == 6 { core::panic ! [\"x\"]; }", // 8
		"    x", // 9
		"}",     // 10
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "src/lib.rs", 2}, {"HISS-07", "src/lib.rs", 3}, {"HISS-07", "src/lib.rs", 4},
		{"HISS-07", "src/lib.rs", 5}, {"HISS-07", "src/lib.rs", 6}, {"HISS-07", "src/lib.rs", 7},
		{"HISS-07", "src/lib.rs", 8},
	})
}

func TestAbortPolicy_Negative_RustEntryPointTestsAndLookalikes(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/main.rs", strings.Join([]string{
		"/// Aborts with panic!(\"doc\") on bad input.",
		"fn main() {",
		"    if bad() { std::process::exit(2); }",
		"    let msg = \"panic!(no)\"; // todo!()",
		"    my_panic!(\"a user macro\");",
		"    unreachable!()",
		"}",
		"",
		"#[cfg(test)]",
		"mod tests {",
		"    #[test]",
		"    #[should_panic]",
		"    fn t() { panic!(\"expected\"); }",
		"}",
		"",
	}, "\n"))
	writeFixture(t, root, "tests/integration.rs", "fn t() { todo!() }\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("entry point, tests and look-alikes must stay silent: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Boundary_RustEntryExemptsOnlyAbortsOnlyInTopLevelMain(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src/main.rs", strings.Join([]string{
		"fn main() {",                   // 1
		"    let v = opt.unwrap();",     // 2 unwrap stays refused in main
		"    panic!(\"allowed\");",      // 3
		"}",                             // 4
		"fn after() { panic!(\"no\") }", // 5 main has closed
		"impl S {",                      // 6
		"    fn main(&self) {",          // 7 a method, not the entry point
		"        todo!()",               // 8
		"    }",                         // 9
		"}",                             // 10
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "src/main.rs", 2}, {"HISS-07", "src/main.rs", 5}, {"HISS-07", "src/main.rs", 8},
	})
}

func TestAbortPolicy_Positive_GoOSExitThroughEveryImportForm(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lib/plain.go", "package lib\n\nimport \"os\"\n\nfunc F() {\n\tos.Exit(1)\n}\n")
	writeFixture(t, root, "lib/alias.go", "package lib\n\nimport o \"os\"\n\nfunc G() {\n\t(o.Exit)(2)\n}\n")
	writeFixture(t, root, "lib/dot.go", "package lib\n\nimport . \"os\"\n\nfunc H() {\n\tExit(3)\n}\n")
	writeFixture(t, root, "lib/panic.go", "package lib\n\nfunc P() {\n\tpanic(\"x\")\n}\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "lib/plain.go", 6}, {"HISS-07", "lib/alias.go", 6},
		{"HISS-07", "lib/dot.go", 6}, {"HISS-07", "lib/panic.go", 4},
	})
}

func TestAbortPolicy_Negative_GoEntryPointTestsValuesAndShadows(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "cmd/main.go", strings.Join([]string{
		"package main", "", "import \"os\"", "",
		"func main() {",
		"\tdefer func() { os.Exit(1) }()",
		"\tif len(os.Args) > 3 {",
		"\t\tpanic(\"usage\")",
		"\t}",
		"\tos.Exit(0)",
		"}", "",
	}, "\n"))
	writeFixture(t, root, "lib/lib_test.go", "package lib\n\nimport \"os\"\n\nfunc helper() {\n\tos.Exit(1)\n}\n")
	writeFixture(t, root, "lib/value.go", "package lib\n\nimport \"os\"\n\nvar exit = os.Exit\n\nfunc Install(f func(int)) {}\n\nfunc Use() {\n\tInstall(os.Exit)\n}\n")
	writeFixture(t, root, "lib/shadow.go", "package lib\n\nimport \"os\"\n\ntype q struct{}\n\nfunc (q) Exit(int) {}\n\nfunc S() {\n\tos := q{}\n\tos.Exit(1)\n}\n\nvar _ = os.Args\n")
	writeFixture(t, root, "lib/other.go", "package lib\n\nimport os \"example.com/fake/os\"\n\nfunc O() {\n\tos.Exit(1)\n}\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("entry point, tests, values, shadows and foreign packages must stay silent: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Boundary_GoMainOutsidePackageMainIsLibrary(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lib/main.go", "package lib\n\nimport \"os\"\n\nfunc main() {\n\tos.Exit(1)\n}\n")
	writeFixture(t, root, "cmd/method.go", "package main\n\nimport \"os\"\n\ntype S struct{}\n\nfunc (S) main() {\n\tos.Exit(1)\n}\n")
	writeFixture(t, root, "cmd/init.go", "package main\n\nfunc init() {\n\tpanic(\"init is not the entry point\")\n}\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "lib/main.go", 6}, {"HISS-07", "cmd/method.go", 8}, {"HISS-07", "cmd/init.go", 4},
	})
}

func TestAbortPolicy_Positive_PythonSysExitInLibraryCode(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "pkg/lib.py", strings.Join([]string{
		"import sys",          // 1
		"",                    // 2
		"def check(x):",       // 3
		"    if not x:",       // 4
		"        sys.exit(1)", // 5
		"    sys . exit (2)",  // 6
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "pkg/lib.py", 5}, {"HISS-07", "pkg/lib.py", 6}})
}

func TestAbortPolicy_Negative_PythonEntryPointsAndTests(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "tool.py", strings.Join([]string{
		"import sys",
		"",
		"def main():",
		"    if len(sys.argv) < 2:",
		"        sys.exit(2)",
		"    return 0",
		"",
		"# sys.exit(9) in a comment",
		"MSG = \"sys.exit(9) in a string\"",
		"",
		"if __name__ == \"__main__\":",
		"    sys.exit(main())",
		"",
	}, "\n"))
	writeFixture(t, root, "rev.py", "import sys\nif '__main__' == __name__:\n    sys.exit(0)\n")
	writeFixture(t, root, "test_tool.py", "import sys\ndef helper():\n    sys.exit(1)\n")
	writeFixture(t, root, "conftest.py", "import sys\ndef helper():\n    sys.exit(1)\n")
	writeFixture(t, root, "tests/helpers.py", "import sys\ndef helper():\n    sys.exit(1)\n")
	writeFixture(t, root, "pkg/__main__.py", "import sys\ndef run():\n    sys.exit(1)\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("entry points and tests must stay silent: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Boundary_PythonEntryEndsAtTheNextTopLevelLine(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "tool.py", strings.Join([]string{
		"import sys",          // 1
		"def main():",         // 2
		"    sys.exit(0)",     // 3 entry point
		"def after():",        // 4
		"    sys.exit(1)",     // 5 main has ended
		"class C:",            // 6
		"    def main(self):", // 7 a method, not the entry point
		"        sys.exit(1)", // 8
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "tool.py", 5}, {"HISS-07", "tool.py", 8}})
}

func TestAbortPolicy_Boundary_PythonWrappedSignatureKeepsTheEntryOpen(t *testing.T) {
	root := t.TempDir()
	// black puts the closing `) -> int:` of a wrapped signature at column zero. Python
	// ignores indentation inside brackets and after a backslash, so such a line continues the
	// def header or the statement above it and neither ends nor opens the entry point.
	writeFixture(t, root, "tool.py", strings.Join([]string{
		"import sys",             // 1
		"",                       // 2
		"def main(",              // 3
		"    argv=None,",         // 4
		") -> int:",              // 5 continues the header
		"    run(",               // 6
		"        argv,",          // 7
		")",                      // 8 continues the call
		"    if not argv:",       // 9
		"        sys.exit(2)",    // 10 still the entry point
		"    return 0",           // 11
		"",                       // 12
		"def helper(",            // 13
		"    x,",                 // 14
		"):",                     // 15
		"    sys.exit(1)",        // 16 library code
		"",                       // 17
		"def main(argv=None) \\", // 18
		"-> int:",                // 19 continues after a backslash
		"    sys.exit(0)",        // 20 the entry point again
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "tool.py", 16}})
}

func TestAbortPolicy_Negative_PythonStrayCloserDoesNotHideAWrappedEntry(t *testing.T) {
	root := t.TempDir()
	// An unbalanced closer must not leave the bracket count below zero, or the next wrapped
	// header would balance to zero on its opening line and its column-zero closer would end
	// the entry point again.
	writeFixture(t, root, "tool.py", strings.Join([]string{
		"import sys",      // 1
		")",               // 2 unbalanced
		"def main(",       // 3
		"    argv=None,",  // 4
		") -> int:",       // 5
		"    sys.exit(2)", // 6 the entry point
		"",
	}, "\n"))

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("a wrapped def main after a stray closer is still the entry point: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Negative_RustBangEqualsIsNotAMacroCall(t *testing.T) {
	root := t.TempDir()
	// A macro call needs its delimiter after the bang. An identifier spelled like an abort
	// macro and compared with != is an ordinary expression.
	writeFixture(t, root, "src/lib.rs", strings.Join([]string{
		"pub fn f(todo: u8, panic: u8) -> bool {",
		"    if todo != 0 { return true; }",
		"    let unreachable = panic!=1;",
		"    unreachable",
		"}",
		"",
	}, "\n"))

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("a != comparison is not an abort macro: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Boundary_RustOneLineAndAttributedMain(t *testing.T) {
	root := t.TempDir()
	// fn main is the entry point even when its body closes on the header line, and when an
	// attribute such as #[tokio::main] shares the header line. The function after each main
	// is library code again.
	writeFixture(t, root, "src/main.rs", strings.Join([]string{
		"fn main() { std::process::exit(run()) }", // 1 entry point
		"fn helper() { std::process::exit(1) }",   // 2 library code
		"",
	}, "\n"))
	writeFixture(t, root, "src/bin/tool.rs", strings.Join([]string{
		"#[tokio::main] async fn main() {", // 1
		"    panic!(\"entry point\");",     // 2 entry point
		"}",                                // 3
		"#[inline] fn after() {",           // 4
		"    todo!()",                      // 5 library code
		"}",                                // 6
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "src/main.rs", 2}, {"HISS-07", "src/bin/tool.rs", 5}})
}

func TestAbortPolicy_Negative_GoSignatureBindingsShadowOS(t *testing.T) {
	root := t.TempDir()
	// A receiver, parameter, named result, closure parameter or range variable named os hides
	// the package for the whole body, so os.Exit there calls the local value's method.
	writeFixture(t, root, "lib/sig.go", strings.Join([]string{
		"package lib", "", "import \"os\"", "",
		"var _ = os.Args", "",
		"type q struct{}", "",
		"func (q) Exit(int) {}", "",
		"func Param(os q) { os.Exit(1) }",
		"func Result() (os q) { os.Exit(1); return }",
		"func (os q) Recv() { os.Exit(1) }",
		"func Closure() { _ = func(os q) { os.Exit(1) } }",
		"func Range(qs []q) {",
		"\tfor _, os := range qs {",
		"\t\tos.Exit(1)",
		"\t}",
		"}", "",
	}, "\n"))

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("a binding named os in the signature, a closure or a range clause shadows the package: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Boundary_GoShadowIsScopedToItsFunction(t *testing.T) {
	root := t.TempDir()
	// A parameter named os shadows the package only inside its own function; the next
	// function reaches package os again.
	writeFixture(t, root, "lib/scope.go", strings.Join([]string{
		"package lib",                        // 1
		"",                                   // 2
		"import \"os\"",                      // 3
		"",                                   // 4
		"type q struct{}",                    // 5
		"",                                   // 6
		"func (q) Exit(int) {}",              // 7
		"",                                   // 8
		"func Shadowed(os q) { os.Exit(1) }", // 9
		"func Unshadowed(o q) { os.Exit(o.n()) }", // 10
		"func (q) n() int { return 1 }",           // 11
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "lib/scope.go", 10}})
}
