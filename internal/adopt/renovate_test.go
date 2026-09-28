// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientjson"
)

// renovateManagedFixturePaths are the documentation family's managed paths, spelled out
// rather than read from the code under test.
var renovateManagedFixturePaths = []string{
	".github/workflows/praetor-docs.yml",
	"tools/markdownlint/package.json",
	"tools/markdownlint/package-lock.json",
	"tools/markdownlint/markdownlint-cli2.yaml",
	"tools/markdownlint/verify.mjs",
	"tools/markdownlint/no-private-scratch-links.mjs",
}

// adopterRenovateConfig is an adopter's own configuration: a preset, an ignorePaths list and
// a rule of its own, in an order and with inline arrays a rewrite must keep in value.
const adopterRenovateConfig = `{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": ["config:recommended"],
  "ignorePaths": ["**/fixtures/**"],
  "packageRules": [
    {"matchManagers": ["github-actions"], "pinDigests": true}
  ],
  "prConcurrentLimit": 1.50e1
}
`

// renovateTestRules decodes the packageRules of a Renovate configuration.
func renovateTestRules(t *testing.T, data string) (clientjson.Object, []renovateRule, []jsontext.Value) {
	t.Helper()
	root, err := clientjson.DecodeObject([]byte(data))
	if err != nil {
		t.Fatalf("configuration is not a JSON object: %v\n%s", err, data)
	}
	raw, _ := root.Get("packageRules")
	var values []jsontext.Value
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("packageRules: %v\n%s", err, data)
	}
	var managed []renovateRule
	for _, value := range values {
		var rule renovateRule
		if json.Unmarshal(value, &rule) == nil && rule.Description == renovateRuleDescription {
			managed = append(managed, rule)
		}
	}
	return root, managed, values
}

func memberNames(root clientjson.Object) []string {
	names := make([]string, 0, len(root))
	for _, member := range root {
		names = append(names, member.Name)
	}
	return names
}

// Positive: adoption adds one packageRules entry disabling Renovate for exactly the managed
// family files and keeps every setting of the adopter's own, in order, the number literal
// included; the rerun, and a rerun after a formatter compacted the file, change nothing.
func TestRenovateIgnorePositiveDeclaresManagedFilesOnce(t *testing.T) {
	repo := newTestRepo(t, "renovate-positive")
	path := filepath.Join(repo, "renovate.json")
	mustWrite(t, path, adopterRenovateConfig)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	first := mustRead(t, path)
	root, managed, rules := renovateTestRules(t, first)
	if len(managed) != 1 || managed[0].Enabled || !slices.Equal(managed[0].MatchFileNames, renovateManagedFixturePaths) {
		t.Fatalf("want one disabled rule over the managed files, got %+v\n%s", managed, first)
	}
	if want := []string{"$schema", "extends", "ignorePaths", "packageRules", "prConcurrentLimit"}; !slices.Equal(memberNames(root), want) {
		t.Fatalf("members %v, want %v", memberNames(root), want)
	}
	if limit, _ := root.Get("prConcurrentLimit"); string(limit) != "1.50e1" || len(rules) != 2 ||
		!sameJSON(rules[0], jsontext.Value(`{"matchManagers":["github-actions"],"pinDigests":true}`)) {
		t.Fatalf("adopter settings changed:\n%s", first)
	}
	if detail := findActionDetail(report.ActionDetails, "renovate.json"); !strings.Contains(detail, "Declared the Praetor-managed files") {
		t.Fatalf("renovate.json action = %q", detail)
	}
	report, err = Adopt(t.Context(), opts)
	if err != nil || mustRead(t, path) != first ||
		!strings.Contains(findActionDetail(report.ActionDetails, "renovate.json"), "already leaves") {
		t.Fatalf("rerun is not a no-op: %v\n%s", err, mustRead(t, path))
	}
	compact := jsontext.Value(first)
	if err := compact.Compact(); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, string(compact))
	if _, err := Adopt(t.Context(), opts); err != nil || mustRead(t, path) != string(compact) {
		t.Fatalf("a reformatted file with the rule was rewritten: %v\n%s", err, mustRead(t, path))
	}
}

