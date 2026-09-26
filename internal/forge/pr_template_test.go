// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// readTemplate loads the shipped pull request template.
func readTemplate(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "pull_request_template.md"))
	if err != nil {
		t.Fatalf("read pull request template: %v", err)
	}
	return string(data)
}

// tickAll marks every checkbox, which is what an honest contributor does before submitting.
func tickAll(body string) string {
	return strings.ReplaceAll(body, "- [ ]", "- [x]")
}

// The template must satisfy the validator that reads it. Before this, filling it in completely
// and honestly still failed two of three checks, with an error naming requirements the template
// appeared to already cover (#110).
func TestShippedTemplateSatisfiesItsOwnChecklist(t *testing.T) {
	result, err := ValidatePRChecklist(tickAll(readTemplate(t)))
	if err != nil {
		t.Fatalf("parse filled template: %v", err)
	}
	if !result.HasHISS16Check {
		t.Error("a fully ticked template must satisfy the HISS-16 check")
	}
	if !result.Has3DTestsCheck {
		t.Error("a fully ticked template must satisfy the HISS-15 3D testing check: " +
			"the matchable token has to sit on a checkbox line, not only in the section heading")
	}
}

// Negative: an unticked template must not satisfy the checklist, or the gate would pass a
// contributor who confirmed nothing.
func TestUntickedTemplateSatisfiesNothing(t *testing.T) {
	result, err := ValidatePRChecklist(readTemplate(t))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	if result.HasHISS16Check || result.Has3DTestsCheck {
		t.Error("an unticked template must not satisfy any mandatory check")
	}
}

// The receipt fence must carry the label the extractor requires. A `text` fence is treated as a
// code snippet in a description and never read, so the pasted receipt was silently ignored.
func TestShippedTemplateReceiptFenceIsLabelled(t *testing.T) {
	template := readTemplate(t)
	var receiptFences, textFences int
	for _, line := range strings.Split(template, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		if isReceiptFence(trimmed) {
			receiptFences++
		}
		if strings.EqualFold(strings.TrimLeft(trimmed, "`"), "text") {
			textFences++
		}
	}
	if receiptFences == 0 {
		t.Error("the template must offer a fence the receipt extractor reads; `text` is not one")
	}
	if textFences > 0 {
		t.Errorf("a %d-fence `text` block invites pasting the receipt where it is never read", textFences)
	}
}

// Boundary: the template must direct the contributor at the artifact the validator verifies.
// It previously asked for gate stdout, which does not parse as the signed envelope.
func TestShippedTemplateAsksForTheSignedReceipt(t *testing.T) {
	template := readTemplate(t)
	if !strings.Contains(template, ".standards-receipt.json") {
		t.Error("the template must name the signed receipt file the validator parses")
	}
	if strings.Contains(template, "Paste stdout/stderr") {
		t.Error("gate stdout is not the artifact the validator verifies; asking for it guarantees a failure")
	}
}

// fencedReceipt wraps a signed receipt body in the given opening and closing fence lines.
func fencedReceipt(t *testing.T, open, closing string) string {
	t.Helper()
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	block := signedReceiptBlock(t, priv, "abc", "out")
	block = strings.Replace(block, "```receipt", open, 1)
	return strings.Replace(block, "\n```\n", "\n"+closing+"\n", 1)
}

// Positive: only a ticked task-list item counts, whichever list marker it uses, and a
// ~~~ receipt fence is read exactly like a ``` one (BUG-878).
func TestChecklistCountsListItemsAndTildeReceipt(t *testing.T) {
	boxes := "## Checklist\n* [x] HISS-16 compile-context verified\n1. [X] HISS-15 3D tests pass\n"
	res, err := ValidatePRChecklist(boxes + fencedReceipt(t, "~~~receipt", "~~~"))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !res.HasHISS16Check || !res.Has3DTestsCheck {
		t.Fatalf("ticked list items were not counted: %+v", res)
	}
	if !res.HasReceipt || !res.Valid {
		t.Fatalf("a ~~~receipt block must be verified like a ```receipt block: %+v", res)
	}
}

