// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// lintPriorFixtures are the managed assets rewritten to pass the linters adopters run over
// every tracked file (internal/managedasset/lint_clean_test.go, #842, #845, #578). Each fixture
// is the text Praetor shipped before, so an adopter's unedited copy must refresh without
// --force.
var lintPriorFixtures = []struct{ family, rel, fixture string }{
	{"API compatibility", "tools/apicompat/gate/main.go", "api-gate-main.go.txt"},
	{"Figure engine", "tools/figures/mkdocs_hook.py", "figures-mkdocs_hook.py.txt"},
	{"Figure engine", "tools/figures/third_party/interfig/VENDOR.md", "figures-VENDOR.md.txt"},
	{"Figure engine", "tools/figures/README.md", "figures-README.md.txt"},
}

func lintPriorFamily(t *testing.T, name string) managedasset.Family {
	t.Helper()
	families := managedasset.Families()
	index := slices.IndexFunc(families, func(f managedasset.Family) bool { return f.Name == name })
	if index < 0 {
		t.Fatalf("no managed asset family %q", name)
	}
	return families[index]
}

func lintPriorText(t *testing.T, fixture string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "managedasset", "testdata", "prior", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: an unedited copy of the earlier text refreshes to the lint-clean text without
// --force, in its LF form and in its CRLF checkout, which keeps its style.
func TestLintCleanAssetPriorsRefreshWithoutForce(t *testing.T) {
	for _, tc := range lintPriorFixtures {
		family := lintPriorFamily(t, tc.family)
		current, owned, err := family.Canonical(tc.rel)
		if err != nil || !owned {
			t.Fatalf("%s: canonical text: owned=%v err=%v", tc.rel, owned, err)
		}
		old := lintPriorText(t, tc.fixture)
		if old == string(current) {
			t.Fatalf("%s: the fixture is the current text, so no refresh is exercised", tc.rel)
		}
		for style, eol := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
			s := familySession(t, false)
			mustWrite(t, filepath.Join(s.repoPath, filepath.FromSlash(tc.rel)), strings.ReplaceAll(old, "\n", eol))
			if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
				t.Fatalf("%s (%s): %v", tc.rel, style, err)
			}
			want := strings.ReplaceAll(string(current), "\n", eol)
			if got, _ := familyFile(t, s, tc.rel); got != want {
				t.Fatalf("%s (%s) was not refreshed to the current text", tc.rel, style)
			}
			if action, _ := actionOf(s.report, tc.rel); action.Details != refreshedPriorDetail {
				t.Fatalf("%s (%s) action = %+v", tc.rel, style, action)
			}
		}
	}
}

// Negative: a hand-edited copy of the earlier text is not Praetor's output and is left alone
// without --force.
func TestLintCleanAssetPriorsKeepEditedCopies(t *testing.T) {
	for _, tc := range lintPriorFixtures {
		family := lintPriorFamily(t, tc.family)
		edited := lintPriorText(t, tc.fixture) + "\nan operator edit\n"
		s := familySession(t, false)
		mustWrite(t, filepath.Join(s.repoPath, filepath.FromSlash(tc.rel)), edited)
		if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
			t.Logf("%s: an edited copy is refused with: %v", tc.rel, err)
		}
		if got, _ := familyFile(t, s, tc.rel); got != edited {
			t.Fatalf("%s: a hand-edited copy was overwritten without --force", tc.rel)
		}
	}
}

// Boundary: the fixtures are exactly the earlier texts the families record, so a recorded
// prior with no fixture fails here instead of going untested.
func TestLintCleanAssetPriorFixturesAreRecorded(t *testing.T) {
	for _, tc := range lintPriorFixtures {
		family := lintPriorFamily(t, tc.family)
		if !family.PriorText(tc.rel, []byte(lintPriorText(t, tc.fixture))) {
			t.Errorf("%s: the fixture %s is not a recorded prior text of %s", tc.rel, tc.fixture, tc.family)
		}
	}
}
