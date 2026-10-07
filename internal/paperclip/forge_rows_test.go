// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package paperclip

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// forgeRepo returns acme/widget whose manifest declares forge ("" declares none) and whose
// origin remote is remote ("" adds none).
func forgeRepo(t *testing.T, forge, remote string) string {
	t.Helper()
	repo := t.TempDir()
	manifest := "repository:\n  owner: acme\n  name: widget\n"
	if forge != "" {
		manifest += "  forge: " + forge + "\n"
	}
	writeRepoFile(t, repo, ".standards.yaml", manifest)
	if remote != "" {
		testsupport.InitGitRepoWithOrigin(t, repo, remote)
	}
	return repo
}

// writtenRules synthesizes and writes the harness of repo and returns harness.json and rules.md.
func writtenRules(t *testing.T, repo string) (*Harness, string, string) {
	t.Helper()
	h, err := SynthesizeHarness(t.Context(), repo, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(h, repo); err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join(repo, paperclipDir, harnessFile))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := os.ReadFile(filepath.Join(repo, paperclipDir, rulesFile))
	if err != nil {
		t.Fatal(err)
	}
	return h, string(harness), string(rules)
}

// Positive: a Forgejo harness carries the AGit push in harness.json and rules.md; a GitHub one,
// declared or defaulted from a github.com remote, and a GitLab one carry only the review-branch
// push, under its own section, and no AGit row (#321).
func TestSynthesizeHarness_Positive_ForgeSelectsPushRows(t *testing.T) {
	_, harness, rules := writtenRules(t, forgeRepo(t, "forgejo", ""))
	if !strings.Contains(harness, `"agit_push_format"`) || strings.Contains(harness, `"push_format"`) ||
		!strings.Contains(rules, "## AGit Push Protocol") || !strings.Contains(rules, "refs/for/main") {
		t.Fatalf("Forgejo harness must carry the AGit push:\n%s\n%s", harness, rules)
	}
	for name, repo := range map[string]string{
		"declared github":  forgeRepo(t, "github", ""),
		"github.com":       forgeRepo(t, "", "git@github.com:acme/widget.git"),
		"declared gitlab":  forgeRepo(t, "gitlab", "https://gitlab.example.org/acme/widget.git"),
		"github over host": forgeRepo(t, "github", "https://code.example.org/acme/widget.git"),
	} {
		h, harness, rules := writtenRules(t, repo)
		if h.PushFormat != reviewBranchPush || h.AGitPushFormat != "" {
			t.Fatalf("%s: push rows %+v, want only the review-branch push", name, h)
		}
		for _, agit := range []string{"refs/for", "agit_push_format", "AGit"} {
			if strings.Contains(harness, agit) || strings.Contains(rules, agit) {
				t.Fatalf("%s: harness names %q:\n%s\n%s", name, agit, harness, rules)
			}
		}
		if !strings.Contains(rules, "## Push Protocol\n\n```bash\n"+reviewBranchPush+"\n```\n") {
			t.Fatalf("%s: rules.md lacks the push section:\n%s", name, rules)
		}
	}
}

// Negative: a repository on a host other than github.com that declares no forge is refused with
// the key named, and no harness is written; a harness setting both push members is refused.
func TestSynthesizeHarness_Negative_UndeclaredForgeRefused(t *testing.T) {
	for _, remote := range []string{"https://forgejo.example.org/acme/widget.git", ""} {
		repo := forgeRepo(t, "", remote)
		_, err := SynthesizeHarness(t.Context(), repo, unknownFacts)
		if !errors.Is(err, config.ErrForgeUndeclared) || !strings.Contains(err.Error(), config.ForgeKey) {
			t.Fatalf("remote %q: %v, want ErrForgeUndeclared naming %s", remote, err, config.ForgeKey)
		}
	}
	both := filepath.Join(t.TempDir(), "harness.json")
	writeRepoFile(t, filepath.Dir(both), "harness.json", `{"version":1,"platform":"acme/widget","operating_contract":["x"],`+
		`"agit_push_format":"a","push_format":"b","invariants":["y"]}`)
	if _, err := LoadHarness(both); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("a harness with both push members must be refused: %v", err)
	}
}

// Boundary: a GitHub harness an earlier release wrote with the AGit push is unmodified earlier
// output, refreshed by adopt, and so is one written before the repository declared another
// forge; one hand-edited push row stays operator-owned, and GitHub and GitLab share one row.
func TestPriorGenerated_Boundary_ForgeSwitchIsEarlierOutput(t *testing.T) {
	if forgePush(config.ForgeGitHub) != forgePush(config.ForgeGitLab) {
		t.Fatal("GitHub and GitLab harnesses must carry the same review-branch push")
	}
	if pairs := releasedPushRows(); len(pairs) != 2 || pairs[0].agit != agitPushFormat {
		t.Fatalf("released push rows %+v, want the AGit pair then the review-branch pair", pairs)
	}
	github := forgeRepo(t, "github", "")
	current, err := SynthesizeHarness(t.Context(), github, unknownFacts)
	if err != nil {
		t.Fatal(err)
	}
	earlier := *current
	earlier.AGitPushFormat, earlier.PushFormat = agitPushFormat, ""
	edited := *current
	edited.PushFormat = reviewBranchPush + " --force"
	for name, tc := range map[string]struct {
		harness   *Harness
		generated bool
	}{"AGit release": {&earlier, true}, "edited push": {&edited, false}} {
		if err := WriteHarness(tc.harness, github); err != nil {
			t.Fatal(err)
		}
		state, err := PriorGenerated(t.Context(), github, current)
		if err != nil || state.Generated != tc.generated {
			t.Fatalf("%s: %+v, %v; want generated %v", name, state, err, tc.generated)
		}
	}
}
