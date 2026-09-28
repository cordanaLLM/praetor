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

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Audit locks every managed family file byte for byte, and Praetor ships each update to them
// itself (Family.Prior keeps an unedited earlier text refreshable). An adopter's own Renovate
// does not know that: with pinDigests it opens a pull request pinning praetor-docs.yml, its
// npm manager bumps tools/markdownlint/package.json, and audit then rejects the edited copy
// (#497). Adoption therefore tells the adopter's Renovate, when the repository has one, to
// leave those files alone, the way formatter-ignore tells Prettier.
//
// The rule is a packageRules entry, not an ignorePaths entry: ignorePaths is not mergeable,
// so a repository-level ignorePaths replaces the list presets such as config:recommended
// contribute, and adding one would re-enable updates in node_modules, test and fixture
// trees. packageRules merge with presets, and matchFileNames with enabled: false turns
// updates off for exactly the listed files. The entry is identified by its description.

const (
	// renovateRuleDescription marks the one packageRules entry adoption owns.
	renovateRuleDescription = "praetor-managed files: praetorctl adopt ships their updates and praetorctl audit locks them byte for byte"
	// renovateReportPath names the step in the report when the repository has no Renovate
	// configuration.
	renovateReportPath = "renovate.json"
	// packageJSONFile carries Renovate configuration under its "renovate" member, last in
	// Renovate's search order.
	packageJSONFile = "package.json"
	// maxRenovateRules bounds the packageRules entries one configuration may hold (HISS-02).
	maxRenovateRules = 1024
)

// renovateConfigFiles are Renovate's repository configuration files in its own search order
// (configFilePatterns in renovatebot/renovate lib/config/app-strings.ts, package.json aside).
// Renovate reads the first that exists and ignores the rest, so adoption edits only that one.
var renovateConfigFiles = []string{
	"renovate.json", "renovate.jsonc", "renovate.json5",
	".github/renovate.json", ".github/renovate.jsonc", ".github/renovate.json5",
	".gitlab/renovate.json", ".gitlab/renovate.jsonc", ".gitlab/renovate.json5",
	".renovaterc", ".renovaterc.json", ".renovaterc.jsonc", ".renovaterc.json5",
}

// renovateStrictJSONFiles are the configuration names adoption may rewrite: Renovate reads
// them as JSONC, so each is edited only when it holds strict JSON (clientjson.Validate). A
// .jsonc or .json5 file is JSON5 or JSONC by name and is reported instead.
var renovateStrictJSONFiles = map[string]bool{
	"renovate.json": true, ".github/renovate.json": true, ".gitlab/renovate.json": true,
	".renovaterc": true, ".renovaterc.json": true,
}

// renovateUnsafeError marks a configuration adoption cannot edit without risking the
// adopter's own settings. The step reports reason and leaves the file untouched.
type renovateUnsafeError struct{ reason string }

func (e *renovateUnsafeError) Error() string {
	return "cannot edit the Renovate configuration safely: " + e.reason
}

func renovateUnsafe(format string, args ...any) error {
	return &renovateUnsafeError{reason: fmt.Sprintf(format, args...)}
}

// renovateRule is the packageRules entry adoption owns.
type renovateRule struct {
	Description    string   `json:"description"`
	MatchFileNames []string `json:"matchFileNames"`
	Enabled        bool     `json:"enabled"`
}

// reconcileRenovateIgnore keeps the managed family files out of the adopter's Renovate
// updates. It never creates a Renovate configuration: a repository without one is left
// alone. A configuration it cannot edit safely (JSON5, JSONC comments, package.json, a
// malformed or ambiguous packageRules) is reported with the rule to add by hand.
func reconcileRenovateIgnore(ctx context.Context, s *adoptSession) error {
	paths, err := renovateManagedPaths(ctx, s)
	if err != nil {
		return err
	}
	rel, data, found, err := findRenovateConfig(ctx, s, paths)
	if err != nil || !found {
		return err
	}
	if !renovateStrictJSONFiles[rel] {
		return reportRenovateUnsafe(s, rel, paths, "its file name marks JSONC or JSON5, which a rewrite cannot keep")
	}
	merged, changed, err := mergeRenovateRule(ctx, data, paths)
	var unsafe *renovateUnsafeError
	if errors.As(err, &unsafe) {
		return reportRenovateUnsafe(s, rel, paths, unsafe.reason)
	}
	if err != nil {
		return err
	}
	if !changed {
		recordRenovateUnchanged(s, rel, len(paths) > 0)
		return nil
	}
	return publishRenovateConfig(ctx, s, rel, data, merged, len(paths) > 0)
}

