package hiss

import (
	"strings"
	"testing"
)

// rustSafetyCase is one Rust source and the lines HISS-09 must report in it.
type rustSafetyCase struct {
	name string
	src  []string
	want []int
}

func runRustSafetyCases(t *testing.T, cases []rustSafetyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := scanFixtureFile(t, "lib.rs", strings.Join(tc.src, "\n")+"\n")
			assertUnsafeLines(t, rep, tc.want...)
		})
	}
}

// TestRustSafety_Negative_ClippyAcceptedShapes holds the shapes clippy's
// undocumented_unsafe_blocks and missing_safety_doc accept, which HISS-09 reported before:
// a documented unsafe fn of any visibility, a trait-impl method, and a proof above the
// statement rustfmt wrapped around a block.
func TestRustSafety_Negative_ClippyAcceptedShapes(t *testing.T) {
	runRustSafetyCases(t, []rustSafetyCase{
		{name: "documented unsafe fn of every visibility", src: []string{
			"/// Reads one byte.", "///", "/// # Safety", "///", "/// `p` must be valid for reads.",
			"pub unsafe fn read_pub(p: *const u8) -> u8 {",
			"    // SAFETY: the caller upholds this function's contract.", "    unsafe { *p }", "}",
			"/// # Safety", "/// `p` must be valid for reads.",
			"unsafe fn read_private(p: *const u8) -> u8 {",
			"    // SAFETY: the caller upholds this function's contract.", "    unsafe { *p }", "}",
		}},
		{name: "proof above a let rustfmt wrapped", src: []string{
			"pub fn wrapped(p: &u8) -> u8 {",
			"    // SAFETY: `p` is a live reference, so it is valid for reads.",
			"    let value =", "        unsafe { read(p) };", "    value", "}",
		}},
		{name: "proof above an assignment, a call and a method chain", src: []string{
			"fn f(p: &u8) {",
			"    // SAFETY: p is live.", "    x =", "        unsafe { read(p) };",
			"    // SAFETY: p is live.", "    let v = outer(", "        a,", "        inner(",
			"            unsafe { read(p) },", "        ),", "    );",
			"    // SAFETY: p is live.", "    let w = source", "        .map(|b| unsafe { read(b) })", "        .sum();",
			"}",
		}},
		{name: "trait-impl methods take the trait's contract", src: []string{
			"struct A;",
			"// SAFETY: forwards every call to the system allocator.",
			"unsafe impl GlobalAlloc for A {",
			"    unsafe fn alloc(&self, l: Layout) -> *mut u8 {",
			"        // SAFETY: the caller's layout contract is the system allocator's.",
			"        unsafe { System.alloc(l) }", "    }",
			"}",
			"impl Reader for A {", "    unsafe fn read(&self) {}", "}",
		}},
		{name: "rustdoc spellings of the safety section", src: []string{
			"/// ## Safety", "pub(crate) const unsafe fn a() {}",
			"/// # SAFETY", "#[inline]", `pub unsafe extern "C" fn b() {}`,
			"/// # Implementation safety", "async unsafe fn c() {}",
			"/**", " * # Safety", " */", "unsafe fn d() {}",
			"/** # Safety */", "unsafe fn e() {}",
			`#[doc = "# Safety"]`, "unsafe fn f() {}",
		}},
		{name: "SAFETY comment above an unsafe fn", src: []string{
			"// SAFETY: callers pass a valid pointer.", "unsafe fn g(p: *const u8) {}",
		}},
		{name: "fn pointer type is no header", src: []string{
			"struct Hooks(", "    unsafe fn(u8),", ");",
		}},
	})
}

