package hiss

import (
	"regexp"
	"strings"
)

// Where a Rust SAFETY proof attaches (HISS-09).
//
// The rule follows clippy's undocumented_unsafe_blocks and missing_safety_doc lints, the two a
// Rust workspace already runs, so code both lints accept is not reported here:
//
//   - An unsafe block carries its proof on its own line or in the comment block directly above
//     it, or directly above the first line of the statement holding it. rustfmt moves a long
//     `let v = unsafe { .. };` onto two lines and leaves the comment above the `let`, which
//     clippy accepts by default (accept-comment-above-statement). The statement proof does not
//     reach a block in the head of an if, match, loop, while or for statement, nor one inside a
//     brace (a struct literal field, a closure body, a match arm), as clippy's does not.
//   - An unsafe fn declaration is not an unsafe block. It documents the contract its callers
//     must uphold in a rustdoc `# Safety` section; a SAFETY: comment above it is accepted as
//     well. Every visibility and qualifier is held to that alike. A method implementing a trait
//     is exempt, because the trait declaration documents the contract, as clippy holds.
//
// Two stricter spellings are deliberate and pinned by positive fixtures: the marker must be the
// upper-case SAFETY:, and a blank line detaches a comment from the block below it.

var (
	// rustSafetyHeading is a rustdoc heading clippy reads as the safety section of an unsafe
	// fn's docs, after the comment marker is removed.
	rustSafetyHeading = regexp.MustCompile(`^#{1,6}[ \t]+(?:Safety|SAFETY|Implementation [Ss]afety)(?:[ \t]+#*)?$`)
	// rustDocAttr is a single-line `#[doc = "..."]` attribute, the desugared form of `///`.
	rustDocAttr = regexp.MustCompile(`^#\s*\[\s*doc\s*=\s*"(.*)"\s*\]$`)
)

// rustProofSite is what a proof may attach to for one line: the first line of its statement,
// and whether the rustdoc comments above the line carry a `# Safety` section.
type rustProofSite struct {
	stmt int
	// branchy reports a statement led by a block-like expression, whose proof is not accepted
	// for a block inside it.
	branchy   bool
	safetyDoc bool
}

// rustProofScope follows, across lines, the statement the current line belongs to and the
// rustdoc comments and attributes above the next item. Each line must be passed exactly once
// and in order.
type rustProofScope struct {
	stmt    int
	branchy bool
	// depth counts the parentheses and brackets opened since the statement started, never
	// below zero, so a line inside a call's arguments continues the call's statement.
	depth int
	// prev is the last code line, trimmed and stripped; empty before the first.
	prev      string
	safetyDoc bool
	// docBlock reports an open `/**` doc comment.
	docBlock bool
}

// observe feeds lines[idx] as raw text and as code, the same line trimmed and stripped of
// literals and comments, and returns what a proof may attach to on that line.
func (p *rustProofScope) observe(raw, code string, idx int) rustProofSite {
	if code == "" || isRustAttribute(code) {
		p.noteDoc(strings.TrimSpace(raw))
		return rustProofSite{stmt: idx, safetyDoc: p.safetyDoc}
	}
	if !p.continues(code) {
		p.stmt, p.depth, p.branchy = idx, 0, rustBlockLed(code) || strings.HasPrefix(code, "}")
	}
	site := rustProofSite{stmt: p.stmt, branchy: p.branchy, safetyDoc: p.safetyDoc}
	p.depth = max(p.depth+rustNesting(code), 0)
	p.prev, p.safetyDoc, p.docBlock = code, false, false
	return site
}

// continues reports whether code continues the statement the previous code line is part of:
// that line did not end the statement, close a block or leave a brace open, and either a
// parenthesis or bracket is still open, the line ended with an operator (`let v =`), or code
// carries a method chain or binary operator on. A line inside an open brace belongs to a
// block, a struct literal or a match, whose statement-level proof clippy does not carry in.
func (p *rustProofScope) continues(code string) bool {
	if p.prev == "" {
		return false
	}
	last := p.prev[len(p.prev)-1]
	if strings.IndexByte(";{}", last) >= 0 || rustBraceNesting(p.prev) > 0 {
		return false
	}
	return p.depth > 0 || strings.IndexByte("=+-*/%&|^<", last) >= 0 || rustCarriesOn(code)
}