// recordRenovateUnchanged reports a configuration that already holds what adoption wants:
// the managed entry, or, when no path needs one, none.
func recordRenovateUnchanged(s *adoptSession, rel string, managed bool) {
	if !managed {
		s.report.recordNotApplicable(rel, "No Praetor-managed file needs a Renovate rule here")
		return
	}
	s.report.recordReconciled(rel, "Renovate already leaves the Praetor-managed files alone")
}

// findRenovateConfig returns the first Renovate configuration file in Renovate's search
// order. Without one it checks package.json, which adoption never edits: a "renovate" member
// there is reported, and no configuration at all is recorded as not applicable.
func findRenovateConfig(ctx context.Context, s *adoptSession, paths []string) (string, []byte, bool, error) {
	for index := 0; index < len(renovateConfigFiles); index++ {
		data, exists, err := observeRepoFile(ctx, s, renovateConfigFiles[index])
		if err != nil || exists {
			return renovateConfigFiles[index], data, exists, err
		}
	}
	data, exists, err := observeRepoFile(ctx, s, packageJSONFile)
	if err != nil {
		return "", nil, false, err
	}
	if exists && packageJSONConfiguresRenovate(ctx, data) {
		return "", nil, false, reportRenovateUnsafe(s, packageJSONFile, paths, "Renovate configuration in package.json is deprecated upstream and not edited")
	}
	s.report.recordNotApplicable(renovateReportPath, "No Renovate configuration found; none created")
	return "", nil, false, nil
}

func observeRepoFile(ctx context.Context, s *adoptSession, rel string) ([]byte, bool, error) {
	full, err := repoFile(s.repoPath, rel)
	if err != nil {
		return nil, false, err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", rel, err)
	}
	return data, exists, nil
}

// packageJSONConfiguresRenovate reports whether data is a strict JSON object with a
// top-level "renovate" member.
func packageJSONConfiguresRenovate(ctx context.Context, data []byte) bool {
	if clientjson.Validate(ctx, data) != nil {
		return false
	}
	object, err := clientjson.DecodeObject(data)
	if err != nil {
		return false
	}
	_, found := object.Get("renovate")
	return found
}

// renovateManagedPaths returns the managed paths of every family the active facets enable,
// except a family whose Source is in the repository: that repository is the family's
// origin, where the files are sources its own Renovate is meant to update.
func renovateManagedPaths(ctx context.Context, s *adoptSession) ([]string, error) {
	enabled, err := documentationEnabledForSession(s)
	if err != nil || !enabled {
		return nil, err
	}
	families := DocumentationFamilies()
	var paths []string
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		_, origin, err := observeRepoFile(ctx, s, families[index].Source)
		if err != nil {
			return nil, err
		}
		if !origin {
			paths = append(paths, families[index].ManagedPaths()...)
		}
	}
	return paths, nil
}

// reportRenovateUnsafe records a configuration left untouched, with the rule to add by hand
// when one is wanted.
func reportRenovateUnsafe(s *adoptSession, rel string, paths []string, reason string) error {
	if len(paths) == 0 {
		s.report.recordNotApplicable(rel, "No Praetor-managed file needs a Renovate rule; "+reason)
		return nil
	}
	rule, err := renderRenovateRule(paths)
	if err != nil {
		return err
	}
	s.report.recordSkipped(rel, "Renovate configuration left untouched: "+reason+
		"; add this entry to its packageRules so Renovate leaves the Praetor-managed files alone: "+string(rule))
	return nil
}

func renderRenovateRule(paths []string) (jsontext.Value, error) {
	raw, err := json.Marshal(renovateRule{Description: renovateRuleDescription, MatchFileNames: paths}, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("render Renovate rule: %w", err)
	}
	return raw, nil
}

func publishRenovateConfig(ctx context.Context, s *adoptSession, rel string, data, merged []byte, present bool) error {
	if !s.opts.DryRun {
		full, err := repoFile(s.repoPath, rel)
		if err != nil {
			return err
		}
		options := contextopt.ReplaceOptions{Expected: data, Exists: true, Mode: filePerm}
		if err := contextopt.ReplaceSnapshot(ctx, full, merged, options); err != nil {
			return fmt.Errorf("update %s: %w", rel, err)
		}
	}
	if !present {
		s.report.recordReconciledAs(rel, actionRemove,
			"Removed the Praetor-managed packageRules entry: no Praetor-managed file needs it")
		return nil
	}
	s.report.recordReconciledAs(rel, actionMerge,
		"Declared the Praetor-managed files to Renovate in a packageRules entry that disables their updates; the file is re-indented with two spaces")
	return nil
}

