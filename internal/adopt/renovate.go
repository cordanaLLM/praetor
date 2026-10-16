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
	"github.com/cordanaLLM/praetor/internal/devcontainer"
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
//
// An adopter's own rule may already keep Renovate away from managed files, for example one
// disabling tools/markdownlint/**. The managed entry then lists only the paths no such rule
// covers, and is left out when every path is covered (#502). Adoption never reorders or
// rewrites an adopter rule. A rule counts as covering a path only when it provably has the
// managed entry's effect on it (uncoveredRenovatePaths):
//
//   - its members are description, matchFileNames and enabled alone, with enabled false. Any
//     other member, a further matcher such as matchManagers or matchUpdateTypes included,
//     could narrow the rule, so the rule is not counted.
//   - one of its matchFileNames patterns, at most maxRenovatePatterns of them, matches the path.
//     Renovate reads each pattern as a regular expression or a minimatch glob with dot and
//     nocase set, and "*" as every file (lib/util/string-match.ts). Adoption reads the glob
//     subset of literal text, "*", "?" and whole "**" segments, case-sensitively; a trailing
//     "**" spans one or more segments, as in minimatch. A list holding any other pattern
//     (negation, regular expression, class, brace, group or escape) is not counted.
//   - no later adopter rule sets enabled to anything but false, since Renovate applies
//     packageRules in order and such a rule may re-enable the path.
//
// Each test errs towards keeping the managed entry: a rule adoption cannot read this way costs
// a redundant entry, never an unprotected file.
//
// The entry also lists the DevContainer bundle files Renovate's managers read, when the bundle
// is one Praetor generates (generatedDevContainerPaths): audit verifies them against the
// rendering of the pinned catalog and their recorded inputs, so a bot's edit fails it, and
// Renovate reads the digest-only images of Dockerfile.praetor as latest and proposes another
// image (#323). Their updates arrive with a regeneration from a reviewed Praetor checkout.

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
	// maxRenovatePatterns bounds the matchFileNames patterns read from one adopter rule when
	// checking whether it covers the managed paths (HISS-02); a longer list is not counted.
	maxRenovatePatterns = 256
)

// renovateEquivalentMembers are the members an adopter rule may hold and still count as having
// the managed entry's effect.
var renovateEquivalentMembers = map[string]bool{"description": true, "matchFileNames": true, "enabled": true}

// renovateConfigFiles are Renovate's repository configuration files in its own search order
// (configFilePatterns in renovatebot/renovate lib/config/app-strings.ts, package.json aside).
// Renovate reads the first that exists and ignores the rest, so adoption edits only that one.
// Upstream getConfigFileNames also filters the list by platform (on GitHub it skips the
// .gitlab/ names, on GitLab the .github/ names, elsewhere both, adding .<platform>/ names
// after package.json). Adoption does not know the platform Renovate runs on and keeps the
// unfiltered order; the documentation governance guide tells adopters so.
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
// alone. A configuration it cannot read or edit safely (a symbolic link or unreadable file,
// JSON5, JSONC comments, package.json, a malformed or ambiguous packageRules) is reported
// with the rule to add by hand and left untouched.
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
	merged, declared, changed, err := mergeRenovateRule(ctx, data, paths)
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
	return publishRenovateConfig(ctx, s, rel, data, merged, len(paths), len(declared))
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
// there is reported, and no configuration at all is recorded as not applicable. A candidate
// that exists but cannot be read under the adoption read contract stops the search the same
// way (reportUninspectableRenovate): Renovate may read it, so no later file is edited.
func findRenovateConfig(ctx context.Context, s *adoptSession, paths []string) (string, []byte, bool, error) {
	for index := 0; index < len(renovateConfigFiles); index++ {
		data, exists, err := observeAdoptionInput(ctx, s, renovateConfigFiles[index])
		if err != nil {
			return "", nil, false, reportUninspectableRenovate(ctx, s, renovateConfigFiles[index], paths, err)
		}
		if exists {
			return renovateConfigFiles[index], data, true, nil
		}
	}
	data, exists, err := observeAdoptionInput(ctx, s, packageJSONFile)
	if err != nil {
		return "", nil, false, reportUninspectableRenovate(ctx, s, packageJSONFile, paths, err)
	}
	if exists && packageJSONConfiguresRenovate(ctx, data) {
		return "", nil, false, reportRenovateUnsafe(s, packageJSONFile, paths, "Renovate configuration in package.json is deprecated upstream and not edited")
	}
	s.report.recordNotApplicable(renovateReportPath, "No Renovate configuration found; none created")
	return "", nil, false, nil
}

