package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pinnedGitIgnoreBlockVersions pins the exact rules accepted for each version of the
// managed .gitignore block, including the current canonical version.
// A change to canonical rules without bumping the version and appending the predecessor
// to gitIgnoreBlockHistory fails TestGitIgnoreBlockHistoryGuard.
var pinnedGitIgnoreBlockVersions = map[string][]string{
	"v1": {
		"/.workingdir/",
		"/.workingdir2/",
		"/.standards/worktrees/",
		"/.agents/mcp_config.json",
	},
	"v2": {
		"/.workingdir/",
		"/.workingdir2/",
		"/.standards/worktrees/",
		"/.agents/mcp_config.json",
		"/.standards/cache/",
	},
	"v3": {
		".workingdir/",
		".workingdir2/",
		"/.standards/worktrees/",
		"/.agents/mcp_config.json",
		"/.standards/cache/",
	},
	"v4": {
		".workingdir/",
		".workingdir2/",
		"/.standards/worktrees/",
		"/.agents/mcp_config.json",
		"/.standards/cache/",
		"/.claude/settings.local.json",
		"/.standards-receipt.json",
	},
	"v5": {
		".workingdir/",
		".workingdir2/",
		"/.standards/worktrees/",
		"/.agents/mcp_config.json",
		"/.standards/cache/",
		"/.claude/settings.local.json",
		"/.standards-receipt.json",
		"/.codacy/",
		".github/instructions/codacy.instructions.md",
		".cursor/rules/codacy.mdc",
	},
}

// Positive: LookupManagedGitIgnoreTail recognizes current and earlier versions across all
// permutations of legacy scratch retirement and config directory negation, in LF and CRLF.
func TestLookupManagedGitIgnoreTail_Positive(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"v5", "v4", "v3", "v2", "v1"} {
		block := gitIgnoreBlockVersion{Version: version, Rules: pinnedGitIgnoreBlockVersions[version]}
		t.Run(version, func(t *testing.T) {
			for _, keepLegacy := range []bool{true, false} {
				for _, negateConfig := range []bool{true, false} {
					text := "dist/\n\n" + block.Render(keepLegacy, negateConfig)
					assertManagedGitIgnoreTail(t, root, text, version == "v5", version)
				}
			}
		})
	}
}

// assertManagedGitIgnoreTail checks that text ends in a recognised managed block of wantVersion,
// with LF and with CRLF line ends.
func assertManagedGitIgnoreTail(t *testing.T, root, text string, wantCurrent bool, wantVersion string) {
	t.Helper()
	match, ok := LookupManagedGitIgnoreTail(t.Context(), root, text)
	if !ok {
		t.Fatalf("block not recognized:\n%s", text)
	}
	if match.Current != wantCurrent || match.Version != wantVersion {
		t.Fatalf("got Current=%v Version=%q, want %v %q", match.Current, match.Version, wantCurrent, wantVersion)
	}
	if !HasManagedGitIgnoreTail(t.Context(), root, text) {
		t.Fatalf("HasManagedGitIgnoreTail returned false for valid block:\n%s", text)
	}
	crlfMatch, crlfOk := LookupManagedGitIgnoreTail(t.Context(), root, strings.ReplaceAll(text, "\n", "\r\n"))
	if !crlfOk || crlfMatch.Version != wantVersion {
		t.Fatalf("CRLF block lookup failed: ok=%v, match=%+v", crlfOk, crlfMatch)
	}
}

// Negative: hand-edited, modified, or unknown blocks fail lookup.
func TestLookupManagedGitIgnoreTail_Negative(t *testing.T) {
	root := t.TempDir()

	for name, text := range map[string]string{
		"hand-edited rule inside block": gitIgnoreManagedBegin + "\n" +
			"/.workingdir/\n" +
			"/hand-edited-rule/\n" +
			gitIgnoreManagedEnd + "\n",
		"missing required workingdir rule": gitIgnoreManagedBegin + "\n" +
			"/.standards/worktrees/\n" +
			gitIgnoreManagedEnd + "\n",
		"arbitrary rules between markers": gitIgnoreManagedBegin + "\n" +
			"node_modules/\n" +
			gitIgnoreManagedEnd + "\n",
		"unterminated block": gitIgnoreManagedBegin + "\n" +
			"/.workingdir/\n",
		"only end marker": "/.workingdir/\n" +
			gitIgnoreManagedEnd + "\n",
		"empty block": gitIgnoreManagedBegin + "\n" +
			gitIgnoreManagedEnd + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			match, ok := LookupManagedGitIgnoreTail(t.Context(), root, text)
			if ok || match.Matched {
				t.Fatalf("invalid block was accepted: %+v", match)
			}
			if HasManagedGitIgnoreTail(t.Context(), root, text) {
				t.Fatalf("HasManagedGitIgnoreTail accepted invalid block:\n%s", text)
			}
		})
	}
}