// TestRustSafety_Positive_UndocumentedUnsafeFn holds an unsafe fn without its contract to
// one rule whatever its visibility or qualifiers; before, only a bare `unsafe fn` was reported.
func TestRustSafety_Positive_UndocumentedUnsafeFn(t *testing.T) {
	runRustSafetyCases(t, []rustSafetyCase{
		{name: "every visibility and qualifier", want: []int{1, 2, 3, 4, 5}, src: []string{
			"pub unsafe fn a() {}", "pub(crate) unsafe fn b() {}", "const unsafe fn c() {}",
			`unsafe extern "C" fn d() {}`, "#[inline] pub unsafe fn e() {}",
		}},
		{name: "a comment that is not rustdoc", want: []int{2, 4, 6, 8}, src: []string{
			"// # Safety", "unsafe fn a() {}",
			"//! # Safety", "unsafe fn b() {}",
			"/// Safety", "unsafe fn c() {}",
			"/// # Safety:", "unsafe fn d() {}",
		}},
		{name: "inherent impl and trait declaration", want: []int{3, 6}, src: []string{
			"struct A;", "impl A {", "    unsafe fn read(&self) {}", "}",
			"trait Reader {", "    unsafe fn read(&self);", "}",
		}},
		{name: "local fn inside a trait-impl method", want: []int{3}, src: []string{
			"impl Reader for A {", "    fn read(&self) {",
			"        unsafe fn local() {}", "    }", "}",
		}},
		{name: "doc of the item above does not carry", want: []int{3}, src: []string{
			"/// # Safety", "fn a() {}", "unsafe fn b() {}",
		}},
	})
}

// TestRustSafety_Boundary_StatementEdges pins where a statement-level proof stops: at a
// match arm, a struct field, a condition and the statement before.
func TestRustSafety_Boundary_StatementEdges(t *testing.T) {
	runRustSafetyCases(t, []rustSafetyCase{
		{name: "next match arm", want: []int{5, 6, 7}, src: []string{
			"fn f(n: i32) {", "    match n {",
			"        // SAFETY: proves the first arm only.", "        0 => unsafe { a() },",
			"        1 => unsafe { b() },", "        -1 => unsafe { c() },", "        ..=5 => unsafe { d() },",
			"        _ => {}", "    }", "}",
		}},
		{name: "arm body on its own line", want: []int{5}, src: []string{
			"fn f(n: i32) {", "    // SAFETY: proves nothing inside the match.", "    match n {",
			"        0 =>", "            unsafe { a() },", "        _ => {}", "    }", "}",
		}},
		{name: "struct literal field", want: []int{4}, src: []string{
			"fn f() {", "    // SAFETY: clippy does not carry this into a field.",
			"    let v = Foo {", "        f: unsafe { a() },", "    };", "}",
		}},
		{name: "struct literal inside a wrapped call", want: []int{4, 8}, src: []string{
			"fn f() {", "    // SAFETY: clippy does not carry this into a field.",
			"    let v = make(", "        Foo { f: unsafe { a() } },", "    );",
			"    // SAFETY: nor into a field whose literal opened a line above.",
			"    let w = make(Foo { g: 1,", "        f: unsafe { a() } });", "}",
		}},
		{name: "condition of an if", want: []int{4}, src: []string{
			"fn f() {", "    // SAFETY: clippy does not accept this for the condition.",
			"    if check(", "        unsafe { a() },", "    ) {", "    }", "}",
		}},
		{name: "previous statement", want: []int{5}, src: []string{
			"fn f() {", "    // SAFETY: proves the first statement only.",
			"    let a = unsafe { x() };", "    let b =", "        unsafe { y() };", "}",
		}},
		{name: "blank line above the statement", want: []int{5}, src: []string{
			"fn f() {", "    // SAFETY: detached by the blank line.", "",
			"    let a =", "        unsafe { x() };", "}",
		}},
	})
}

func TestRustUnsafeFnHeader(t *testing.T) {
	for line, want := range map[string]bool{
		"unsafe fn a() {":                  true,
		"pub(in crate::x) unsafe fn b() {": true,
		"#[unsafe(no_mangle)] fn c() {":    false,
		"safe fn d();":                     false,
		"unsafe fn(u8),":                   false,
		"let f: unsafe fn() = g;":          false,
		"fn unsafe_read() {":               false,
	} {
		code := strings.TrimSpace((&literalStripper{syn: cLikeSyntax}).strip(line))
		if got := rustUnsafeFnHeader(code); got != want {
			t.Errorf("rustUnsafeFnHeader(%q) = %v, want %v", line, got, want)
		}
	}
}
