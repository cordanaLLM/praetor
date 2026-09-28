// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// The documentation gate block follows the replace-vs-refresh contract of every audit-locked
// file: an exact earlier Praetor block is refreshed on a plain run, reported as a reconcile with
// no backup, while restoring an edited block needs --force and is a replace with a backup. A
// target the new block adds is refused when the rest of the Makefile may already define it.

// refreshedMakefileDetail is the action detail of a refreshed earlier block, spelled out.
const refreshedMakefileDetail = "Refreshed an earlier Praetor documentation gate block to the current locked block"

// assertMakefileRefreshed fails unless the report holds exactly one Makefile entry, a reconcile
// with the refresh detail, and the session kept no backup.
func assertMakefileRefreshed(t *testing.T, s *adoptSession) {
	t.Helper()
	entries := 0
	for _, entry := range s.report.ActionDetails {
		if entry.Path != makefileName {
			continue
		}
		entries++
		if entry.Action != actionReconcile || entry.Details != refreshedMakefileDetail {
			t.Fatalf("Makefile entry = %+v, want reconcile %q", entry, refreshedMakefileDetail)
		}
	}
	if entries != 1 || len(s.report.Replaced()) != 0 || fileExists(backupFile(s, makefileName)) {
		t.Fatalf("Makefile entries %d, replaced %+v, backup kept %v", entries, s.report.Replaced(), fileExists(backupFile(s, makefileName)))
	}
}

// Positive: a plain run over the earlier Markdown-only block refreshes it in place, in LF and in
// a CRLF checkout, keeps every operator line and takes no backup; a dry run reports the same
// refresh and writes nothing.
func TestReconcileDocumentationMakefile_Positive_PriorBlockIsRefreshed(t *testing.T) {
	for name, crlf := range map[string]bool{"lf": false, "crlf": true} {
		before, after := documentationMakefileAround(priorDocumentationBlock), documentationMakefileAround(DocumentationMakefileBlock())
		if crlf {
			before, after = strings.ReplaceAll(before, "\n", "\r\n"), strings.ReplaceAll(after, "\n", "\r\n")
		}
		s := backupSession(t, map[string]string{makefileName: before}, true, AdoptOptions{})
		if err := reconcileDocumentationMakefile(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertMakefileRefreshed(t, s)
		if got := mustRead(t, filepath.Join(s.repoPath, makefileName)); got != after {
			t.Fatalf("%s: Makefile = %q", name, got)
		}
	}
	dry := backupSession(t, map[string]string{makefileName: documentationMakefileAround(priorDocumentationBlock)}, true, AdoptOptions{DryRun: true})
	if err := reconcileDocumentationMakefile(t.Context(), dry); err != nil {
		t.Fatal(err)
	}
	assertMakefileRefreshed(t, dry)
	if got := mustRead(t, filepath.Join(dry.repoPath, makefileName)); got != documentationMakefileAround(priorDocumentationBlock) {
		t.Fatalf("a dry run wrote the Makefile: %q", got)
	}
	if planned := string(dry.dryRunWrites[makefileName]); planned != documentationMakefileAround(DocumentationMakefileBlock()) {
		t.Fatalf("dry run planned %q", planned)
	}
}

// Negative: an edited earlier block is still a replace under --force, with its delta and a
// backup of the edited Makefile, and is refused on a plain run; a docs-figures recipe the
// operator defines outside the earlier block stops the refresh, as it stops a first attachment,
// on a plain run and under --force alike, and leaves the Makefile as it was.
func TestReconcileDocumentationMakefile_Negative_EditedOrCollidingPriorBlockIsNoRefresh(t *testing.T) {
	edited := documentationMakefileAround(strings.Replace(priorDocumentationBlock, "@node", "@npx", 1))
	plain := backupSession(t, map[string]string{makefileName: edited}, true, AdoptOptions{})
	if err := reconcileDocumentationMakefile(t.Context(), plain); err == nil || mustRead(t, filepath.Join(plain.repoPath, makefileName)) != edited {
		t.Fatalf("an edited earlier block was rewritten without --force (err %v)", err)
	}
	forced := backupSession(t, map[string]string{makefileName: edited}, true, AdoptOptions{Force: true})
	if err := reconcileDocumentationMakefile(t.Context(), forced); err != nil {
		t.Fatal(err)
	}
	entry := replacedEntry(t, forced.report, makefileName)
	if !strings.HasPrefix(entry.Details, "Restored the locked documentation gate block; replaced existing content (") {
		t.Fatalf("edited earlier block entry = %+v", entry)
	}
	if got := mustRead(t, backupFile(forced, makefileName)); got != edited {
		t.Fatalf("backup = %q", got)
	}
	colliding := documentationMakefileAround(priorDocumentationBlock) + "\ndocs-figures:\n\t@echo operator\n"
	for name, opts := range map[string]AdoptOptions{"plain": {}, "force": {Force: true}} {
		s := backupSession(t, map[string]string{makefileName: colliding}, true, opts)
		err := reconcileDocumentationMakefile(t.Context(), s)
		if err == nil || !strings.Contains(err.Error(), "may define target docs-figures outside the Praetor-managed block") {
			t.Fatalf("%s: an operator docs-figures recipe beside the earlier block was not refused: %v", name, err)
		}
		if got := mustRead(t, filepath.Join(s.repoPath, makefileName)); got != colliding {
			t.Fatalf("%s: the refused Makefile changed: %q", name, got)
		}
	}
}

// Boundary: the earlier block without a line after its end marker is no exact block, so --force
// restores it as a replace rather than refreshing it. An edited current block that still
// defines both targets is restored under --force even beside an include, since restoring it
// adds no target; one whose edit dropped docs-figures is refused beside that include.
func TestReconcileDocumentationMakefile_Boundary_UnterminatedPriorAndRestoredTargets(t *testing.T) {
	unterminated := "build:\n\t@echo build\n\n" + strings.TrimSuffix(priorDocumentationBlock, "\n")
	s := backupSession(t, map[string]string{makefileName: unterminated}, true, AdoptOptions{Force: true})
	if err := reconcileDocumentationMakefile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if entry := replacedEntry(t, s.report, makefileName); !strings.HasPrefix(entry.Details, "Restored the locked documentation gate block;") {
		t.Fatalf("unterminated earlier block entry = %+v", entry)
	}
	block := DocumentationMakefileBlock()
	keeps := "include local.mk\n\n" + strings.Replace(block, "docs-lint:\n", "docs-lint: extra\n", 1)
	restore := backupSession(t, map[string]string{makefileName: keeps}, true, AdoptOptions{Force: true})
	if err := reconcileDocumentationMakefile(t.Context(), restore); err != nil {
		t.Fatalf("restoring an edited block that keeps its targets beside an include: %v", err)
	}
	replacedEntry(t, restore.report, makefileName)
	drops := "include local.mk\n\n" + strings.Replace(block, "docs-figures:\n", "figures:\n", 1)
	refused := backupSession(t, map[string]string{makefileName: drops}, true, AdoptOptions{Force: true})
	err := reconcileDocumentationMakefile(t.Context(), refused)
	if err == nil || !strings.Contains(err.Error(), "may define target docs-figures") {
		t.Fatalf("restoring a block that dropped docs-figures beside an include: %v", err)
	}
}
