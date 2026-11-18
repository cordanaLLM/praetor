// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"strings"
	"testing"
)

const pinTestSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"

// Positive: a step's uses: value is read with its comment, and the SHA-pinned form splits
// into action, SHA and release, a sub-path action included.
func TestParsePinnedActionPositive(t *testing.T) {
	ref, uses := ActionUsesValue("      - uses: actions/checkout@" + pinTestSHA + "  # v7.0.1  ")
	if !uses || ref != "actions/checkout@"+pinTestSHA+"  # v7.0.1" {
		t.Fatalf("uses value = %q, %v", ref, uses)
	}
	pin, ok := ParsePinnedAction(ref)
	if !ok || pin != (PinnedAction{Action: "actions/checkout", SHA: pinTestSHA, Release: "v7.0.1"}) {
		t.Fatalf("pin = %+v, %v", pin, ok)
	}
	pin, ok = ParsePinnedAction("github/codeql-action/init@" + pinTestSHA + "  # v4")
	if !ok || pin.Action != "github/codeql-action/init" || pin.Release != "v4" {
		t.Fatalf("sub-path pin = %+v, %v", pin, ok)
	}
	// #610: the one-space form Renovate and pinact write reads as the two-space form does.
	pin, ok = ParsePinnedAction("actions/checkout@" + pinTestSHA + " # v7.0.1")
	if !ok || pin.Release != "v7.0.1" {
		t.Fatalf("one-space pin = %+v, %v", pin, ok)
	}
}

// Positive: ParseSHAPin reads every SHA pin, a bare SHA and one whose comment names no
// release included, and keeps the release only where the comment names one.
func TestParseSHAPin(t *testing.T) {
	for ref, release := range map[string]string{
		"actions/checkout@" + pinTestSHA:                 "",
		"actions/checkout@" + pinTestSHA + "  # pinned":  "",
		"actions/checkout@" + pinTestSHA + " # v7.0.1":   "v7.0.1",
		"actions/checkout@" + pinTestSHA + "\t# 7.0.1":   "7.0.1",
		"actions/checkout@" + pinTestSHA + " # v7.0.1 x": "",
		"actions/checkout@" + pinTestSHA + " #v7.0.1":    "",
		"actions/checkout@" + pinTestSHA + "  #  \tv7.1": "v7.1",
	} {
		pin, ok := ParseSHAPin(ref)
		if !ok || pin.Action != "actions/checkout" || pin.SHA != pinTestSHA || pin.Release != release {
			t.Errorf("%q: pin = %+v, %v; want release %q", ref, pin, ok, release)
		}
	}
	for _, ref := range []string{
		"actions/checkout@v7",
		"actions/checkout@" + pinTestSHA[:39] + " # v7.0.1",
		"actions/checkout@" + pinTestSHA + "0",
		"actions/checkout@" + pinTestSHA + "# v7.0.1",
		"./.github/actions/local",
	} {
		if pin, ok := ParseSHAPin(ref); ok {
			t.Errorf("%q: parsed %+v", ref, pin)
		}
	}
}

// Negative: a comment line declares no uses: key, and a tag, a branch, a short or
// uppercase SHA, a SHA without its release comment, a non-release comment, a comment
// glued to the SHA, a local action and a Docker reference are not the pinned form.
func TestParsePinnedActionNegative(t *testing.T) {
	if ref, uses := ActionUsesValue("      # - uses: actions/checkout@v7"); uses {
		t.Fatalf("a comment line declared %q", ref)
	}
	if ref, uses := ActionUsesValue("      run: echo uses: x"); uses {
		t.Fatalf("a run line declared %q", ref)
	}
	for name, ref := range map[string]string{
		"tag":             "actions/checkout@v7",
		"tag and comment": "actions/checkout@v7.0.1  # v7.0.1",
		"branch":          "owner/action/path@main",
		"short SHA":       "actions/checkout@" + pinTestSHA[:39] + "  # v7.0.1",
		"uppercase SHA":   "actions/checkout@" + strings.ToUpper(pinTestSHA) + "  # v7.0.1",
		"no comment":      "actions/checkout@" + pinTestSHA,
		"glued comment":   "actions/checkout@" + pinTestSHA + "# v7.0.1",
		"39-hex, 1 space": "actions/checkout@" + pinTestSHA[:39] + " # v7.0.1",
		"not a release":   "actions/checkout@" + pinTestSHA + "  # pinned",
		"local":           "./.github/actions/local",
		"docker":          "docker://alpine:3",
	} {
		if pin, ok := ParsePinnedAction(ref); ok {
			t.Fatalf("%s: parsed %+v", name, pin)
		}
	}
}

