// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// apiCompatibilityFixturePaths are the managed paths of the api:public-contract family.
var apiCompatibilityFixturePaths = []string{".github/workflows/praetor-api.yml", "tools/apicompat/gate/main.go", "tools/apicompat/gate/placeholder.go"}

// documentationFixturePaths are the managed paths of the docs:seo-portal families.
var documentationFixturePaths = []string{
	".github/workflows/praetor-docs.yml",
	"tools/markdownlint/package.json",
	"tools/markdownlint/package-lock.json",
	"tools/markdownlint/markdownlint-cli2.yaml",
	"tools/markdownlint/verify.mjs",
	"tools/markdownlint/no-private-scratch-links.mjs",
	"tools/figures/core.mjs",
	"tools/figures/checks.mjs",
	"tools/figures/build.mjs",
	"tools/figures/types.ts",
	"tools/figures/third_party/interfig/vendor.json",
	"tools/figures/third_party/interfig/VENDOR.md",
	"tools/figures/third_party/interfig/upstream/LICENSE",
	"tools/figures/third_party/interfig/upstream/src/svg.ts",
	"tools/figures/third_party/interfig/upstream/src/geometry.ts",
	"tools/figures/third_party/interfig/upstream/src/model.ts",
	"tools/figures/dist/loader.js",
	"tools/figures/dist/player.js",
	"tools/figures/dist/THIRD-PARTY-LICENSES.txt",
	"tools/figures/figures.css",
	"tools/figures/mkdocs_hook.py",
	"tools/figures/astro.mjs",
	"tools/figures/serve.mjs",
	"tools/figures/README.md",
}

// renovateManagedFixturePaths are the managed paths of the families the default facets
// enable in a repository whose go.mod git tracks, the documentation families' and then the API
// compatibility family's (apiCompatibilityFixturePaths), spelled out rather than read from the
// code under test.
var renovateManagedFixturePaths = append(slices.Clone(documentationFixturePaths), apiCompatibilityFixturePaths...)

// devContainerBundleFixturePaths are the generated DevContainer bundle files Renovate's
// managers read, which the managed entry lists after the family paths when the bundle is
// Praetor's (#323).
var devContainerBundleFixturePaths = []string{".devcontainer/devcontainer.json", ".devcontainer/Dockerfile.praetor"}

// withBundleFixturePaths returns paths followed by the bundle files, as adoption declares them
// in a repository whose DevContainer it generates.
func withBundleFixturePaths(paths []string) []string {
	return append(slices.Clone(paths), devContainerBundleFixturePaths...)
}

