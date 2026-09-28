// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"regexp"
	"slices"
	"strings"
)

// CleanupGoto is the C/C++ cleanup-goto exception to the zero-goto clause, declared as
// hiss.exceptions.c_goto_cleanup in .standards.yaml (#68). The zero value declares nothing,
// and the native scan reports every `goto`, as it always has.
//
// With Enabled set, the native scan reports every `goto` except one that satisfies all of
// these, the rule CleanupGotoRule states (cleanupGotoAllowed):
//   - forward: the `goto` line precedes its label line;
//   - same function: the label is defined in the function body enclosing the `goto`;
//   - single level: that function defines exactly one label, so it has one cleanup exit;
//   - body level: the label sits directly in the function body, outside every nested block,
//     so the jump only leaves blocks and never enters one;
//   - name: the label is one of cleanupGotoLabels or one of Labels.
type CleanupGoto struct {
	// Enabled is set when the repository declares the exception and the document it names
	// exists (config.Manifest.CleanupGotoException).
	Enabled bool
	// Labels are further label names the repository declares
	// (hiss.exceptions.c_goto_cleanup_labels), at most MaxCleanupGotoLabels.
	Labels []string
}

// MaxCleanupGotoLabels bounds the label names a repository may declare (HISS-02).
const MaxCleanupGotoLabels = 8

// maxCleanupGotoLabelLen bounds one declared label name.
const maxCleanupGotoLabelLen = 64

// maxPendingGotos bounds the `goto` statements one function holds until it closes (HISS-02).
// A further `goto` is reported at once, so the bound fails closed.
const maxPendingGotos = 1024

// cleanupGotoLabels are the label names the exception accepts without a declaration.
var cleanupGotoLabels = [...]string{"cleanup", "out", "err", "fail"}

// cleanupGotoLabel is the shape of a declarable label name: a C identifier.
var cleanupGotoLabel = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidCleanupGotoLabel reports whether name can be declared as a cleanup label: a C
// identifier of at most 64 bytes.
func ValidCleanupGotoLabel(name string) bool {
	return len(name) <= maxCleanupGotoLabelLen && cleanupGotoLabel.MatchString(name)
}

// CleanupGotoRule states, in the internal register of the adopted harness, exactly the rule the
// native scan applies under the exception (CleanupGoto, cleanupGotoAllowed). The adopted HISS-01
// clause for a repository declaring it renders this text (internal/hisscatalog), so the harness
// and the scan share one source for the rule and its wording.
func CleanupGotoRule() string {
	names := make([]string, 0, len(cleanupGotoLabels))
	for _, name := range cleanupGotoLabels {
		names = append(names, "`"+name+"`")
	}
	return "`goto` only forward jump to sole label of same function; label directly in function body, " +
		"outside nested blocks; label named " + strings.Join(names, " / ") +
		" or listed in `hiss.exceptions.c_goto_cleanup_labels`"
}

// allowsName reports whether label is a cleanup label name under c.
func (c CleanupGoto) allowsName(label string) bool {
	return slices.Contains(cleanupGotoLabels[:], label) || slices.Contains(c.Labels, label)
}

// nativeLabel matches a statement label opening a line of stripped native code: an identifier
// and one colon, never the `::` of a qualified name. `case` never matches, because a space
// separates it from its value; nativeNonLabel drops the keywords that do.
var nativeLabel = regexp.MustCompile(`^([A-Za-z_]\w*)\s*:(?:[^:]|$)`)

// nativeNonLabel reports whether name, matched by nativeLabel, is a keyword rather than a label.
func nativeNonLabel(name string) bool {
	switch name {
	case "default", "public", "private", "protected":
		return true
	default:
		return false
	}
}

// gotoSite is one `goto` held until its function closes.
type gotoSite struct {
	line   int
	target string
}

// nativeGotos decides HISS-01 for the `goto` statements of one native file. Without the
// exception every `goto` is reported on its own line. With it, a `goto` inside a function body
// is held until that function closes, since its label may follow it, and is then reported
// unless cleanupGotoAllowed accepts it.
type nativeGotos struct {
	rel     string
	rep     *ScanReport
	allow   CleanupGoto
	pending []gotoSite
	// labels counts the labels the open function defines; label, labelLine and labelDepth
	// describe the first, the only one cleanupGotoAllowed can accept.
	labels     int
	label      string
	labelLine  int
	labelDepth int
}

// observe feeds one line of stripped code. inBody reports whether the line starts inside a
// function body, and depth is the brace depth there, 1 directly in the body.
func (g *nativeGotos) observe(code string, lineNum int, inBody bool, depth int) {
	trimmed := strings.TrimSpace(code)
	if rest, ok := strings.CutPrefix(trimmed, "goto "); ok {
		g.jump(gotoTarget(rest), lineNum, inBody)
		return
	}
	if inBody {
		g.mark(trimmed, lineNum, depth)
	}
}

// gotoTarget is the operand of a `goto`: the label, or for a computed goto the expression.
func gotoTarget(rest string) string {
	target, _, _ := strings.Cut(strings.TrimSpace(rest), ";")
	return strings.TrimSpace(target)
}

// jump reports the `goto` at lineNum at once, or holds it for its function's close when the
// exception may accept it.
func (g *nativeGotos) jump(target string, lineNum int, inBody bool) {
	if !g.allow.Enabled || !inBody || len(g.pending) >= maxPendingGotos {
		g.report(lineNum)
		return
	}
	g.pending = append(g.pending, gotoSite{line: lineNum, target: target})
}

// mark records a label opening the line. Closing braces before it leave blocks first, so the
// label's depth is the line's depth less those braces.
func (g *nativeGotos) mark(trimmed string, lineNum, depth int) {
	rest := strings.TrimLeft(trimmed, "} \t")
	depth -= strings.Count(trimmed[:len(trimmed)-len(rest)], "}")
	match := nativeLabel.FindStringSubmatch(rest)
	if match == nil || nativeNonLabel(match[1]) {
		return
	}
	g.labels++
	if g.labels == 1 {
		g.label, g.labelLine, g.labelDepth = match[1], lineNum, depth
	}
}

// close ends the open function: every held `goto` the exception does not accept is reported.
func (g *nativeGotos) close() {
	for _, site := range g.pending {
		if !g.cleanupGotoAllowed(site) {
			g.report(site.line)
		}
	}
	g.pending = g.pending[:0]
	g.labels, g.label, g.labelLine, g.labelDepth = 0, "", 0, 0
}

// cleanupGotoAllowed is the exception's rule (CleanupGoto): a forward jump to the only label of
// the same function, directly in its body, under a cleanup label name.
func (g *nativeGotos) cleanupGotoAllowed(site gotoSite) bool {
	return g.labels == 1 && site.target == g.label && site.line < g.labelLine &&
		g.labelDepth == 1 && g.allow.allowsName(site.target)
}

func (g *nativeGotos) report(lineNum int) {
	recordViolation(g.rep, "HISS-01", g.rel, lineNum, "", "Legacy non-DAG control flow jump (goto)")
}
