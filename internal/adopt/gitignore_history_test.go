package adopt

import (
	"slices"
	"strings"
	"testing"
)

// Positive: LookupManagedGitIgnoreTail recognizes current and earlier versions across all
// permutations of legacy scratch retirement and config directory negation, in LF and CRLF.
func TestLookupManagedGitIgnoreTail_Positive(t *testing.T) {
	root := t.TempDir()

	for name, tc := range map[string]struct {
		version     GitIgnoreBlockVersion
		wantCurrent bool
		wantVersion string
	}{
		"current v2 canonical block": {
			version:     CanonicalGitIgnoreBlockVersion,
			wantCurrent: true,
			wantVersion: "v2",
		},
		"historical v1 four-rule block": {
			version:     GitIgnoreBlockHistory[0],
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
// its previous version must be appended to GitIgnoreBlockHistory.
func TestGitIgnoreBlockHistoryGuard(t *testing.T) {
	assertHistoryInvariants(t)

	v1 := GitIgnoreBlockHistory[0]
	if err := CheckBlockHistoryTransition(v1, CanonicalGitIgnoreBlockVersion, GitIgnoreBlockHistory); err != nil {
		t.Fatalf("CheckBlockHistoryTransition failed for valid history: %v", err)
	}

	mutatedCanonical := GitIgnoreBlockVersion{
		Version: "v3",
		Rules:   append(slices.Clone(CanonicalGitIgnoreBlockVersion.Rules), "/.new-tool-cache/"),
	}
	err := CheckBlockHistoryTransition(CanonicalGitIgnoreBlockVersion, mutatedCanonical, GitIgnoreBlockHistory)
	if err == nil {
		t.Fatal("CheckBlockHistoryTransition passed when canonical block changed without appending previous to history")
	}
	if !strings.Contains(err.Error(), "without appending it to history") {
		t.Fatalf("unexpected error message: %v", err)
	}

	updatedHistory := append(slices.Clone(GitIgnoreBlockHistory), CanonicalGitIgnoreBlockVersion)
	if err := CheckBlockHistoryTransition(CanonicalGitIgnoreBlockVersion, mutatedCanonical, updatedHistory); err != nil {
		t.Fatalf("CheckBlockHistoryTransition failed with updated history: %v", err)
	}
}

func assertHistoryInvariants(t *testing.T) {
	t.Helper()
	if len(GitIgnoreBlockHistory) == 0 {
		t.Fatal("GitIgnoreBlockHistory must not be empty")
	}
	if len(GitIgnoreBlockHistory) > maxHistoryVersions {
		t.Fatalf("GitIgnoreBlockHistory length %d exceeds bound %d", len(GitIgnoreBlockHistory), maxHistoryVersions)
	}

	seen := make(map[string]bool, len(GitIgnoreBlockHistory)+1)
	seen[CanonicalGitIgnoreBlockVersion.Version] = true
	for _, v := range GitIgnoreBlockHistory {
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
		if slices.Equal(v.Rules, CanonicalGitIgnoreBlockVersion.Rules) {
			t.Fatalf("historical version %s has identical rules to canonical", v.Version)
		}
	}
}