// ownDevContainer is an operator's own DevContainer, which adoption preserves and whose
// updates stay with the operator's Renovate.
const ownDevContainer = "{\n  \"image\": \"ghcr.io/acme/dev:1\"\n}\n"

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
// family files and the DevContainer bundle files it generated, and keeps every setting of the
// adopter's own, in order, the number literal included; the rerun, and a rerun after a
// formatter compacted the file, change nothing.
func TestRenovateIgnorePositiveDeclaresManagedFilesOnce(t *testing.T) {
	repo := newTestRepo(t, "renovate-positive")
	trackGoModule(t, repo, "go.mod")
	path := filepath.Join(repo, "renovate.json")
	mustWrite(t, path, adopterRenovateConfig)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	first := mustRead(t, path)
	root, managed, rules := renovateTestRules(t, first)
	if len(managed) != 1 || managed[0].Enabled || !slices.Equal(managed[0].MatchFileNames, withBundleFixturePaths(renovateManagedFixturePaths)) {
		t.Fatalf("want one disabled rule over the managed files and the generated bundle, got %+v\n%s", managed, first)
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
// own Renovate updates, so adoption adds no rule for them there: the origin of one family keeps
// the rule over the other's files, and the bundle adoption generated, only, and the origin of
// all, with a DevContainer of its own, removes a rule it added before.
func TestRenovateIgnoreBoundarySkipsTheFamilyOrigin(t *testing.T) {
	markdownOrigin := newTestRepo(t, "renovate-markdown-origin")
	trackGoModule(t, markdownOrigin, "go.mod")
	mustWrite(t, filepath.Join(markdownOrigin, "tools", "markdownlint", "assets.go"), "package markdownlint\n")
	mustWrite(t, filepath.Join(markdownOrigin, "renovate.json"), "{\"extends\": [\"config:recommended\"]}\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: markdownOrigin}); err != nil {
		t.Fatalf("adopt at the Markdown origin: %v", err)
	}
	if _, managed, _ := renovateTestRules(t, mustRead(t, filepath.Join(markdownOrigin, "renovate.json"))); len(managed) != 1 ||
		!slices.Equal(managed[0].MatchFileNames, withBundleFixturePaths(renovateManagedFixturePaths[6:])) {
		t.Fatalf("the Markdown origin's rule = %+v, want the figure engine's, the API gate's and the bundle's files only", managed)
	}
	repo := newTestRepo(t, "renovate-origin")
	path := filepath.Join(repo, "renovate.json")
	withRule, _, changed, err := mergeRenovateRule(t.Context(), []byte("{\"extends\": [\"config:recommended\"]}\n"), renovateManagedFixturePaths)
	if err != nil || !changed {
		t.Fatalf("seed rule: changed=%v err=%v", changed, err)
	}
	mustWrite(t, path, string(withRule))
	mustWrite(t, filepath.Join(repo, "tools", "markdownlint", "assets.go"), "package markdownlint\n")
	mustWrite(t, filepath.Join(repo, "tools", "figures", "assets.go"), "package figures\n")
	mustWrite(t, filepath.Join(repo, "tools", "apicompat", "assets.go"), "package apicompat\n")
	mustWrite(t, filepath.Join(repo, filepath.FromSlash(devcontainerFile)), ownDevContainer)
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
	out, _, changed, err := mergeRenovateRule(ctx, data, []string{"new.yml"})
	if _, managed, rules := renovateTestRules(t, string(out)); err != nil || !changed || len(rules) != 2 ||
		len(managed) != 1 || managed[0].Enabled || !slices.Equal(managed[0].MatchFileNames, []string{"new.yml"}) ||
		!sameJSON(rules[1], jsontext.Value(own)) {
		t.Fatalf("stale entry not replaced in place: changed=%v err=%v\n%s", changed, err, out)
	}
	out, _, changed, err = mergeRenovateRule(ctx, data, nil)
	if _, managed, rules := renovateTestRules(t, string(out)); err != nil || !changed || len(managed) != 0 || len(rules) != 1 {
		t.Fatalf("empty path set did not remove only the managed entry: changed=%v err=%v\n%s", changed, err, out)
	}
	if _, _, changed, err := mergeRenovateRule(ctx, []byte(`{"packageRules":[`+own+`]}`), nil); err != nil || changed {
		t.Fatalf("no entry and no paths changed the file: changed=%v err=%v", changed, err)
	}
	out, _, _, err = mergeRenovateRule(ctx, []byte("{\r\n  \"extends\": []\r\n}\r\n"), []string{"new.yml"})
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
		if _, _, _, err := mergeRenovateRule(ctx, []byte(input), []string{"new.yml"}); !errors.As(err, &unsafe) {
			t.Fatalf("%s: %v, want *renovateUnsafeError", name, err)
		}
	}
	belowBound := `{"packageRules": [` + strings.TrimSuffix(strings.Repeat("{},", maxRenovateRules-1), ",") + `]}`
	atBound, _, changed, err := mergeRenovateRule(ctx, []byte(belowBound), []string{"new.yml"})
	if err != nil || !changed {
		t.Fatalf("configuration reaching the rules bound refused: changed=%v err=%v", changed, err)
	}
	if _, managed, rules := renovateTestRules(t, string(atBound)); len(rules) != maxRenovateRules || len(managed) != 1 {
		t.Fatalf("append at the bound wrote %d entries, %d managed", len(rules), len(managed))
	}
	if _, _, changed, err := mergeRenovateRule(ctx, atBound, []string{"new.yml"}); err != nil || changed {
		t.Fatalf("rerun on a configuration at the bound: changed=%v err=%v, want a no-op", changed, err)
	}
	if _, _, changed, err := mergeRenovateRule(ctx, atBound, nil); err != nil || !changed {
		t.Fatalf("managed entry at the bound not removable: changed=%v err=%v", changed, err)
	}
	full := `{"packageRules": [` + strings.TrimSuffix(strings.Repeat("{},", maxRenovateRules), ",") + `]}`
	var unsafe *renovateUnsafeError
	if _, _, _, err := mergeRenovateRule(ctx, []byte(full), []string{"new.yml"}); !errors.As(err, &unsafe) {
		t.Fatalf("append past the rules bound: %v, want *renovateUnsafeError", err)
	}
}

// adopterOwnRenovateRule is the rule adopterRenovateConfig holds.
const adopterOwnRenovateRule = `{"matchManagers": ["github-actions"], "pinDigests": true}`

// equivalentRenovateRule is an adopter's own rule with the managed entry's effect: globs that
// cover every managed path, and enabled false.
const equivalentRenovateRule = `{"description": "keep update bots off vendored lint tooling", "matchFileNames": [".github/workflows/praetor-*.yml", "tools/markdownlint/**", "tools/figures/**", "tools/apicompat/**", ".devcontainer/*"], "enabled": false}`

// adopterRenovateConfigWith returns adopterRenovateConfig with rules appended to its own rule.
func adopterRenovateConfigWith(rules ...string) string {
	return strings.Replace(adopterRenovateConfig, adopterOwnRenovateRule,
		strings.Join(append([]string{adopterOwnRenovateRule}, rules...), ",\n    "), 1)
}

// Positive (#502): an adopter rule that already disables Renovate for every managed path, by
// globs, satisfies the managed entry: adoption adds none and leaves the file byte for byte, on
// the rerun too. A managed entry an earlier run added beside such a rule is removed, and the
// adopter's rules keep their order and value.
func TestRenovateIgnorePositiveAcceptsAnEquivalentAdopterRule(t *testing.T) {
	repo := newTestRepo(t, "renovate-equivalent")
	path := filepath.Join(repo, "renovate.json")
	config := adopterRenovateConfigWith(equivalentRenovateRule)
	mustWrite(t, path, config)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	for run := 0; run < 2; run++ {
		report, err := Adopt(t.Context(), opts)
		if err != nil || mustRead(t, path) != config ||
			!strings.Contains(findActionDetail(report.ActionDetails, "renovate.json"), "already leaves") {
			t.Fatalf("adopt %d edited a configuration whose own rule is equivalent: %v\n%s", run, err, mustRead(t, path))
		}
	}
	managed, err := renderRenovateRule(renovateManagedFixturePaths)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, adopterRenovateConfigWith(string(managed), equivalentRenovateRule))
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got := mustRead(t, path)
	if _, own, rules := renovateTestRules(t, got); len(own) != 0 || len(rules) != 2 ||
		!sameJSON(rules[0], jsontext.Value(adopterOwnRenovateRule)) || !sameJSON(rules[1], jsontext.Value(equivalentRenovateRule)) {
		t.Fatalf("managed entry not removed, or an adopter rule changed:\n%s", got)
	}
	if detail := findActionDetail(report.ActionDetails, "renovate.json"); !strings.Contains(detail, "own rules already disable Renovate for every Praetor-managed file") {
		t.Fatalf("removal not reported: %q", detail)
	}
	if _, err := Adopt(t.Context(), opts); err != nil || mustRead(t, path) != got {
		t.Fatalf("rerun after the removal changed renovate.json: %v\n%s", err, mustRead(t, path))
	}
}