// reportUninspectableRenovate reports a Renovate configuration candidate that exists but
// cannot be read safely: a symbolic link, a directory or other non-regular file, a file over
// 1 MiB, or one the process may not read (contextopt.ObserveSnapshot). Such a file is
// reported with the entry to add by hand and left untouched; it never fails adoption. Only a
// cancelled or expired adoption context fails the step, because then nothing was observed.
func reportUninspectableRenovate(ctx context.Context, s *adoptSession, rel string, paths []string, err error) error {
	reason, err := uninspectableReason(ctx, err)
	if err != nil {
		return err
	}
	return reportRenovateUnsafe(s, rel, paths, reason)
}

// uninspectableReason returns the report reason for a third-party configuration candidate the
// adoption read contract refused, err, or an error when the adoption context ended, because
// then nothing was observed. The Renovate and actionlint steps both read their candidates so.
func uninspectableReason(ctx context.Context, err error) (string, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(err, ctxErr) {
			return "", err
		}
		return "", fmt.Errorf("%w: %w", ctxErr, err)
	}
	return "it cannot be read safely (" + err.Error() + ")", nil
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
// origin, where the files are sources its own Renovate is meant to update. The generated
// DevContainer bundle files and the hosted REUSE gate follow them.
func renovateManagedPaths(ctx context.Context, s *adoptSession) ([]string, error) {
	families, err := enabledManagedFamiliesForSession(ctx, s)
	if err != nil {
		return nil, err
	}
	adopted := make([]managedasset.Family, 0, len(families))
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		_, origin, err := observeAdoptionInput(ctx, s, families[index].Source)
		if err != nil {
			return nil, err
		}
		if !origin {
			adopted = append(adopted, families[index])
		}
	}
	bundle, err := generatedDevContainerPaths(ctx, s)
	if err != nil {
		return nil, err
	}
	reuse, err := reuseManagedPaths(ctx, s)
	if err != nil {
		return nil, err
	}
	return append(append(managedPathsOf(adopted), bundle...), reuse...), nil
}

