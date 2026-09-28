package hiss

import "testing"

// Positive (#570): .hh is a C++ header exactly like .hpp, so the native scanner reads it and
// reports the same HISS-02 shape it reports in a .hpp file. Before, a .hh file was counted as
// unscanned and its debt never reached V_total.
func TestScan_CxxHhHeaderIsScannedAsNative(t *testing.T) {
	for _, name := range []string{"loop.hh", "loop.hpp"} {
		rep := scanFixtureFile(t, name, "inline void f(void) {\n\twhile (1) { }\n}\n")
		if rep.Coverage.FilesRead != 1 || rep.Breakdown["HISS-02"] != 1 {
			t.Errorf("%s: want one file read and one HISS-02 finding, got read=%d violations=%+v",
				name, rep.Coverage.FilesRead, rep.Violations)
		}
	}
}

// Negative: a kind with no language dispatch stays unscanned; adding .hh admitted no
// neighbouring suffix and no non-C language.
func TestSupportsExtension_RejectsUndispatchedKinds(t *testing.T) {
	for _, ext := range []string{".hhh", ".h.in", ".zig", ".glsl", ".comp", ".ts", ""} {
		if SupportsExtension(ext) {
			t.Errorf("%q has no HISS dispatch but SupportsExtension accepted it", ext)
		}
	}
	rep := scanFixtureFile(t, "loop.zig", "fn f() void {\n\twhile (true) {}\n}\n")
	if rep.Coverage.FilesRead != 0 || rep.Coverage.UnscannedByExtension[".zig"] != 1 {
		t.Errorf("a .zig file must stay unscanned, got %+v", rep.Coverage)
	}
}

// Boundary: every C-family extension is accepted, in either case, because Scan lowercases
// the extension before dispatch and SupportsExtension answers from the same table.
func TestSupportsExtension_CFamilyIsCaseInsensitive(t *testing.T) {
	for _, ext := range []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".cu", ".hip", ".HH", ".Cpp"} {
		if !SupportsExtension(ext) {
			t.Errorf("%q is a native C-family extension but SupportsExtension rejected it", ext)
		}
	}
}
