// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