// Boundary: files without managed blocks, empty files, and plain comments fail lookup without error.
func TestLookupManagedGitIgnoreTail_Boundary(t *testing.T) {
	root := t.TempDir()

	for name, text := range map[string]string{
		"empty string":           "",
		"whitespace only":        "   \n\t\n",
		"regular gitignore only": "bin/\n*.test\ncoverage.txt\n",
		"unmarked rules":         "/.workingdir/\n/.workingdir2/\n",
	} {
		t.Run(name, func(t *testing.T) {
			match, ok := LookupManagedGitIgnoreTail(t.Context(), root, text)
			if ok || match.Matched {
				t.Fatalf("boundary text %s matched: %+v", name, match)
			}
			if HasManagedGitIgnoreTail(t.Context(), root, text) {
				t.Fatalf("HasManagedGitIgnoreTail returned true for boundary text %s", name)
			}
		})
	}
}

// Guard: TestGitIgnoreBlockHistoryGuard verifies that when the canonical block changes,
// its previous version must be appended to gitIgnoreBlockHistory and all versions match pinned literals.
func TestGitIgnoreBlockHistoryGuard(t *testing.T) {
	assertHistoryInvariants(t)
	assertPinnedCanonicalRules(t)
	assertPinnedHistoryRules(t)
	assertHistoryTransitions(t)
}

func assertPinnedCanonicalRules(t *testing.T) {
	t.Helper()
	pinnedCanonical, ok := pinnedGitIgnoreBlockVersions[canonicalGitIgnoreBlockVersion.Version]
	if !ok {
		t.Fatalf("canonical version %q is not pinned in pinnedGitIgnoreBlockVersions", canonicalGitIgnoreBlockVersion.Version)
	}
	if !slices.Equal(canonicalGitIgnoreBlockVersion.Rules, pinnedCanonical) {
		t.Fatalf("canonical rules differ from pinned record for %s: got %v, want %v; to change canonical rules, bump Version and append previous to gitIgnoreBlockHistory",
			canonicalGitIgnoreBlockVersion.Version, canonicalGitIgnoreBlockVersion.Rules, pinnedCanonical)
	}
}

func assertPinnedHistoryRules(t *testing.T) {
	t.Helper()
	for version, rules := range pinnedGitIgnoreBlockVersions {
		if version == canonicalGitIgnoreBlockVersion.Version {
			continue
		}
		found := false
		for _, h := range gitIgnoreBlockHistory {
			if h.Version == version {
				found = true
				if !slices.Equal(h.Rules, rules) {
					t.Fatalf("historical version %s rules differ from pinned record: got %v, want %v",
						version, h.Rules, rules)
				}
				break
			}
		}
		if !found {
			t.Fatalf("pinned historical version %s is missing from gitIgnoreBlockHistory", version)
		}
	}
	for _, h := range gitIgnoreBlockHistory {
		pinned, ok := pinnedGitIgnoreBlockVersions[h.Version]
		if !ok {
			t.Fatalf("history entry %s is not pinned in pinnedGitIgnoreBlockVersions", h.Version)
		}
		if !slices.Equal(h.Rules, pinned) {
			t.Fatalf("history entry %s differs from pinned record: got %v, want %v", h.Version, h.Rules, pinned)
		}
	}
}

func assertHistoryTransitions(t *testing.T) {
	t.Helper()
	v1 := gitIgnoreBlockHistory[0]
	if err := checkBlockHistoryTransition(v1, canonicalGitIgnoreBlockVersion, gitIgnoreBlockHistory); err != nil {
		t.Fatalf("checkBlockHistoryTransition failed for valid history: %v", err)
	}

	mutatedCanonical := gitIgnoreBlockVersion{
		Version: "v5",
		Rules:   append(slices.Clone(canonicalGitIgnoreBlockVersion.Rules), "/.new-tool-cache/"),
	}
	err := checkBlockHistoryTransition(canonicalGitIgnoreBlockVersion, mutatedCanonical, gitIgnoreBlockHistory)
	if err == nil {
		t.Fatal("checkBlockHistoryTransition passed when canonical block changed without appending previous to history")
	}
	if !strings.Contains(err.Error(), "without appending it to history") {
		t.Fatalf("unexpected error message: %v", err)
	}

	updatedHistory := append(slices.Clone(gitIgnoreBlockHistory), canonicalGitIgnoreBlockVersion)
	if err := checkBlockHistoryTransition(canonicalGitIgnoreBlockVersion, mutatedCanonical, updatedHistory); err != nil {
		t.Fatalf("checkBlockHistoryTransition failed with updated history: %v", err)
	}
}

