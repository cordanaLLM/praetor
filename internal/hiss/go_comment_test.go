package hiss

import "testing"

func TestScanGoCommentsPositiveValidSafetyProof(t *testing.T) {
	rep := scanFixtureFile(t, "input.go", `package p

import "unsafe"

func f() {
	/* SAFETY: nil is never dereferenced. */
	p := unsafe.Pointer(nil)
	_ = p
}
`)
	if got := rep.Breakdown["HISS-09"]; got != 0 {
		t.Fatalf("valid SAFETY proof reported %d HISS-09 violations: %+v", got, rep.Violations)
	}
	if rep.Incomplete() {
		t.Fatalf("valid source reported incomplete: %+v", rep.Skips)
	}
}

func TestScanGoCommentsNegativeOrdinaryCommentDoesNotExemptUnsafe(t *testing.T) {
	rep := scanFixtureFile(t, "input.go", `package p

import "unsafe"

func f() {
	/* This comment is not a safety proof. */
	p := unsafe.Pointer(nil)
	_ = p
}
`)
	if got := rep.Breakdown["HISS-09"]; got != 1 {
		t.Fatalf("ordinary comment reported %d HISS-09 violations, want 1: %+v", got, rep.Violations)
	}
}

func TestScanGoCommentsBoundaryUnterminatedBlockAtEOF(t *testing.T) {
	// ParseFile returns a partial AST whose final comment has Text "/*" and a valid
	// End position at EOF. Scan must treat the malformed body as unparsed, not ask
	// CommentGroup.Text to strip a closing delimiter that is not present.
	rep := scanFixtureFile(t, "input.go", "package A)0/*")
	if rep.Skips.Unparsed != 1 {
		t.Fatalf("unterminated block comment unparsed count = %d, want 1", rep.Skips.Unparsed)
	}
	if !rep.Incomplete() {
		t.Fatal("unterminated block comment must make the scan incomplete")
	}
}