// Negative: a repository without a Renovate configuration gets none, and the report says so.
func TestRenovateIgnoreNegativeCreatesNoConfiguration(t *testing.T) {
	repo := newTestRepo(t, "renovate-none")
	mustWrite(t, filepath.Join(repo, packageJSONFile), `{"name": "widgets"}`+"\n")
	report, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	for _, rel := range renovateConfigFiles {
		if _, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("adoption created %s: %v", rel, err)
		}
	}
	if got := mustRead(t, filepath.Join(repo, packageJSONFile)); got != `{"name": "widgets"}`+"\n" {
		t.Fatalf("package.json changed:\n%s", got)
	}
	if detail := findActionDetail(report.ActionDetails, renovateReportPath); !strings.Contains(detail, "none created") {
		t.Fatalf("report does not say no configuration was created: %q", detail)
	}
}

// Boundary: a configuration adoption cannot edit safely (JSON5 by name, JSONC comments in a
// .json file, the deprecated package.json member) is reported with the rule to add by hand
// and left byte for byte, on the rerun too; with two files, only the one Renovate reads first
// is edited.
func TestRenovateIgnoreBoundaryReportsUneditableConfiguration(t *testing.T) {
	for name, tc := range map[string]struct{ rel, content string }{
		"json5":        {"renovate.json5", "{\n  // adopter comment\n  extends: ['config:recommended'],\n}\n"},
		"jsonc":        {".github/renovate.json", "{\n  // adopter comment\n  \"extends\": [\"config:recommended\"]\n}\n"},
		"package.json": {packageJSONFile, "{\"name\": \"widgets\", \"renovate\": {\"extends\": [\"config:recommended\"]}}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "renovate-boundary")
			path := filepath.Join(repo, filepath.FromSlash(tc.rel))
			mustWrite(t, path, tc.content)
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
			for run := 0; run < 2; run++ {
				report, err := Adopt(t.Context(), opts)
				if err != nil {
					t.Fatalf("adopt %d: %v", run, err)
				}
				if got := mustRead(t, path); got != tc.content {
					t.Fatalf("adopt %d edited %s:\n%s", run, tc.rel, got)
				}
				warnings := strings.Join(report.Warnings, "\n")
				if !strings.Contains(warnings, tc.rel+": Renovate configuration left untouched") ||
					!strings.Contains(warnings, `"matchFileNames":[".github/workflows/praetor-docs.yml"`) {
					t.Fatalf("adopt %d does not report the rule to add by hand:\n%s", run, warnings)
				}
			}
		})
	}
	repo := newTestRepo(t, "renovate-precedence")
	mustWrite(t, filepath.Join(repo, "renovate.json"), "{}\n")
	mustWrite(t, filepath.Join(repo, ".github", "renovate.json"), "{}\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, managed, _ := renovateTestRules(t, mustRead(t, filepath.Join(repo, "renovate.json"))); len(managed) != 1 ||
		mustRead(t, filepath.Join(repo, ".github", "renovate.json")) != "{}\n" {
		t.Fatal("adoption did not edit exactly the configuration Renovate reads first")
	}
}

// uninspectableRenovateReport runs adoption on repo and checks that it completes, runs the
// steps after renovate-ignore, and reports rel as left untouched with the entry to add by hand.
func uninspectableRenovateReport(t *testing.T, repo, rel string) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, readmeFile), "# Demo\n")
	report, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo})
	if err != nil {
		t.Fatalf("an uninspectable %s failed adoption: %v", rel, err)
	}
	warnings := strings.Join(report.Warnings, "\n")
	if !strings.Contains(warnings, rel+": Renovate configuration left untouched: it cannot be read safely") ||
		!strings.Contains(warnings, `"matchFileNames":[".github/workflows/praetor-docs.yml"`) {
		t.Fatalf("%s not reported with the entry to add by hand:\n%s", rel, warnings)
	}
	if !strings.Contains(mustRead(t, filepath.Join(repo, readmeFile)), testReadmeGovernanceStart) {
		t.Fatal("the steps after renovate-ignore did not run: README has no governance block")
	}
}