func assertHistoryInvariants(t *testing.T) {
	t.Helper()
	if len(gitIgnoreBlockHistory) == 0 {
		t.Fatal("gitIgnoreBlockHistory must not be empty")
	}
	if len(gitIgnoreBlockHistory) > maxHistoryVersions {
		t.Fatalf("gitIgnoreBlockHistory length %d exceeds bound %d", len(gitIgnoreBlockHistory), maxHistoryVersions)
	}

	seen := make(map[string]bool, len(gitIgnoreBlockHistory)+1)
	seen[canonicalGitIgnoreBlockVersion.Version] = true
	for _, v := range gitIgnoreBlockHistory {
		if v.Version == "" {
			t.Fatal("historical version has empty Version string")
		}
		if seen[v.Version] {
			t.Fatalf("duplicate version identifier: %q", v.Version)
		}
		seen[v.Version] = true
		if len(v.Rules) == 0 {
			t.Fatalf("historical version %s has no rules", v.Version)
		}
		if slices.Equal(v.Rules, canonicalGitIgnoreBlockVersion.Rules) {
			t.Fatalf("historical version %s has identical rules to canonical", v.Version)
		}
	}
}

// Positive: returned rules slices from RulesFor and managedGitIgnoreRules are decoupled clones
// so appending or mutating elements cannot corrupt the canonical or historical definitions.
func TestGitIgnoreRulesImmutability(t *testing.T) {
	canonicalBefore := slices.Clone(managedIgnoreRules)
	v1Before := slices.Clone(gitIgnoreBlockHistory[0].Rules)

	for _, keepLegacy := range []bool{true, false} {
		for _, negateConfig := range []bool{true, false} {
			rules := canonicalGitIgnoreBlockVersion.RulesFor(keepLegacy, negateConfig)
			_ = append(rules, "/.mutated-cache/")
			if len(rules) > 0 {
				rules[0] = "/.mutated-root/"
			}

			histRules := gitIgnoreBlockHistory[0].RulesFor(keepLegacy, negateConfig)
			_ = append(histRules, "/.mutated-cache/")
			if len(histRules) > 0 {
				histRules[0] = "/.mutated-root/"
			}

			managed := managedGitIgnoreRules(keepLegacy, negateConfig)
			_ = append(managed, "/.mutated-cache/")
			if len(managed) > 0 {
				managed[0] = "/.mutated-root/"
			}
		}
	}

	if !slices.Equal(managedIgnoreRules, canonicalBefore) {
		t.Fatalf("managedIgnoreRules was mutated: got %v, want %v", managedIgnoreRules, canonicalBefore)
	}
	if !slices.Equal(canonicalGitIgnoreBlockVersion.Rules, canonicalBefore) {
		t.Fatalf("canonicalGitIgnoreBlockVersion.Rules was mutated: got %v, want %v", canonicalGitIgnoreBlockVersion.Rules, canonicalBefore)
	}
	if !slices.Equal(gitIgnoreBlockHistory[0].Rules, v1Before) {
		t.Fatalf("gitIgnoreBlockHistory[0].Rules was mutated: got %v, want %v", gitIgnoreBlockHistory[0].Rules, v1Before)
	}

	v1Copy := gitIgnoreBlockHistory[0].Rules
	_ = append(v1Copy, "/.appended-rule/")
	if !slices.Equal(managedIgnoreRules, canonicalBefore) {
		t.Fatalf("appending to gitIgnoreBlockHistory[0].Rules affected managedIgnoreRules: got %v, want %v", managedIgnoreRules, canonicalBefore)
	}
}

// Positive: state-ledger-integrity.md references this test file and TestLookupManagedGitIgnoreTail_Positive.
func TestGitIgnoreHistory_GuideReferencesHistoryTest_Positive(t *testing.T) {
	guidePath := filepath.Join("..", "..", "docs", "guides", "state-ledger-integrity.md")
	content, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read guide: %v", err)
	}
	text := string(content)
	wantFile := "internal/adopt/gitignore_history_test.go"
	wantTest := "TestLookupManagedGitIgnoreTail_Positive"
	if !strings.Contains(text, wantFile) {
		t.Fatalf("state-ledger-integrity.md must reference %s: missing from text", wantFile)
	}
	if !strings.Contains(text, wantTest) {
		t.Fatalf("state-ledger-integrity.md must reference %s: missing from text", wantTest)
	}
}