// Negative (#502): an adopter rule covering only some managed paths does not satisfy the
// managed entry. Adoption appends one listing just the paths that rule leaves out, keeps the
// adopter's rules first and unchanged, and the rerun is a no-op.
func TestRenovateIgnoreNegativeDeclaresOnlyUncoveredPaths(t *testing.T) {
	partial := `{"matchFileNames": ["tools/markdownlint/**", "tools/figures/**"], "enabled": false}`
	repo := newTestRepo(t, "renovate-partial")
	trackGoModule(t, repo, "go.mod")
	path := filepath.Join(repo, "renovate.json")
	mustWrite(t, path, adopterRenovateConfigWith(partial))
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got := mustRead(t, path)
	_, managed, rules := renovateTestRules(t, got)
	uncovered := withBundleFixturePaths(append([]string{renovateManagedFixturePaths[0]}, apiCompatibilityFixturePaths...))
	if len(managed) != 1 || managed[0].Enabled || !slices.Equal(managed[0].MatchFileNames, uncovered) {
		t.Fatalf("want one disabled entry over the uncovered workflows, gate and bundle only, got %+v\n%s", managed, got)
	}
	if len(rules) != 3 || !sameJSON(rules[0], jsontext.Value(adopterOwnRenovateRule)) || !sameJSON(rules[1], jsontext.Value(partial)) {
		t.Fatalf("adopter rules reordered or changed:\n%s", got)
	}
	if detail := findActionDetail(report.ActionDetails, "renovate.json"); !strings.Contains(detail, fmt.Sprintf("the %d of %d Praetor-managed files", len(uncovered), len(withBundleFixturePaths(renovateManagedFixturePaths)))) {
		t.Fatalf("partial declaration not reported: %q", detail)
	}
	if _, err := Adopt(t.Context(), opts); err != nil || mustRead(t, path) != got {
		t.Fatalf("rerun is not a no-op: %v\n%s", err, mustRead(t, path))
	}
}

