// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// ownerLegacyHeaderTable uses the shared legacy header with repository-authored HISS wording.
const ownerLegacyHeaderTable = "## Core Directives & Invariants\n\n" +
	"| Invariant | Rule | Enforcement | On fail |\n" +
	"| :--- | :--- | :--- | :--- |\n" +
	"| **HISS-01** control flow | Repository call graph must be acyclic. | project graph check | reject change |\n"

// TestHarnessVerifyParagraphPreservesOwnerText checks appended, prepended, and receipt-like
// owner text, while an unchanged generated paragraph leaves nothing to preserve.
func TestHarnessVerifyParagraphPreservesOwnerText(t *testing.T) {
	const owner = "Run the repository integration checks before reporting."
	generated := repositoryOwnedVerifySummary + harnessReceiptLine
	cases := []struct{ name, paragraph, want string }{
		{"appended", generated + owner, owner},
		{"prepended", owner + "\n\n" + generated, owner},
		{"same line", strings.TrimSpace(generated) + " " + owner, owner},
		{"receipt words", "Record the Ed25519 Exit-0 receipt in the project ledger.", "Record the Ed25519 Exit-0 receipt in the project ledger."},
		{"generated gate", generatedVerifySummary + harnessReceiptLine + owner, owner},
		{"no generated target", noVerifyTargetSummary + harnessReceiptLine + owner, owner},
		{"unavailable generated target", unavailableVerifySummary + harnessReceiptLine + owner, owner},
		{"legacy gate", legacyGeneratedVerifyParagraph + " " + owner, owner},
		{"legacy only", legacyGeneratedVerifyParagraph, ""},
		{"generated only", generated, ""},
		{"empty", "\n\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := harnessTurnEndIntro + "\n```bash\nmake verify-all\n```\n\n" + tc.paragraph + "\n\n## Core Directives & Invariants\n"
			if got := repositoryVerifyParagraph(head, generated); got != tc.want {
				t.Errorf("repositoryVerifyParagraph = %q; want %q", got, tc.want)
			}
			if got := repositoryVerifyParagraph(head, "generated gate"); got != "" {
				t.Errorf("unowned verification target preserved %q", got)
			}
		})
	}
}

// TestHarnessForcePreservesOwnerTextWithGeneratedMarkers exercises a forced refresh twice:
// owner verification text follows a generated receipt and owner HISS rows use the old header.
func TestHarnessForcePreservesOwnerTextWithGeneratedMarkers(t *testing.T) {
	const owner = "Run the repository integration checks before reporting."
	repo := newTestRepo(t, "owner-sections")
	useLefthook(t, lefthookRuns)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/owner\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, makefileName), "verify-all:\n\t@echo project gate\n")
	fixture := "# owner Agent Operating Harness\n\n<!-- praetor:head -->\n\n" +
		harnessTurnEndIntro + "\n```bash\nmake verify-all\n```\n\n" +
		repositoryOwnedVerifySummary + harnessReceiptLine + owner + "\n\n" + ownerLegacyHeaderTable + "\n" +
		"## Operational Rules\n\n1. **Act on verified state.**\n\n---\n\n# Instructions\nKeep.\n"
	mustWrite(t, filepath.Join(repo, agentsFile), fixture)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true}
	var previous string
	for i := 0; i < 2; i++ {
		if _, err := Adopt(t.Context(), opts); err != nil {
			t.Fatalf("Adopt --force: %v", err)
		}
		got := mustRead(t, filepath.Join(repo, agentsFile))
		if !strings.Contains(got, owner) {
			t.Error("adopt --force lost owner text appended to generated receipt")
		}
		if !strings.Contains(got, ownerLegacyHeaderTable) {
			t.Error("adopt --force lost owner invariant table using legacy header")
		}
		if i > 0 && got != previous {
			t.Error("second Adopt --force changed preserved owner sections")
		}
		previous = got
	}
}

// TestHarnessLegacyInvariantSectionProvenance distinguishes legacy generated prose from owner
// tables sharing its header, including historical prose outside the verification paragraph.
func TestHarnessLegacyInvariantSectionProvenance(t *testing.T) {
	const intro = harnessTurnEndIntro + "\n```bash\nmake verify-all\n```\n\n"
	const operational = "\n## Operational Rules\n\n1. **Act on verified state.**\n"
	cases := []struct {
		name, before, after string
		owned               bool
	}{
		{"header alone", "", "", true},
		{"owner paragraph", intro + "Run project checks.\n\n", "", true},
		{"current receipt", intro + repositoryOwnedVerifySummary + harnessReceiptLine, "", true},
		{"historical generated", intro + legacyGeneratedVerifyParagraph + "\n\n", "", false},
		{"historical outside paragraph", "", legacyGeneratedVerifyParagraph, true},
		{"historical before block", legacyGeneratedVerifyParagraph + "\n\n" + intro, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := tc.before + ownerLegacyHeaderTable + operational + tc.after
			section, owned, err := repositoryInvariantsSection(head)
			if err != nil || owned != tc.owned {
				t.Fatalf("repositoryInvariantsSection = (%q, %t, %v); want owned %t", section, owned, err, tc.owned)
			}
			if tc.owned && section != strings.TrimSpace(ownerLegacyHeaderTable) {
				t.Errorf("owner section changed: %q", section)
			}
		})
	}
	for _, head := range []string{"", ownerLegacyHeaderTable, "## Core Directives & Invariants\n" + operational} {
		if section, owned, err := repositoryInvariantsSection(head); err != nil || owned || section != "" {
			t.Errorf("missing section boundary or rows: got (%q, %t, %v)", section, owned, err)
		}
	}
}