// Boundary (review of #497): a Renovate configuration that exists but cannot be read under
// the adoption read contract, a symbolic link or a directory, is reported with the entry to
// add by hand and left untouched, and adoption completes; the search stops there, so a later
// configuration Renovate does not read is not edited either.
func TestRenovateIgnoreBoundaryReportsUninspectableConfiguration(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		repo := newTestRepo(t, "renovate-symlink")
		shared := filepath.Join(repo, "config", "renovate-shared.json")
		mustWrite(t, shared, adopterRenovateConfig)
		if err := os.Symlink(filepath.Join("config", "renovate-shared.json"), filepath.Join(repo, "renovate.json")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		mustWrite(t, filepath.Join(repo, ".github", "renovate.json"), "{}\n")
		uninspectableRenovateReport(t, repo, "renovate.json")
		if info, err := os.Lstat(filepath.Join(repo, "renovate.json")); err != nil || info.Mode()&os.ModeSymlink == 0 ||
			mustRead(t, shared) != adopterRenovateConfig || mustRead(t, filepath.Join(repo, ".github", "renovate.json")) != "{}\n" {
			t.Fatalf("an uninspectable configuration or a later one was changed: %v", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		repo := newTestRepo(t, "renovate-directory")
		if err := os.MkdirAll(filepath.Join(repo, "renovate.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		uninspectableRenovateReport(t, repo, "renovate.json")
	})
}

// Negative (review of #497): an unreadable configuration is reported, not fatal. Windows file
// modes do not deny reads, and root ignores them; the directory case above covers the
// non-regular path there.
func TestRenovateIgnoreNegativeReportsUnreadableConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny this process reads here")
	}
	repo := newTestRepo(t, "renovate-unreadable")
	path := filepath.Join(repo, ".renovaterc.json")
	mustWrite(t, path, adopterRenovateConfig)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreMode(t, path) })
	uninspectableRenovateReport(t, repo, ".renovaterc.json")
	restoreMode(t, path)
	if got := mustRead(t, path); got != adopterRenovateConfig {
		t.Fatalf("unreadable configuration changed:\n%s", got)
	}
}

// Negative: a cancelled adoption context is the one observation failure the search returns,
// since nothing was observed; it is never reported as an uninspectable configuration.
func TestFindRenovateConfigNegativePropagatesCancellation(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "renovate.json"), "{}\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := recordingSession(repo, repoIdentity{})
	if _, _, found, err := findRenovateConfig(ctx, s, renovateManagedFixturePaths); found || !errors.Is(err, context.Canceled) {
		t.Fatalf("found=%v err=%v, want context.Canceled", found, err)
	}
	if len(s.report.Warnings) != 0 {
		t.Fatalf("cancellation reported as a configuration finding: %v", s.report.Warnings)
	}
}