// renovatePatternRule returns a disabling rule of count patterns, the last "**".
func renovatePatternRule(count int) string {
	return `[{"matchFileNames": [` + strings.Repeat(`"x/y", `, count-1) + `"**"], "enabled": false}]`
}

// Boundary (#502): only a rule that provably has the managed entry's effect covers a path. A
// rule matching every path with another effect, a further matcher, a pattern outside the glob
// subset or past the pattern bound, or a later rule that may re-enable the path covers nothing,
// and the managed entry never covers itself. Rules may cover together what none covers alone.
func TestUncoveredRenovatePathsBoundary(t *testing.T) {
	all := renovateManagedFixturePaths
	managed, err := renderRenovateRule(all)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		rules string
		want  []string
	}{
		"globstar covers every path":     {`[{"matchFileNames": ["**"], "enabled": false}]`, nil},
		"star is every file":             {`[{"description": "all", "matchFileNames": ["*"], "enabled": false}]`, nil},
		"rules cover together":           {`[{"matchFileNames": ["tools/markdownlint/*"], "enabled": false}, {"matchFileNames": [".github/**/praetor-docs.yml", ".github/**/praetor-api.yml"], "enabled": false}, {"matchFileNames": ["tools/figures/**", "tools/apicompat/*/*.go"], "enabled": false}]`, nil},
		"earlier re-enabling rule":       {`[{"matchPackageNames": ["left-pad"], "enabled": true}, {"matchFileNames": ["**"], "enabled": false}]`, nil},
		"patterns at the bound":          {renovatePatternRule(maxRenovatePatterns), nil},
		"enabled true":                   {`[{"matchFileNames": ["**"], "enabled": true}]`, all},
		"no enabled member":              {`[{"matchFileNames": ["**"]}]`, all},
		"enabled as a string":            {`[{"matchFileNames": ["**"], "enabled": "false"}]`, all},
		"narrowing matcher":              {`[{"matchFileNames": ["**"], "matchUpdateTypes": ["major"], "enabled": false}]`, all},
		"later re-enabling rule":         {`[{"matchFileNames": ["**"], "enabled": false}, {"matchPackageNames": ["left-pad"], "enabled": true}]`, all},
		"negation in the list":           {`[{"matchFileNames": ["**", "!tools/markdownlint/verify.mjs"], "enabled": false}]`, all},
		"regular expression":             {`[{"matchFileNames": ["/praetor/"], "enabled": false}]`, all},
		"brace expansion":                {`[{"matchFileNames": ["{.github,tools}/**"], "enabled": false}]`, all},
		"case differs":                   {`[{"matchFileNames": ["TOOLS/**", ".GITHUB/**"], "enabled": false}]`, all},
		"star stays in its segment":      {`[{"matchFileNames": ["tools/*", ".github/*"], "enabled": false}]`, all},
		"empty pattern list":             {`[{"matchFileNames": [], "enabled": false}]`, all},
		"patterns past the bound":        {renovatePatternRule(maxRenovatePatterns + 1), all},
		"managed entry":                  {`[` + string(managed) + `]`, all},
		"trailing globstar below a file": {`[{"matchFileNames": [".github/workflows/praetor-docs.yml/**", ".github/workflows/praetor-api.yml", "tools/markdownlint/**", "tools/figures/**", "tools/apicompat/**"], "enabled": false}]`, all[:1]},
	} {
		var rules []jsontext.Value
		if err := json.Unmarshal([]byte(tc.rules), &rules); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := uncoveredRenovatePaths(rules, all); !slices.Equal(got, tc.want) {
			t.Errorf("%s: uncovered %v, want %v", name, got, tc.want)
		}
	}
}

// Positive: the rendered Renovate rule that disables updates on managed files lists every
// file adoption scaffolds and refreshes: all families in the registry, the generated
// DevContainer bundle, and reuse.yml when the repository declares REUSE (HISS-19).
func TestRenovateManagedPaths_IncludesAllScaffoldedFilesAndReuseGate(t *testing.T) {
	repo := newTestRepo(t, "renovate-all-managed")
	trackGoModule(t, repo, "go.mod")
	mustWrite(t, filepath.Join(repo, supplychain.ReuseFile), "version = 1\n")
	mustWrite(t, filepath.Join(repo, "renovate.json"), adopterRenovateConfig)

	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	data := mustRead(t, filepath.Join(repo, "renovate.json"))
	_, managed, _ := renovateTestRules(t, data)
	if len(managed) != 1 {
		t.Fatalf("want 1 managed rule, got %d", len(managed))
	}

	rendered := managed[0].MatchFileNames
	want := append(withBundleFixturePaths(renovateManagedFixturePaths), reuseWorkflowFile)
	if !slices.Equal(rendered, want) {
		t.Fatalf("rendered Renovate rule mismatch:\ngot:  %v\nwant: %v", rendered, want)
	}
}