// mergeRenovateRule returns data with exactly one managed packageRules entry listing paths,
// or none when paths is empty, and whether that changed anything. An entry already equal to
// the rule, in any formatting, is a no-op. Every other member and entry keeps its order and
// value; the rewritten document is re-indented with two spaces (clientjson.Object.Encode)
// in data's line-ending style. A *renovateUnsafeError marks a document it will not rewrite.
func mergeRenovateRule(ctx context.Context, data []byte, paths []string) ([]byte, bool, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, false, renovateUnsafe("its line endings are mixed")
	}
	if clientjson.Validate(ctx, []byte(text)) != nil {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, renovateUnsafe("it is not one strict JSON object of at most 1 MiB; comments, trailing commas and duplicate members cannot survive a rewrite")
	}
	root, err := clientjson.DecodeObject([]byte(text))
	if err != nil {
		return nil, false, renovateUnsafe("it is not one JSON object")
	}
	rules, err := renovatePackageRules(root)
	if err != nil {
		return nil, false, err
	}
	next, changed, err := withRenovateRule(rules, paths)
	if err != nil || !changed {
		return nil, false, err
	}
	encodedRules, err := json.Marshal(next)
	if err != nil {
		return nil, false, fmt.Errorf("encode Renovate packageRules: %w", err)
	}
	encoded, err := root.With("packageRules", encodedRules).Encode()
	if err != nil {
		return nil, false, fmt.Errorf("encode Renovate configuration: %w", err)
	}
	return []byte(util.RestoreLineEndings(string(encoded), crlf)), true, nil
}

// renovatePackageRules returns the packageRules entries of root; none when root declares no
// such member.
func renovatePackageRules(root clientjson.Object) ([]jsontext.Value, error) {
	raw, declared := root.Get("packageRules")
	if !declared {
		return nil, nil
	}
	var rules []jsontext.Value
	if raw.Kind() != '[' || json.Unmarshal(raw, &rules) != nil {
		return nil, renovateUnsafe("its packageRules is not an array")
	}
	if len(rules) > maxRenovateRules {
		return nil, renovateUnsafe("its packageRules holds %d entries, more than %d", len(rules), maxRenovateRules)
	}
	return rules, nil
}

// withRenovateRule returns rules with the managed entry replaced in place, appended when
// absent, or removed when paths is empty, and whether that differs from rules. Two managed
// entries are ambiguous and refused.
func withRenovateRule(rules []jsontext.Value, paths []string) ([]jsontext.Value, bool, error) {
	found := -1
	for index := 0; index < len(rules) && index < maxRenovateRules; index++ {
		if !isManagedRenovateRule(rules[index]) {
			continue
		}
		if found >= 0 {
			return nil, false, renovateUnsafe("its packageRules holds two entries described %q", renovateRuleDescription)
		}
		found = index
	}
	if len(paths) == 0 {
		if found < 0 {
			return rules, false, nil
		}
		return append(rules[:found:found], rules[found+1:]...), true, nil
	}
	rule, err := renderRenovateRule(paths)
	if err != nil {
		return nil, false, err
	}
	if found < 0 {
		return append(rules, rule), true, nil
	}
	if sameJSON(rules[found], rule) {
		return rules, false, nil
	}
	next := append([]jsontext.Value(nil), rules...)
	next[found] = rule
	return next, true, nil
}

// isManagedRenovateRule reports whether rule is an object carrying the managed description.
func isManagedRenovateRule(rule jsontext.Value) bool {
	if rule.Kind() != '{' {
		return false
	}
	object, err := clientjson.DecodeObject(rule)
	if err != nil {
		return false
	}
	description, _ := object.Get("description")
	return clientjson.StringValue(description) == renovateRuleDescription
}

// sameJSON reports whether a and b hold the same JSON value, member order and formatting
// aside (RFC 8785 canonical form).
func sameJSON(a, b jsontext.Value) bool {
	left, right := a.Clone(), b.Clone()
	if left.Canonicalize() != nil || right.Canonicalize() != nil {
		return false
	}
	return string(left) == string(right)
}