// Boundary: a release without its "v", a single-number release, a wider comment gap, a tab
// before the "#" and a bare "uses:" with no value hold at the edges of the form.
func TestParsePinnedActionBoundary(t *testing.T) {
	for ref, release := range map[string]string{
		"actions/setup-node@" + pinTestSHA + "  # 7.0.0":   "7.0.0",
		"actions/setup-node@" + pinTestSHA + "  # v7":      "v7",
		"actions/setup-node@" + pinTestSHA + "     # v7.1": "v7.1",
		"actions/setup-node@" + pinTestSHA + "\t# v7.1":    "v7.1",
	} {
		if pin, ok := ParsePinnedAction(ref); !ok || pin.Release != release {
			t.Fatalf("%q: pin = %+v, %v", ref, pin, ok)
		}
	}
	if pin, ok := ParsePinnedAction("actions/setup-node@" + pinTestSHA + "  # v7.0.1-rc"); ok {
		t.Fatalf("a prerelease comment parsed as %+v", pin)
	}
	if ref, uses := ActionUsesValue("uses:"); !uses || ref != "" {
		t.Fatalf("bare uses: = %q, %v", ref, uses)
	}
}

// Positive, negative and boundary: ScanActionUses returns the lines and each uses: line in
// order, pinned or not, skips comment and run lines, and refuses one line past its bound.
func TestScanActionUses(t *testing.T) {
	workflow := "steps:\n  - uses: actions/checkout@" + pinTestSHA + "  # v7.0.1\n" +
		"  # - uses: actions/cache@v4\n  - run: \"echo uses: x\"\n  - uses: ./local\n"
	lines, uses, err := ScanActionUses(workflow, 6)
	if err != nil || len(lines) != 6 || len(uses) != 2 {
		t.Fatalf("scan = %d lines, %+v, %v", len(lines), uses, err)
	}
	if uses[0].Line != 1 || !uses[0].Pinned || uses[0].Pin.Release != "v7.0.1" {
		t.Fatalf("pinned use = %+v", uses[0])
	}
	if uses[1].Line != 4 || uses[1].Pinned || uses[1].SHAPinned || uses[1].Ref != "./local" {
		t.Fatalf("local use = %+v", uses[1])
	}
	_, bare, err := ScanActionUses("- uses: actions/checkout@"+pinTestSHA+"\n", 2)
	if err != nil || len(bare) != 1 || !bare[0].SHAPinned || bare[0].Pinned || bare[0].Pin.SHA != pinTestSHA {
		t.Fatalf("bare SHA use = %+v, %v", bare, err)
	}
	if _, _, err := ScanActionUses(workflow, 5); err == nil {
		t.Fatal("a workflow one line past the bound was scanned")
	}
	if lines, uses, err := ScanActionUses("", 1); err != nil || len(lines) != 1 || len(uses) != 0 {
		t.Fatalf("empty workflow = %v, %+v, %v", lines, uses, err)
	}
}

// TestScanActionUsesStructuralValues separates mapping keys from scalar text and preserves source spans.
func TestScanActionUsesStructuralValues(t *testing.T) {
	for _, workflow := range []string{
		`- {uses: actions/checkout@v4, name: "example, uses: ./local"}`,
		`- {name: "uses: ./local", "u\u0073es": "actions/checkout@v4"}`,
		"steps:\n  - name: écho\n    uses: 'actions/checkout@v4'",
		`- {name: "écho", uses: "actions/checkout@v\u0034"}`,
		"steps:\n  - run: |\n      uses: ./local\n  - uses: actions/checkout@v4",
		"steps:\n  - uses: &checkout actions/checkout@v4",
		"steps:\n  - uses: !!str actions/checkout@v4",
	} {
		_, uses, err := ScanActionUses(workflow, 10)
		if err != nil || len(uses) != 1 || uses[0].Value != "actions/checkout@v4" {
			t.Fatalf("%q: uses = %+v, %v", workflow, uses, err)
		}
		assertActionUseRewrite(t, workflow, uses[0])
	}
}

