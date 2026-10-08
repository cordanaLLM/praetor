// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// declareMergeQueue adds overrides.branch_protection.merge_queue: true to the manifest of repo.
func declareMergeQueue(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, config.ManifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "\noverrides:") {
		t.Fatalf("the fixture manifest already declares overrides; extend this helper:\n%s", text)
	}
	mustWrite(t, path, text+"\noverrides:\n  branch_protection:\n    merge_queue: true\n")
}

// Migration path: a repository adopted without a queue declares merge_queue and adopts again.
// The ruleset on disk is the unedited queue-less rendering, so adoption refreshes it without
// --force instead of keeping it as drift (Positive). The workflows that lack merge_group are
// reported, because their checks leave the required set (Positive). A queue-less ruleset with a
// value edited is still kept (Negative).
func TestAdopt_Positive_DeclaringAMergeQueueRefreshesTheUneditedRuleset(t *testing.T) {
	repo := newTestRepo(t, "declare-queue")
	mustWrite(t, filepath.Join(repo, "meson.build"), "project('x', 'c')\n")
	mustWrite(t, filepath.Join(repo, ".github", "workflows", "lint.yml"),
		"name: Lint\non:\n  pull_request:\njobs:\n  lint:\n    name: Lint Check\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n")
	adoptForRuleset(t, repo, false)
	before, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil {
		t.Fatal(err)
	}
	declareMergeQueue(t, repo)

	rep := adoptForRuleset(t, repo, false)
	if warnings := rulesetWarnings(rep); len(warnings) != 0 {
		t.Fatalf("an unedited queue-less ruleset was kept as drift: %v", warnings)
	}
	after, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil {
		t.Fatal(err)
	}
	requireQueueRefresh(t, before, after)
	omitted := 0
	for _, w := range rep.Warnings {
		if strings.Contains(w, "not required by the merge queue ruleset") && strings.Contains(w, "merge_group:") {
			omitted++
		}
	}
	if omitted == 0 {
		t.Fatalf("workflows without merge_group must be reported by the run: %v", rep.Warnings)
	}
}

// requireQueueRefresh requires the refreshed main ruleset to carry no merge_queue rule (it lives in
// its own ruleset) and to drop the check of a workflow without merge_group that it required before.
func requireQueueRefresh(t *testing.T, before, after []byte) {
	t.Helper()
	if has, err := forge.RulesetHasRule(after, "merge_queue"); err != nil || has {
		t.Fatalf("the committed main ruleset must carry no merge_queue rule: %v %v\n%s", has, err, after)
	}
	if was, err := forge.RulesetRequiresStatusContext(before, "Lint Check"); err != nil || !was {
		t.Fatalf("the queue-less ruleset must require Lint Check: %v %v", was, err)
	}
	if required, err := forge.RulesetRequiresStatusContext(after, "Lint Check"); err != nil || required {
		t.Fatalf("the refreshed ruleset must drop the check of a workflow without merge_group: %v %v\n%s", required, err, after)
	}
}

func TestAdopt_Negative_EditedQueuelessRulesetIsKeptWhenAQueueIsDeclared(t *testing.T) {
	repo := newTestRepo(t, "declare-queue-edited")
	mustWrite(t, filepath.Join(repo, "meson.build"), "project('x', 'c')\n")
	adoptForRuleset(t, repo, false)
	path := filepath.Join(repo, rulesetFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"required_review_thread_resolution": true`, `"required_review_thread_resolution": false`, 1)
	if edited == string(data) {
		t.Fatal("the fixture ruleset has no review thread resolution to edit")
	}
	mustWrite(t, path, edited)
	declareMergeQueue(t, repo)
	rep := adoptForRuleset(t, repo, false)
	if len(rulesetWarnings(rep)) != 1 {
		t.Fatalf("an edited queue-less ruleset must be kept with one warning: %v", rep.Warnings)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != edited {
		t.Fatalf("the edited ruleset was changed without --force: %v", err)
	}
}