// rustCarriesOn reports whether code starts with a token that only continues an expression
// begun above: a method call or a binary operator that cannot also start a match arm's
// pattern. A range (`..`), a negative literal, a reference and a leading vert can, so they
// do not count.
func rustCarriesOn(code string) bool {
	if strings.HasPrefix(code, ".") {
		return !strings.HasPrefix(code, "..")
	}
	for _, op := range []string{"?", "+", "/", "%", "^", "&&", "||", "==", "!="} {
		if strings.HasPrefix(code, op) {
			return true
		}
	}
	return false
}

// isRustAttribute reports whether code is one whole outer or inner attribute.
func isRustAttribute(code string) bool {
	return (strings.HasPrefix(code, "#[") || strings.HasPrefix(code, "#![")) && strings.HasSuffix(code, "]")
}

// noteDoc records a `# Safety` heading on a line holding no code but rustdoc: a `///` comment,
// a line of a `/** */` comment or a `#[doc = "..."]` attribute. An inner `//!` doc documents
// the enclosing module, not the next item, and a plain comment is not documentation at all.
func (p *rustProofScope) noteDoc(trimmed string) {
	text, ok := p.docText(trimmed)
	if ok && rustSafetyHeading.MatchString(strings.TrimSpace(text)) {
		p.safetyDoc = true
	}
}

// docText returns the rustdoc text of trimmed and whether the line is outer documentation.
func (p *rustProofScope) docText(trimmed string) (string, bool) {
	switch {
	case p.docBlock:
	case strings.HasPrefix(trimmed, "///"):
		return trimmed[len("///"):], !strings.HasPrefix(trimmed, "////")
	case strings.HasPrefix(trimmed, "/**") && !strings.HasPrefix(trimmed, "/***") && !strings.HasPrefix(trimmed, "/**/"):
		p.docBlock = true
		trimmed = trimmed[len("/**"):]
	default:
		m := rustDocAttr.FindStringSubmatch(trimmed)
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	text, _, closed := strings.Cut(trimmed, blockCommentClose)
	if closed {
		p.docBlock = false
	}
	return strings.TrimPrefix(strings.TrimSpace(text), "*"), true
}

// rustUnsafeFnHeader reports whether code, a trimmed line with literals stripped, opens the
// header of an unsafe fn, whatever its attributes, visibility and other qualifiers. A pointer
// type such as `unsafe fn(u8)` names no function and is not a header.
func rustUnsafeFnHeader(code string) bool {
	m := rustFnHeader.FindStringSubmatch(code)
	return m != nil && containsIdent(m[1], "unsafe")
}

// checkRustUnsafe enforces HISS-09 on lines[idx], whose stripped code is code. member is the
// kind of impl or trait body the line stands directly in. A line reports at most one finding,
// because a baseline fingerprints a finding by rule, path and line.
func checkRustUnsafe(lines []string, idx int, code string, site rustProofSite, member rustBodyKind, rel string, rep *ScanReport) {
	lineNum := idx + 1
	if rustUnsafeFnHeader(strings.TrimSpace(code)) && member != rustTraitImplBody &&
		!site.safetyDoc && !hasSafetyComment(lines, idx) {
		recordViolation(rep, "HISS-09", rel, lineNum, "",
			"unsafe fn without a rustdoc # Safety section or a preceding // SAFETY: proof comment")
		return
	}
	loc := rustUnsafeBlock.FindStringIndex(code)
	if loc != nil && !rustBlockProven(lines, idx, code[:loc[0]], site) {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "unsafe block without a preceding // SAFETY: proof comment")
	}
}

// rustBlockProven reports whether the unsafe block on lines[idx], preceded on its line by
// before, carries a SAFETY proof on its own line, directly above it, or directly above the
// first line of its statement. A block inside a brace opened earlier on its line (a struct
// literal field, a closure body, a match arm) is not reached by the statement's proof.
func rustBlockProven(lines []string, idx int, before string, site rustProofSite) bool {
	if hasSafetyComment(lines, idx) {
		return true
	}
	return site.stmt < idx && !site.branchy && rustBraceNesting(before) <= 0 && hasSafetyComment(lines, site.stmt)
}