// TestScanActionUsesRefusals rejects invalid documents and never treats quoted scalar content as a comment.
func TestScanActionUsesRefusals(t *testing.T) {
	for _, text := range []string{"uses: [", "uses: {}", "uses: [./local]", "uses: *missing", "uses: ./local\n---\nuses: actions/checkout@v4"} {
		if _, _, err := ScanActionUses(text, 10); err == nil {
			t.Errorf("unsupported YAML accepted: %q", text)
		}
	}
	text := `uses: "actions/checkout@` + pinTestSHA + `  # v7.0.1"`
	_, uses, err := ScanActionUses(text, 1)
	if err != nil || len(uses) != 1 || uses[0].SHAPinned {
		t.Fatalf("quoted comment = %+v, %v; want no SHA pin", uses, err)
	}
}

// TestScanTemplateActionUsesBoundary accepts plain blocks and refuses ambiguous template lines.
func TestScanTemplateActionUsesBoundary(t *testing.T) {
	prefix := "{{if .Enabled}}\nsteps:\n"
	for _, text := range []string{
		"  - {uses: ./local}", `  - "uses": ./local`, "  - name: uses example",
		"  - uses: {{ .Action }}", "  - uses: [./local]", "  - uses: ./local\n    uses: [",
	} {
		if _, _, err := ScanTemplateActionUses(prefix+text+"\n{{end}}", 10); err == nil || !strings.Contains(err.Error(), "unsupported uses") {
			t.Errorf("%q: want unsupported uses error, got %v", text, err)
		}
	}
	text := prefix + "  # uses: ignored\n  - uses: actions/checkout@" + pinTestSHA + "  # v7.0.1\n{{end}}"
	_, uses, err := ScanTemplateActionUses(text, 6)
	if err != nil || len(uses) != 1 || uses[0].Line != 3 || !uses[0].Pinned {
		t.Fatalf("template uses = %+v, %v", uses, err)
	}
	if _, _, err := ScanTemplateActionUses(text, 4); err == nil {
		t.Fatal("template over line bound accepted")
	}
}

// TestScanTemplateActionUsesEscapedKey refuses a uses key spelled with YAML escapes: the text
// "uses" never appears on the line, yet the parser decodes the key as uses.
func TestScanTemplateActionUsesEscapedKey(t *testing.T) {
	prefix := "{{if .Enabled}}\nsteps:\n"
	for _, text := range []string{
		`  - "u\x73es": actions/checkout@v4`, `  - "\u0075ses": ./local`, `  - {"u\x73es": ./local}`,
		`  - ? "u\x73es"` + "\n    : ./local", `  - "u\x73es": {{ .Action }}`,
	} {
		_, uses, err := ScanTemplateActionUses(prefix+text+"\n{{end}}", 10)
		if err == nil || !strings.Contains(err.Error(), "unsupported uses") {
			t.Errorf("%q: want unsupported uses error, got %+v, %v", text, uses, err)
		}
	}
	// Negative: lines whose keys and values hold no uses key stay readable beside a plain pin.
	text := prefix + `  - "na\x6de": x` + "\n  - run: echo \"a\\nb\" {{ .X }}\n  - uses: ./local\n{{end}}"
	if _, uses, err := ScanTemplateActionUses(text, 10); err != nil || len(uses) != 1 || uses[0].Value != "./local" {
		t.Fatalf("plain pin beside escaped non-uses keys = %+v, %v", uses, err)
	}
}

// TestScanActionUsesQuotedAcrossBreaks keeps every line break outside a rewritten quoted scalar:
// the next sequence entry stays on its own line for LF, CRLF, CR, NEL, LS and PS.
func TestScanActionUsesQuotedAcrossBreaks(t *testing.T) {
	for name, sep := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r", "NEL": "\u0085", "LS": "\u2028", "PS": "\u2029"} {
		assertQuotedRewriteKeepsBreak(t, name, sep)
	}
}