// Negative: a "[x]" inside a code fence or in the middle of a sentence is not a ticked box,
// so an example quoted in the description cannot satisfy the gate (BUG-878).
func TestChecklistIgnoresFencedAndMidSentenceBoxes(t *testing.T) {
	cases := map[string]string{
		"backtick fence": "```\n- [x] HISS-16 compile-context\n- [x] HISS-15 3D tests\n```\n",
		"tilde fence":    "~~~markdown\n- [x] HISS-16 compile-context\n- [x] HISS-15 3D tests\n~~~\n",
		"mid-sentence":   "Tick [x] for HISS-16 compile-context and [x] for HISS-15 3D tests.\n",
		"no list marker": "[x] HISS-16 compile-context\n[x] HISS-15 3D tests\n",
	}
	for name, body := range cases {
		res, err := ValidatePRChecklist(body)
		if err != nil {
			t.Fatalf("%s: validate: %v", name, err)
		}
		if res.HasHISS16Check || res.Has3DTestsCheck {
			t.Errorf("%s: a box that is not a ticked task-list item was counted: %+v", name, res)
		}
	}
}

// Negative: a receipt label quoted inside another fence is that fence's content, not a
// receipt; the old scanner toggled on every ``` line and read it.
func TestReceiptLabelInsideAnotherFenceIsNotAReceipt(t *testing.T) {
	inner := fencedReceipt(t, "```receipt", "```")
	body := prChecklistBoxes + "\n~~~~\n" + inner + "\n~~~~\n"
	res, err := ValidatePRChecklist(body)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.HasReceipt {
		t.Fatalf("a receipt quoted inside a tilde example fence was accepted: %+v", res)
	}
}

// Boundary: an unterminated fence swallows the rest of the body, so it is rejected with the
// line it opened on instead of silently dropping the boxes and receipt after it.
func TestChecklistRejectsUnterminatedFence(t *testing.T) {
	body := "```\nexample\n" + prChecklistBoxes
	res, err := ValidatePRChecklist(body)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.Valid || res.HasHISS16Check {
		t.Fatalf("boxes after an unterminated fence must not count: %+v", res)
	}
	if !containsFragment(res.Errors, "opened at line 1") {
		t.Fatalf("the unterminated fence was not reported with its line: %v", res.Errors)
	}

	unclosed := prChecklistBoxes + strings.TrimSuffix(fencedReceipt(t, "~~~receipt", "~~~"), "~~~\n")
	res, err = ValidatePRChecklist(unclosed)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if res.HasReceipt || !containsFragment(res.Errors, "never closed") {
		t.Fatalf("an unclosed receipt fence must be rejected, got: %+v", res)
	}
}

// Boundary: a box with no trailing text and tab separators are task-list items; a box glued
// to its text, a missing separator or a quote marker is not. A longer closing run closes.
func TestChecklistBoxEdgeSpellings(t *testing.T) {
	for _, line := range []string{"- [x]", "-\t[x]\tHISS-16 compile-context", "+ [X] hiss-16", "123456789) [x] a"} {
		if !checkedBoxRegex.MatchString(line) {
			t.Errorf("%q is a ticked task-list item", line)
		}
	}
	for _, line := range []string{"- [x]HISS-16", "-[x] HISS-16", "> [x] HISS-16", "1234567890. [x] a"} {
		if checkedBoxRegex.MatchString(line) {
			t.Errorf("%q is not a ticked task-list item", line)
		}
	}
	block, err := extractReceiptBlock(strings.Split("~~~ receipt\n{\"a\":1}\n~~~~~\n", "\n"))
	if err != nil || block != "{\"a\":1}" {
		t.Fatalf("a longer closing run must close the receipt fence: %q, %v", block, err)
	}
}

// containsFragment reports whether any validation error mentions fragment.
func containsFragment(errs []string, fragment string) bool {
	return slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, fragment) })
}