// Negative: unreferenced or fictitious test files are not cited in the guide.
func TestGitIgnoreHistory_GuideReferencesHistoryTest_Negative(t *testing.T) {
	guidePath := filepath.Join("..", "..", "docs", "guides", "state-ledger-integrity.md")
	content, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read guide: %v", err)
	}
	text := string(content)
	fictitious := "internal/adopt/fictitious_gitignore_test.go"
	if strings.Contains(text, fictitious) {
		t.Fatalf("state-ledger-integrity.md must not reference fictitious file %q", fictitious)
	}
}

// Boundary: historical compatibility claims for both block v3 and block v4 are linked.
func TestGitIgnoreHistory_GuideReferencesHistoryTest_Boundary(t *testing.T) {
	guidePath := filepath.Join("..", "..", "docs", "guides", "state-ledger-integrity.md")
	content, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read guide: %v", err)
	}
	text := string(content)
	claim := "A repository still carrying block v3 or v4 is recognised and re-rendered, not refused"
	assertGuideClaimReference(t, text, claim, "internal/adopt/gitignore_history_test.go", 250)
}

// assertGuideClaimReference asserts that claim appears in text and that wantRef appears
// within windowLen bytes following the start of claim. The window is clamped to len(text)
// so a claim near the end reports a missing reference rather than panicking on slice bounds.
func assertGuideClaimReference(t testing.TB, text, claim, wantRef string, windowLen int) {
	t.Helper()
	if !strings.Contains(text, claim) {
		t.Fatalf("state-ledger-integrity.md missing historical compatibility claim: %q", claim)
		return
	}
	idx := strings.Index(text, claim)
	end := min(len(text), idx+windowLen)
	window := text[idx:end]
	if !strings.Contains(window, wantRef) {
		t.Fatalf("historical compatibility claim must be followed by %s reference, got window: %q", wantRef, window)
	}
}

type testRecorder struct {
	testing.TB
	failed  bool
	message string
}

func (r *testRecorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

// Positive: assertGuideClaimReference succeeds when the reference sits within windowLen,
// including when the reference sits near the end of text shorter than claim + windowLen.
func TestGitIgnoreHistory_GuideClaimWindow_Positive(t *testing.T) {
	claim := "A repository still carrying block v3 or v4 is recognised and re-rendered, not refused"
	ref := "internal/adopt/gitignore_history_test.go"

	longText := claim + " (`TestLookupManagedGitIgnoreTail_Positive` in " + ref + ")\n" + strings.Repeat("x", 500)
	assertGuideClaimReference(t, longText, claim, ref, 250)

	shortText := claim + " (" + ref + ")"
	assertGuideClaimReference(t, shortText, claim, ref, 250)
}

// Negative: assertGuideClaimReference reports missing reference with Fatalf rather than panicking,
// both when text continues past windowLen and when text ends within windowLen.
func TestGitIgnoreHistory_GuideClaimWindow_Negative(t *testing.T) {
	claim := "A repository still carrying block v3 or v4 is recognised and re-rendered, not refused"
	ref := "internal/adopt/gitignore_history_test.go"

	recShort := &testRecorder{TB: t}
	shortMissing := claim + " (reference omitted near end)"
	assertGuideClaimReference(recShort, shortMissing, claim, ref, 250)
	if !recShort.failed || !strings.Contains(recShort.message, "must be followed by") {
		t.Fatalf("expected missing reference report on short text, got failed=%v msg=%q", recShort.failed, recShort.message)
	}

	recLong := &testRecorder{TB: t}
	longMissing := claim + " (omitted)\n" + strings.Repeat("x", 500)
	assertGuideClaimReference(recLong, longMissing, claim, ref, 250)
	if !recLong.failed || !strings.Contains(recLong.message, "must be followed by") {
		t.Fatalf("expected missing reference report on long text, got failed=%v msg=%q", recLong.failed, recLong.message)
	}
}

// Boundary: text ending exactly at the claim or missing the claim entirely reports failures without panicking.
func TestGitIgnoreHistory_GuideClaimWindow_Boundary(t *testing.T) {
	claim := "A repository still carrying block v3 or v4 is recognised and re-rendered, not refused"
	ref := "internal/adopt/gitignore_history_test.go"

	recExact := &testRecorder{TB: t}
	assertGuideClaimReference(recExact, claim, claim, ref, 250)
	if !recExact.failed || !strings.Contains(recExact.message, "must be followed by") {
		t.Fatalf("expected missing reference report when text ends at claim, got failed=%v msg=%q", recExact.failed, recExact.message)
	}

	recMissing := &testRecorder{TB: t}
	assertGuideClaimReference(recMissing, "other guide content without the claim", claim, ref, 250)
	if !recMissing.failed || !strings.Contains(recMissing.message, "missing historical compatibility claim") {
		t.Fatalf("expected missing claim report, got failed=%v msg=%q", recMissing.failed, recMissing.message)
	}
}