// assertQuotedRewriteKeepsBreak rewrites a quoted uses value joined with sep and checks the break survives.
func assertQuotedRewriteKeepsBreak(t *testing.T, name, sep string) {
	t.Helper()
	workflow := strings.Join([]string{"steps:", "  - uses: 'actions/checkout@v4'", "  - uses: ./local", ""}, sep)
	_, uses, err := ScanActionUses(workflow, 10)
	if err != nil || len(uses) != 2 || workflow[uses[0].Start:uses[0].End] != "'actions/checkout@v4'" {
		t.Errorf("%s: uses = %+v, %v", name, uses, err)
		return
	}
	replacement, err := uses[0].Replacement("actions/checkout@v7")
	if err != nil {
		t.Fatal(err)
	}
	rewritten := workflow[:uses[0].Start] + replacement + workflow[uses[0].End:]
	_, after, err := ScanActionUses(rewritten, 10)
	if err != nil || len(after) != 2 || after[0].Value != "actions/checkout@v7" || after[1].Value != "./local" || after[1].Line != 2 {
		t.Errorf("%s: rewritten %q: uses = %+v, %v", name, rewritten, after, err)
	}
	if !strings.Contains(rewritten, replacement+sep+"  - uses: ./local") {
		t.Errorf("%s: separator lost in %q", name, rewritten)
	}
}

// assertActionUseRewrite checks the source span and retains scalar properties after replacement.
func assertActionUseRewrite(t *testing.T, workflow string, use ActionUse) {
	t.Helper()
	replacement, err := use.Replacement("actions/checkout@v7")
	if err != nil {
		t.Fatal(err)
	}
	rewritten := workflow[:use.Start] + replacement + workflow[use.End:]
	_, after, err := ScanActionUses(rewritten, 10)
	if err != nil || len(after) != 1 || after[0].Value != "actions/checkout@v7" {
		t.Fatalf("rewritten %q: uses = %+v, %v", rewritten, after, err)
	}
	if use.source.Anchor != after[0].source.Anchor || use.source.Tag != after[0].source.Tag {
		t.Fatalf("scalar properties changed: %q -> %q", workflow, rewritten)
	}
}

// scanUsesNoPanic runs scan and reports a panic as an error, so a planted case fails by name.
func scanUsesNoPanic(scan func() ([]ActionUse, error)) (uses []ActionUse, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return scan()
}

// Positive, negative and boundary: LF, CRLF and bare CR line endings (yaml/v3 treats a bare CR
// as a line break) read the same action on the same line with the same source span; NEL, LS and
// PS breaks are read the same way. No ending panics on an out-of-range line index.
func TestScanActionUsesLineBreaks(t *testing.T) {
	for name, sep := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r", "NEL": "\u0085", "LS": "\u2028", "PS": "\u2029"} {
		assertLineBreakScan(t, name, sep)
	}
	lines, _, err := ScanActionUses("a\r\nb\rc\nd", 4)
	if err != nil || len(lines) != 4 || lines[0] != "a\r" || lines[3] != "d" {
		t.Fatalf("mixed breaks = %q, %v", lines, err)
	}
	if _, _, err := ScanActionUses("steps:\r  - uses: ./local\r", 2); err == nil {
		t.Fatal("a bare CR workflow of three lines was scanned within a bound of two")
	}
}

// assertLineBreakScan scans one workflow joined with sep and checks lines, spans and the rewrite.
func assertLineBreakScan(t *testing.T, name, sep string) {
	t.Helper()
	action := "actions/checkout@" + pinTestSHA
	body := []string{"steps:", "  - name: écho", "  - uses: " + action + "  # v7.0.1", "  - uses: ./local", ""}
	workflow := strings.Join(body, sep)
	uses, err := scanUsesNoPanic(func() ([]ActionUse, error) {
		_, found, scanErr := ScanActionUses(workflow, 10)
		return found, scanErr
	})
	if err != nil || len(uses) != 2 {
		t.Errorf("%s: uses = %+v, %v", name, uses, err)
		return
	}
	if uses[0].Line != 2 || uses[0].Value != action || !uses[0].Pinned || workflow[uses[0].Start:uses[0].End] != action {
		t.Errorf("%s: pinned use = %+v, span %q", name, uses[0], workflow[uses[0].Start:uses[0].End])
	}
	if uses[1].Line != 3 || workflow[uses[1].Start:uses[1].End] != "./local" {
		t.Errorf("%s: local use = %+v, span %q", name, uses[1], workflow[uses[1].Start:uses[1].End])
	}
	assertLineBreakRewrite(t, name, workflow, uses[0])
}