// Boundary: in the repository that holds a family's Source the managed files are sources its
// own Renovate updates, so adoption adds no rule there and removes one it added before.
func TestRenovateIgnoreBoundarySkipsTheFamilyOrigin(t *testing.T) {
	repo := newTestRepo(t, "renovate-origin")
	path := filepath.Join(repo, "renovate.json")
	withRule, changed, err := mergeRenovateRule(t.Context(), []byte("{\"extends\": [\"config:recommended\"]}\n"), renovateManagedFixturePaths)
	if err != nil || !changed {
		t.Fatalf("seed rule: changed=%v err=%v", changed, err)
	}
	mustWrite(t, path, string(withRule))
	mustWrite(t, filepath.Join(repo, "tools", "markdownlint", "assets.go"), "package markdownlint\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got := mustRead(t, path)
	if root, managed, rules := renovateTestRules(t, got); len(managed) != 0 || len(rules) != 0 || len(root) != 2 {
		t.Fatalf("family origin kept the managed rule or lost a setting:\n%s", got)
	}
	if detail := findActionDetail(report.ActionDetails, "renovate.json"); !strings.Contains(detail, "Removed the Praetor-managed packageRules entry") {
		t.Fatalf("removal not reported: %q", detail)
	}
	report, err = Adopt(t.Context(), opts)
	if err != nil || mustRead(t, path) != got ||
		!strings.Contains(findActionDetail(report.ActionDetails, "renovate.json"), "No Praetor-managed file needs a Renovate rule") {
		t.Fatalf("rerun at the family origin changed renovate.json or misreported it: %v", err)
	}
}

// mergeRenovateRule in isolation. Positive: a stale managed entry is replaced where it
// stands, and an empty path set removes it and nothing else. Boundary: CRLF text stays CRLF,
// no managed entry with no paths is no change, and an append that reaches maxRenovateRules is
// a no-op on the rerun and removable. Negative: documents whose rewrite could lose or misread
// the adopter's settings, or an append past the bound, are refused with *renovateUnsafeError.
func TestMergeRenovateRule(t *testing.T) {
	ctx := t.Context()
	own := `{"groupName":"adopter"}`
	stale := `{"description":"` + renovateRuleDescription + `","matchFileNames":["old.yml"],"enabled":true}`
	data := []byte(`{"packageRules":[` + stale + `,` + own + `]}`)
	out, changed, err := mergeRenovateRule(ctx, data, []string{"new.yml"})
	if _, managed, rules := renovateTestRules(t, string(out)); err != nil || !changed || len(rules) != 2 ||
		len(managed) != 1 || managed[0].Enabled || !slices.Equal(managed[0].MatchFileNames, []string{"new.yml"}) ||
		!sameJSON(rules[1], jsontext.Value(own)) {
		t.Fatalf("stale entry not replaced in place: changed=%v err=%v\n%s", changed, err, out)
	}
	out, changed, err = mergeRenovateRule(ctx, data, nil)
	if _, managed, rules := renovateTestRules(t, string(out)); err != nil || !changed || len(managed) != 0 || len(rules) != 1 {
		t.Fatalf("empty path set did not remove only the managed entry: changed=%v err=%v\n%s", changed, err, out)
	}
	if _, changed, err := mergeRenovateRule(ctx, []byte(`{"packageRules":[`+own+`]}`), nil); err != nil || changed {
		t.Fatalf("no entry and no paths changed the file: changed=%v err=%v", changed, err)
	}
	out, _, err = mergeRenovateRule(ctx, []byte("{\r\n  \"extends\": []\r\n}\r\n"), []string{"new.yml"})
	if err != nil || strings.Count(string(out), "\r\n") != strings.Count(string(out), "\n") {
		t.Fatalf("CRLF configuration came back with other endings: %v\n%q", err, out)
	}
	duplicate := `{"packageRules":[` + stale + `,` + stale + `]}`
	for name, input := range map[string]string{
		"mixed endings":        "{\r\n  \"extends\": []\n}\n",
		"trailing comma":       `{"extends": [],}`,
		"comment":              "{\n  // note\n}\n",
		"array document":       `[]`,
		"packageRules object":  `{"packageRules": {}}`,
		"duplicate managed":    duplicate,
		"duplicate member":     `{"extends": [], "extends": []}`,
		"past the size bound":  `{"x": "` + strings.Repeat("a", clientjson.MaxBytes) + `"}`,
		"past the rules bound": `{"packageRules": [` + strings.TrimSuffix(strings.Repeat("{},", maxRenovateRules+1), ",") + `]}`,
	} {
		var unsafe *renovateUnsafeError
		if _, _, err := mergeRenovateRule(ctx, []byte(input), []string{"new.yml"}); !errors.As(err, &unsafe) {
			t.Fatalf("%s: %v, want *renovateUnsafeError", name, err)
		}
	}
	belowBound := `{"packageRules": [` + strings.TrimSuffix(strings.Repeat("{},", maxRenovateRules-1), ",") + `]}`
	atBound, changed, err := mergeRenovateRule(ctx, []byte(belowBound), []string{"new.yml"})
	if err != nil || !changed {
		t.Fatalf("configuration reaching the rules bound refused: changed=%v err=%v", changed, err)
	}
	if _, managed, rules := renovateTestRules(t, string(atBound)); len(rules) != maxRenovateRules || len(managed) != 1 {
		t.Fatalf("append at the bound wrote %d entries, %d managed", len(rules), len(managed))
	}
	if _, changed, err := mergeRenovateRule(ctx, atBound, []string{"new.yml"}); err != nil || changed {
		t.Fatalf("rerun on a configuration at the bound: changed=%v err=%v, want a no-op", changed, err)
	}
	if _, changed, err := mergeRenovateRule(ctx, atBound, nil); err != nil || !changed {
		t.Fatalf("managed entry at the bound not removable: changed=%v err=%v", changed, err)
	}
	full := `{"packageRules": [` + strings.TrimSuffix(strings.Repeat("{},", maxRenovateRules), ",") + `]}`
	var unsafe *renovateUnsafeError
	if _, _, err := mergeRenovateRule(ctx, []byte(full), []string{"new.yml"}); !errors.As(err, &unsafe) {
		t.Fatalf("append past the rules bound: %v, want *renovateUnsafeError", err)
	}
}
