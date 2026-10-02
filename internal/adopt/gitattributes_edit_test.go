// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// The .gitattributes block is audit-locked, so it follows the replace-vs-refresh contract: an
// unedited block is verified or moved to the tail without a backup, an edited block is restored
// only under --force, as a replace with its delta and a backup, and a disable that would remove
// the block refuses an edited one (TestPreflightAttributes_Boundary_EditedBlock covers the
// disable that keeps the DevContainer rule).

// editedAttributeBlock is figureAttributeBlock with one rule an operator added inside it.
var editedAttributeBlock = strings.Replace(figureAttributeBlock, "docs/figures/*.ts text eol=lf\n",
	"docs/figures/*.ts text eol=lf\n*.png binary\n", 1)

// reconcileAttributes runs the enabled attribute step over a repository whose .gitattributes is
// text, with opts.
func reconcileAttributes(t *testing.T, text string, opts AdoptOptions) (*adoptSession, error) {
	t.Helper()
	s := backupSession(t, map[string]string{gitAttributesFile: text}, true, opts)
	return s, reconcileGitAttributes(t.Context(), s, DocumentationAttributes())
}

// Positive: --force restores an edited block as a replace whose delta names the operator's line
// and whose backup holds the edited file; an unedited block with operator rules after it moves to
// the tail as an append, with no backup.
func TestReconcileGitAttributes_Positive_EditedBlockReplacedUnderForce(t *testing.T) {
	edited := "* text=auto\n\n" + editedAttributeBlock
	s, err := reconcileAttributes(t, edited, AdoptOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, s.report, gitAttributesFile)
	if !strings.HasPrefix(entry.Details, "Restored the managed attribute block at the tail; replaced existing content (-1/+0 lines, removed \"*.png binary\")") {
		t.Fatalf("replace entry = %+v", entry)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != "* text=auto\n\n"+figureAttributeBlock {
		t.Fatalf(".gitattributes = %q", got)
	}
	if got := mustRead(t, backupFile(s, gitAttributesFile)); got != edited {
		t.Fatalf("backup = %q", got)
	}
	moved, err := reconcileAttributes(t, figureAttributeBlock+"* text=auto\n", AdoptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if action, _ := actionOf(moved.report, gitAttributesFile); action.Action != actionAppend || len(moved.report.Replaced()) != 0 ||
		fileExists(backupFile(moved, gitAttributesFile)) {
		t.Fatalf("moving an unedited block: %+v", moved.report.ActionDetails)
	}
}

// Negative: without --force an edited block is refused and left as it is, with no backup; a
// disable that removes the block, dev-container being declined, refuses to remove an edited
// one under --force too, in its preflight and in the step itself.
func TestReconcileGitAttributes_Negative_EditedBlockRefused(t *testing.T) {
	edited := "* text=auto\n\n" + editedAttributeBlock
	s, err := reconcileAttributes(t, edited, AdoptOptions{})
	if err == nil || !strings.Contains(err.Error(), "managed attribute block was edited; review it and rerun "+s.forceCommand()) {
		t.Fatalf("an edited block without --force: %v", err)
	}
	if got := mustRead(t, filepath.Join(s.repoPath, gitAttributesFile)); got != edited || fileExists(backupFile(s, gitAttributesFile)) {
		t.Fatalf("the refused .gitattributes changed or was backed up: %q", got)
	}
	disable := backupSession(t, map[string]string{gitAttributesFile: edited}, true, AdoptOptions{Force: true})
	disable.declined = []string{devContainerStep}
	if err := preflightDocumentationAttributes(t.Context(), disable); err == nil || !strings.Contains(err.Error(), "refusing to remove the edited managed attribute block") {
		t.Fatalf("the disable preflight accepted an edited block: %v", err)
	}
	if err := reconcileGitAttributes(t.Context(), disable, nil); err == nil {
		t.Fatal("a disable removed an edited block")
	}
	if got := mustRead(t, filepath.Join(disable.repoPath, gitAttributesFile)); got != edited {
		t.Fatalf("the refused disable changed .gitattributes: %q", got)
	}
}

// Boundary: a CRLF checkout of the canonical block is verified, not edited; the canonical block
// without a final newline after its end marker is no edit either, so a plain run restores the
// newline as an append; a block whose one rule differs by a trailing space is an edit.
func TestReconcileGitAttributes_Boundary_LineEndingsAreNoEdit(t *testing.T) {
	crlf, err := reconcileAttributes(t, strings.ReplaceAll("* text=auto\n\n"+figureAttributeBlock, "\n", "\r\n"), AdoptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if action, _ := actionOf(crlf.report, gitAttributesFile); action.Details != "Managed attribute block already at the tail" {
		t.Fatalf("a CRLF canonical block: %+v", action)
	}
	unterminated, err := reconcileAttributes(t, "* text=auto\n\n"+strings.TrimSuffix(figureAttributeBlock, "\n"), AdoptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if action, _ := actionOf(unterminated.report, gitAttributesFile); action.Action != actionAppend || len(unterminated.report.Replaced()) != 0 {
		t.Fatalf("a canonical block without its final newline: %+v", unterminated.report.ActionDetails)
	}
	spaced := strings.Replace(figureAttributeBlock, "-text\n", "-text \n", 1)
	if _, err := reconcileAttributes(t, spaced, AdoptOptions{}); err == nil {
		t.Fatal("a rule with a trailing space was not an edit")
	}
}