// assertLineBreakRewrite replaces use's span and checks the document reads back at the same line.
func assertLineBreakRewrite(t *testing.T, name, workflow string, use ActionUse) {
	t.Helper()
	replacement, err := use.Replacement("actions/checkout@v7")
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := ScanActionUses(workflow[:use.Start]+replacement+workflow[use.End:], 10)
	if err != nil || len(after) != 2 || after[0].Value != "actions/checkout@v7" || after[0].Line != 2 {
		t.Errorf("%s: rewritten uses = %+v, %v", name, after, err)
	}
}

// Positive and negative: an unsupported coordinate is an error, never an index panic.
func TestActionSourceOffsetBounds(t *testing.T) {
	source := newActionSource("a\rbc\n")
	for _, at := range [][2]int{{0, 1}, {-1, 1}, {4, 1}, {1, 0}} {
		if offset, err := source.offset(at[0], at[1]); err == nil {
			t.Errorf("offset(%d, %d) = %d, want an error", at[0], at[1], offset)
		}
	}
	for at, want := range map[[2]int]int{{1, 1}: 0, {2, 1}: 2, {2, 2}: 3, {3, 1}: 5} {
		if offset, err := source.offset(at[0], at[1]); err != nil || offset != want {
			t.Errorf("offset(%d, %d) = %d, %v, want %d", at[0], at[1], offset, err, want)
		}
	}
}

// Positive and negative: a template that is not valid YAML keeps its plain inline uses lines,
// bare CR templates included, and refuses a uses header with a null value or a value on the
// next line instead of reading an empty reference that drops the action.
func TestScanTemplateActionUsesNullAndContinuation(t *testing.T) {
	action := "actions/checkout@" + pinTestSHA
	for name, sep := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r"} {
		assertTemplateLineEnding(t, name, sep, action)
	}
	prefix := "{{if .Enabled}}\nsteps:\n"
	for _, text := range []string{
		"  - uses:\n      actions/checkout@v4", "  - uses:\n", "  - uses: ~", "  - uses: null", "  - uses: !!null",
		"  - uses: &pin", "  - uses: \"\"", "  - uses: >-\n      actions/checkout@v4",
		"  - uses: actions/checkout\n      @v4", "  - uses:\r\n      actions/checkout@v4",
	} {
		_, uses, err := ScanTemplateActionUses(prefix+text+"\n{{end}}", 10)
		if err == nil || !strings.Contains(err.Error(), "unsupported uses") {
			t.Errorf("%q: want unsupported uses error, got %+v, %v", text, uses, err)
		}
	}
	_, uses, err := ScanTemplateActionUses(prefix+"  - uses: ./local\n    with:\n      x: 1\n  - uses: "+action+"\n{{end}}", 10)
	if err != nil || len(uses) != 2 {
		t.Fatalf("sibling keys after uses = %+v, %v", uses, err)
	}
}

// assertTemplateLineEnding scans an unrendered template joined with sep and checks its inline pin.
func assertTemplateLineEnding(t *testing.T, name, sep, action string) {
	t.Helper()
	workflow := strings.Join([]string{"{{if .Enabled}}", "steps:", "  - uses: " + action + "  # v7.0.1", "{{end}}"}, sep)
	uses, err := scanUsesNoPanic(func() ([]ActionUse, error) {
		_, found, scanErr := ScanTemplateActionUses(workflow, 10)
		return found, scanErr
	})
	if err != nil || len(uses) != 1 || uses[0].Line != 2 || !uses[0].Pinned || workflow[uses[0].Start:uses[0].End] != action {
		t.Errorf("%s: template uses = %+v, %v", name, uses, err)
	}
}
