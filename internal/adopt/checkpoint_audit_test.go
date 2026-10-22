// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// shippedCheckpointText returns the text this repository ships at the bundle path rel.
func shippedCheckpointText(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// checkpointAuditRepo writes texts, keyed by repository path, into a fresh directory.
func checkpointAuditRepo(t *testing.T, texts map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range texts {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	return root
}

func auditCheckpointLines(t *testing.T, root string) []string {
	t.Helper()
	lines, err := AuditCheckpointBundle(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// Negative (#239): a pre-#149 checkpoint.py, in either line-ending style, is reported once,
// naming the file and the release that shipped it, so a stale copy is visible before a
// Windows job fails on os.O_DIRECTORY.
func TestAuditCheckpointBundle_Negative_PriorScriptWarns(t *testing.T) {
	prior := firstPriorCheckpointText(t, checkpointScript)
	for style, text := range map[string]string{"lf": prior, "crlf": crlfText(prior)} {
		root := checkpointAuditRepo(t, map[string]string{
			checkpointScript:   text,
			checkpointCommon:   shippedCheckpointText(t, checkpointCommon),
			checkpointLauncher: shippedCheckpointText(t, checkpointLauncher),
		})
		lines := auditCheckpointLines(t, root)
		if len(lines) != 1 {
			t.Fatalf("%s: lines = %q, want one warning", style, lines)
		}
		for _, want := range []string{"[WARN] ", checkpointScript, "platform promotion (#14)", "praetorctl adopt"} {
			if !strings.Contains(lines[0], want) {
				t.Fatalf("%s: warning lacks %q: %s", style, want, lines[0])
			}
		}
	}
}

// Positive: the shipped bundle, an edited copy, a missing bundle and a vendored repository,
// whose lefthook.yml extends the canonical policy and keeps its scripts on purpose (BUG-858),
// stay quiet.
func TestAuditCheckpointBundle_Positive_QuietCases(t *testing.T) {
	prior := firstPriorCheckpointText(t, checkpointScript)
	cases := map[string]map[string]string{
		"current": {
			checkpointScript:   shippedCheckpointText(t, checkpointScript),
			checkpointCommon:   shippedCheckpointText(t, checkpointCommon),
			checkpointLauncher: shippedCheckpointText(t, checkpointLauncher),
		},
		"edited":  {checkpointScript: prior + "# local edit\n"},
		"missing": {},
		"vendored": {
			checkpointScript: prior,
			lefthookFile:     "extends:\n  - " + canonicalLefthookPolicy + "\n",
		},
		"vendored by remote": {
			checkpointScript: prior,
			lefthookFile:     "remotes:\n  - git_url: https://example.invalid/praetor\n    configs:\n      - ./" + canonicalLefthookPolicy + "\n",
		},
	}
	for name, texts := range cases {
		if lines := auditCheckpointLines(t, checkpointAuditRepo(t, texts)); len(lines) != 0 {
			t.Errorf("%s: lines = %q, want none", name, lines)
		}
	}
}

// The source bundle selected for adoption can differ from the installed audit binary.
func TestAuditCheckpointBundle_AfterAdoptFromSelectedSource(t *testing.T) {
	source := newAdoptLockSource(t)
	for _, rel := range checkpointBundle {
		text := firstPriorCheckpointText(t, rel)
		if rel == checkpointLauncher {
			text = mustRead(t, filepath.Join(checkpointFixtureDir(rel), "proven-339.python.sh"))
		}
		if text == "" {
			t.Fatalf("missing prior fixture for %s", rel)
		}
		mustWrite(t, filepath.Join(source, filepath.FromSlash(rel)), text)
	}
	repo := newTestRepo(t, "checkpoint-source-audit")
	adoptWithSource(t, repo, source, false)
	lines, err := AuditCheckpointBundleWithSource(t.Context(), repo, source)
	if err != nil || len(lines) != 0 {
		t.Fatalf("freshly adopted source bundle warned: %q / %v", lines, err)
	}
	if lines := auditCheckpointLines(t, repo); len(lines) != len(checkpointBundle) {
		t.Fatalf("running-release comparison lost stale warnings: %q", lines)
	}
	if err := os.Remove(filepath.Join(source, filepath.FromSlash(checkpointCommon))); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditCheckpointBundleWithSource(t.Context(), repo, source); err == nil {
		t.Fatal("incomplete selected source was silently accepted")
	}
}

// Boundary: currentCheckpointDigests names exactly the bundle this repository ships, each also
// recorded as a prior text, so a changed script fails here until both maps record it.
func TestCurrentCheckpointDigests_Boundary_MatchShippedBundle(t *testing.T) {
	if len(currentCheckpointDigests) != len(checkpointBundle) {
		t.Fatalf("currentCheckpointDigests covers %d paths, want the %d bundle files", len(currentCheckpointDigests), len(checkpointBundle))
	}
	for _, rel := range checkpointBundle {
		got := fixtureDigest(t, rel, []byte(shippedCheckpointText(t, rel)))
		if currentCheckpointDigests[rel] != got {
			t.Errorf("currentCheckpointDigests[%s] = %q, the shipped text is %s", rel, currentCheckpointDigests[rel], got)
		}
		if _, ok := priorCheckpointDigests[rel][got]; !ok {
			t.Errorf("the shipped %s (%s) is not in priorCheckpointDigests", rel, got)
		}
	}
}

func TestAuditCheckpointBundle_SourceRemedy(t *testing.T) {
	source := checkpointAuditRepo(t, map[string]string{
		checkpointScript:   shippedCheckpointText(t, checkpointScript),
		checkpointCommon:   shippedCheckpointText(t, checkpointCommon),
		checkpointLauncher: shippedCheckpointText(t, checkpointLauncher),
	})
	edited := shippedCheckpointText(t, checkpointCommon) + "# local edit\n"
	commonLF, _ := util.NormalizeLineEndings(shippedCheckpointText(t, checkpointCommon))
	mixed := crlfText(commonLF) + "# local edit\n"
	for _, tc := range []struct {
		name, root, common, want, forbidden string
	}{
		{"positive selected source", source, "", "praetorctl adopt --lock-source-root=" + source, "this release's text"},
		{"negative edited selected source", source, edited, "praetorctl adopt --lock-source-root=" + source, "this release's text"},
		{"boundary release", "", edited, "this release's text, then run praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT.", "--force"},
		{"positive running release", "", "", "praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT (no --force needed)", "the selected source bundle's"},
		{"negative mixed edited selected source", source, mixed, "adoption keeps the bundle while " + checkpointCommon, "no --force needed"},
		{"boundary mixed edited release", "", mixed, "this release's text, then run praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT.", "no --force needed"},
		{"negative CRLF edited release", "", crlfText(commonLF + "# local edit\n"), "this release's text, then run praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT.", "no --force needed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			texts := map[string]string{checkpointScript: firstPriorCheckpointText(t, checkpointScript)}
			if tc.common != "" {
				texts[checkpointCommon] = tc.common
			}
			lines, err := AuditCheckpointBundleWithSource(t.Context(), checkpointAuditRepo(t, texts), tc.root)
			if err != nil || len(lines) != 1 {
				t.Fatalf("warnings = %q, %v; want one", lines, err)
			}
			if !strings.Contains(lines[0], tc.want) || strings.Contains(lines[0], tc.forbidden) {
				t.Fatalf("warning must contain %q without %q: %s", tc.want, tc.forbidden, lines[0])
			}
		})
	}
}
