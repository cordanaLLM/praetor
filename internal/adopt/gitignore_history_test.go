package adopt

import (
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
}

// Positive: LookupManagedGitIgnoreTail recognizes current and earlier versions across all
// permutations of legacy scratch retirement and config directory negation, in LF and CRLF.
func TestLookupManagedGitIgnoreTail_Positive(t *testing.T) {
	root := t.TempDir()

	for name, tc := range map[string]struct {
		version     gitIgnoreBlockVersion
		wantCurrent bool
		wantVersion string
	}{
		"current v2 canonical block": {
			version:     gitIgnoreBlockVersion{Version: "v2", Rules: pinnedGitIgnoreBlockVersions["v2"]},
			wantCurrent: true,
			wantVersion: "v2",
		},
		"historical v1 four-rule block": {
			version:     gitIgnoreBlockVersion{Version: "v1", Rules: pinnedGitIgnoreBlockVersions["v1"]},
			wantCurrent: false,
			wantVersion: "v1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, keepLegacy := range []bool{true, false} {
				for _, negateConfig := range []bool{true, false} {
					block := tc.version.Render(keepLegacy, negateConfig)
					text := "dist/\n\n" + block

					match, ok := LookupManagedGitIgnoreTail(root, text)
					if !ok {
						t.Fatalf("keepLegacy=%v negateConfig=%v: block not recognized:\n%s", keepLegacy, negateConfig, text)
					}
					if match.Current != tc.wantCurrent {
						t.Fatalf("keepLegacy=%v negateConfig=%v: got Current=%v, want %v", keepLegacy, negateConfig, match.Current, tc.wantCurrent)
					}
					if match.Version != tc.wantVersion {
						t.Fatalf("keepLegacy=%v negateConfig=%v: got Version=%q, want %q", keepLegacy, negateConfig, match.Version, tc.wantVersion)
					}

					if !HasManagedGitIgnoreTail(root, text) {
						t.Fatalf("HasManagedGitIgnoreTail returned false for valid block:\n%s", text)
					}

					crlfText := strings.ReplaceAll(text, "\n", "\r\n")
					crlfMatch, crlfOk := LookupManagedGitIgnoreTail(root, crlfText)
					if !crlfOk || crlfMatch.Version != tc.wantVersion {
						t.Fatalf("CRLF block lookup failed: ok=%v, match=%+v", crlfOk, crlfMatch)
					}
				}
			}
		})
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
			match, ok := LookupManagedGitIgnoreTail(root, text)
			if ok || match.Matched {
				t.Fatalf("invalid block was accepted: %+v", match)
			}
			if HasManagedGitIgnoreTail(root, text) {
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
			match, ok := LookupManagedGitIgnoreTail(root, text)
			if ok || match.Matched {
				t.Fatalf("boundary text %s matched: %+v", name, match)
			}
			if HasManagedGitIgnoreTail(root, text) {
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
		Version: "v3",
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