// Positive (HISS-20): the managed-asset registry's rendered path list matches the spelled-out
// fixture list in renovateManagedFixturePaths exactly in content and order.
func TestManagedAssetRegistry_MatchesSpelledOutRenovateFixturePaths(t *testing.T) {
	registryPaths := managedPathsOf(managedasset.Families())
	if !slices.Equal(registryPaths, renovateManagedFixturePaths) {
		t.Fatalf("managed-asset registry paths do not match spelled-out fixture paths:\ngot:  %v\nwant: %v", registryPaths, renovateManagedFixturePaths)
	}
}

// Negative (HISS-15): reuse.yml is omitted from the Renovate managed rule when:
// (a) the reuse-gate step is declined;
// (b) REUSE is not declared;
// (c) the file is an adopter-owned copy;
// (d) the file is an edited Praetor copy kept with a warning;
// (e) the same edited copy meets --force, which regenerates only audit-locked scaffolds,
// and the REUSE gate is not one, so the copy is kept and stays unlisted.
func TestRenovateManagedPaths_ReuseGateNegativeCases(t *testing.T) {
	edited := mustReuseWorkflow(t, forge.FallbackDefaultBranch) + "\n# custom adopter addition\n"
	adopterWorkflow := "name: Custom REUSE Gate\non: [push]\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"
	for _, tc := range []reuseUnlistedCase{
		{name: "declined step", dir: "renovate-reuse-declined", declined: true, reuse: true},
		{name: "reuse not declared", dir: "renovate-reuse-not-declared"},
		{name: "adopter owned copy", dir: "renovate-reuse-adopter-owned", reuse: true, workflow: adopterWorkflow},
		{name: "edited praetor copy kept with warning", dir: "renovate-reuse-edited-praetor", reuse: true, workflow: edited, warn: true},
		{name: "force keeps edited copy and does not list it", dir: "renovate-reuse-forced", reuse: true, workflow: edited, force: true},
	} {
		t.Run(tc.name, func(t *testing.T) { assertReuseUnlisted(t, tc) })
	}
}

// reuseUnlistedCase is one adoption that must keep reuse.yml out of the managed rule.
type reuseUnlistedCase struct {
	name, dir string
	declined  bool   // adoption.decline lists the reuse-gate step
	reuse     bool   // REUSE.toml declares REUSE
	workflow  string // existing reuse.yml text; empty means none
	force     bool
	warn      bool // adoption must warn that it kept the file
}

// seedReuseUnlisted builds the repository for tc and returns its path and the reuse.yml path.
func seedReuseUnlisted(t *testing.T, tc reuseUnlistedCase) (string, string) {
	t.Helper()
	repo := newTestRepo(t, tc.dir)
	trackGoModule(t, repo, "go.mod")
	if tc.declined {
		mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: ["+reuseGateStep+"]\n")
	}
	if tc.reuse {
		mustWrite(t, filepath.Join(repo, supplychain.ReuseFile), "version = 1\n")
	}
	mustWrite(t, filepath.Join(repo, "renovate.json"), adopterRenovateConfig)
	workflowPath := filepath.Join(repo, filepath.FromSlash(reuseWorkflowFile))
	if tc.workflow != "" {
		mustWrite(t, workflowPath, tc.workflow)
	}
	return repo, workflowPath
}

// assertReuseUnlisted adopts tc's repository and checks that reuse.yml is unlisted, that an
// existing copy is byte-identical afterwards, and that a kept edited copy warns.
func assertReuseUnlisted(t *testing.T, tc reuseUnlistedCase) {
	t.Helper()
	repo, workflowPath := seedReuseUnlisted(t, tc)
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Force: tc.force})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	_, managed, _ := renovateTestRules(t, mustRead(t, filepath.Join(repo, "renovate.json")))
	if len(managed) != 1 {
		t.Fatalf("want 1 managed rule, got %d", len(managed))
	}
	if slices.Contains(managed[0].MatchFileNames, reuseWorkflowFile) {
		t.Errorf("%s: %s must not be listed in the Renovate managed rule", tc.name, reuseWorkflowFile)
	}
	if tc.workflow != "" {
		if got := mustRead(t, workflowPath); got != tc.workflow {
			t.Fatalf("%s: existing workflow was modified: got %s, want %s", tc.name, got, tc.workflow)
		}
	}
	if tc.warn && len(rep.Warnings) == 0 {
		t.Errorf("%s: expected a warning for the kept copy, got none", tc.name)
	}
}