// generatedDevContainerPaths returns the DevContainer bundle files Renovate's managers read
// (devcontainer.UpdateBotBundleFiles) when the repository's bundle is one Praetor generates,
// or will be after this adoption: no devcontainer.json yet, which the dev-container step
// creates; --force, under which that step replaces it; or a file recording a bootstrap
// specification, edited since or not. An existing DevContainer of the operator's own, which adoption preserves, is
// theirs to update and contributes no path; so does a file the adoption read contract refuses
// (a symbolic link, a directory, a file over 1 MiB), which that step preserves the same way.
// Only an ended adoption context is an error, because then nothing was observed.
func generatedDevContainerPaths(ctx context.Context, s *adoptSession) ([]string, error) {
	data, exists, err := observeAdoptionInput(ctx, s, devcontainerFile)
	if err != nil {
		_, err = uninspectableReason(ctx, err)
		return nil, err
	}
	if exists && !s.opts.Force && !devcontainer.RecordsBootstrap(data) {
		return nil, nil
	}
	return devcontainer.UpdateBotBundleFiles(devcontainerFile), nil
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

// publishRenovateConfig writes merged over data and reports the rewrite: managed counts the
// paths Renovate must leave alone, declared those the managed entry lists.
func publishRenovateConfig(ctx context.Context, s *adoptSession, rel string, data, merged []byte, managed, declared int) error {
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
	action, detail := renovatePublishDetail(managed, declared)
	s.report.recordReconciledAs(rel, action, detail)
	return nil
}

// renovatePublishDetail returns the report action and detail of a rewrite whose managed entry
// lists declared of the managed paths.
func renovatePublishDetail(managed, declared int) (string, string) {
	const reindented = "; the file is re-indented with two spaces"
	switch {
	case managed == 0:
		return actionRemove, "Removed the Praetor-managed packageRules entry: no Praetor-managed file needs it"
	case declared == 0:
		return actionRemove, "Removed the Praetor-managed packageRules entry: the configuration's own rules already disable Renovate for every Praetor-managed file" + reindented
	case declared < managed:
		return actionMerge, fmt.Sprintf("Declared to Renovate, in a packageRules entry that disables their updates, the %d of %d Praetor-managed files its own rules do not already disable", declared, managed) + reindented
	}
	return actionMerge, "Declared the Praetor-managed files to Renovate in a packageRules entry that disables their updates" + reindented
}

// mergeRenovateRule returns data with at most one managed packageRules entry, listing the
// paths no adopter rule already covers (uncoveredRenovatePaths), or none when that leaves no
// path; those declared paths; and whether that changed anything. An entry already equal to
// the rule, in any formatting, is a no-op. Every other member and entry keeps its order and
// value; the rewritten document is re-indented with two spaces (clientjson.Object.Encode)
// in data's line-ending style. A *renovateUnsafeError marks a document it will not rewrite.
func mergeRenovateRule(ctx context.Context, data []byte, paths []string) ([]byte, []string, bool, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, nil, false, renovateUnsafe("its line endings are mixed")
	}
	if clientjson.Validate(ctx, []byte(text)) != nil {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		return nil, nil, false, renovateUnsafe("it is not one strict JSON object of at most 1 MiB; comments, trailing commas and duplicate members cannot survive a rewrite")
	}
	root, err := clientjson.DecodeObject([]byte(text))
	if err != nil {
		return nil, nil, false, renovateUnsafe("it is not one JSON object")
	}
	rules, err := renovatePackageRules(root)
	if err != nil {
		return nil, nil, false, err
	}
	declared := uncoveredRenovatePaths(rules, paths)
	next, changed, err := withRenovateRule(rules, declared)
	if err != nil || !changed {
		return nil, declared, false, err
	}
	encodedRules, err := json.Marshal(next)
	if err != nil {
		return nil, nil, false, fmt.Errorf("encode Renovate packageRules: %w", err)
	}
	encoded, err := root.With("packageRules", encodedRules).Encode()
	if err != nil {
		return nil, nil, false, fmt.Errorf("encode Renovate configuration: %w", err)
	}
	return []byte(util.RestoreLineEndings(string(encoded), crlf)), declared, true, nil
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

func findManagedRenovateRule(rules []jsontext.Value) (int, error) {
	found := -1
	for index := 0; index < len(rules) && index < maxRenovateRules; index++ {
		if !isManagedRenovateRule(rules[index]) {
			continue
		}
		if found >= 0 {
			return -1, renovateUnsafe("its packageRules holds two entries described %q", renovateRuleDescription)
		}
		found = index
	}
	return found, nil
}

// withRenovateRule returns rules with the managed entry replaced in place, appended when
// absent, or removed when paths is empty, and whether that differs from rules. Two managed
// entries are ambiguous and refused, and so is an append past maxRenovateRules: the written
// configuration stays within the bound renovatePackageRules reads back, so the rerun finds
// the entry instead of refusing the file.
func withRenovateRule(rules []jsontext.Value, paths []string) ([]jsontext.Value, bool, error) {
	found, err := findManagedRenovateRule(rules)
	if err != nil {
		return nil, false, err
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
		if len(rules) >= maxRenovateRules {
			return nil, false, renovateUnsafe("its packageRules holds %d entries, and the managed entry would exceed %d", len(rules), maxRenovateRules)
		}
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
	_, managed := decodeRenovateRule(rule)
	return managed
}

// decodeRenovateRule returns rule as an object, nil when it is not one, and whether it carries
// the managed description.
func decodeRenovateRule(rule jsontext.Value) (clientjson.Object, bool) {
	if rule.Kind() != '{' {
		return nil, false
	}
	object, err := clientjson.DecodeObject(rule)
	if err != nil {
		return nil, false
	}
	description, _ := object.Get("description")
	return object, clientjson.StringValue(description) == renovateRuleDescription
}

// adopterRenovateRule returns rule as an object when it is one and not the managed entry,
// which adoption rewrites and so never counts as the adopter's.
func adopterRenovateRule(rule jsontext.Value) (clientjson.Object, bool) {
	object, managed := decodeRenovateRule(rule)
	return object, object != nil && !managed
}

// uncoveredRenovatePaths returns, in order, the paths no adopter rule already disables the
// way the managed entry would (the equivalence the comment at the top of this file defines).
// The work is bounded by maxRenovateRules rules of at most maxRenovatePatterns patterns each.
func uncoveredRenovatePaths(rules []jsontext.Value, paths []string) []string {
	covered := make([]bool, len(paths))
	for index := renovateCoverageStart(rules); index < len(rules) && index < maxRenovateRules; index++ {
		patterns, disables := disablingRenovatePatterns(rules[index])
		if !disables {
			continue
		}
		for item := 0; item < len(paths); item++ {
			covered[item] = covered[item] || util.RenovatePatternsCover(patterns, paths[item])
		}
	}
	uncovered := make([]string, 0, len(paths))
	for item := 0; item < len(paths); item++ {
		if !covered[item] {
			uncovered = append(uncovered, paths[item])
		}
	}
	return uncovered
}

// renovateCoverageStart returns the index after the last adopter rule that sets enabled to
// anything but false: Renovate applies packageRules in order, so such a rule may re-enable a
// path an earlier rule disabled, and only rules after it can cover one.
func renovateCoverageStart(rules []jsontext.Value) int {
	start := 0
	for index := 0; index < len(rules) && index < maxRenovateRules; index++ {
		object, adopter := adopterRenovateRule(rules[index])
		if !adopter {
			continue
		}
		if enabled, declared := object.Get("enabled"); declared && enabled.Kind() != 'f' {
			start = index + 1
		}
	}
	return start
}

// disablingRenovatePatterns returns the matchFileNames patterns of an adopter rule that
// disables Renovate for exactly the files they match, and false for any other rule.
func disablingRenovatePatterns(rule jsontext.Value) ([]string, bool) {
	object, adopter := adopterRenovateRule(rule)
	if !adopter {
		return nil, false
	}
	for index := 0; index < len(object); index++ {
		if !renovateEquivalentMembers[object[index].Name] {
			return nil, false
		}
	}
	if enabled, _ := object.Get("enabled"); enabled.Kind() != 'f' {
		return nil, false
	}
	raw, _ := object.Get("matchFileNames")
	return supportedRenovatePatterns(raw)
}

// supportedRenovatePatterns returns the patterns of a matchFileNames value when it is a
// non-empty array of at most maxRenovatePatterns strings, each in the glob subset adoption
// reads (util.RenovateGlobSupported).
func supportedRenovatePatterns(raw jsontext.Value) ([]string, bool) {
	var patterns []string
	if raw.Kind() != '[' || json.Unmarshal(raw, &patterns) != nil || len(patterns) == 0 || len(patterns) > maxRenovatePatterns {
		return nil, false
	}
	for index := 0; index < len(patterns); index++ {
		if !util.RenovateGlobSupported(patterns[index]) {
			return nil, false
		}
	}
	return patterns, true
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
