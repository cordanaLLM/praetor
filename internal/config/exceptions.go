// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// ExceptionRuleClangTidyCoverage is the rule of the clang-tidy translation-unit coverage gate
// (internal/tidycoverage): an entry excuses a tracked translation unit that no declared
// clang-tidy lane reads.
const ExceptionRuleClangTidyCoverage = "clang-tidy-coverage"

// ExceptionRuleCredits is the rule of the credits gate (internal/supplychain,
// CheckUpstreamCredits): an entry excuses a docs/credits.yaml entry whose license could not be
// verified upstream and is written unknown, when the entry names the excused path.
const ExceptionRuleCredits = "credits"

// ExceptionRuleSupplyChain is the rule of the HISS-11 supply-chain gate
// (internal/adopt.AuditSupplyChain): an entry names the release workflow whose measured SLSA
// Build level, cosign signing or SBOM generation falls short of what the policy declares. The
// gate prints the declared and measured values with the entry's reason and expiry and passes,
// so the gap stays visible until the entry expires.
const ExceptionRuleSupplyChain = "HISS-11"

// ExceptionRuleWorkflowTriggers is the rule of the HISS-18 workflow trigger check
// (internal/forge.AuditWorkflowTriggers): an entry names one workflow that must run on every
// branch push or on a draft pull request. The check prints the workflow's findings with the
// entry's reason and expiry instead of reporting them, until the entry expires.
const ExceptionRuleWorkflowTriggers = "HISS-18"

// ExceptionRuleBuildWarnings is the rule of the HISS-10 build-warnings gate
// (internal/adopt.AuditBuildWarnings): an entry names a workflow whose build lanes cannot run
// with warnings as errors yet. The gate prints each excused lane with the entry's reason and
// expiry and passes, so the lanes stay visible until the entry expires.
const ExceptionRuleBuildWarnings = "HISS-10"

// ExceptionRuleRootLicenseNotice is the rule of the one-root-licence gate
// (supplychain.CheckRootLicense): an entry keeps one file at the repository root that is named
// like a licence, such as an upstream COPYING, beside the root LICENSE. It names that file by
// path, one entry per file.
const ExceptionRuleRootLicenseNotice = "root-license-notice"

// ExceptionRuleReuseAnnotationOrder is the rule of the REUSE.toml annotation order gate
// (praetorctl audit, supplychain.ReuseShadowedPaths): an entry names the root REUSE.toml whose
// path globs need more comparison steps than the gate's bound, so the gate reports the order as
// not checked, with the entry's reason and expiry, instead of failing. A file the gate checks in
// full makes the entry stale.
const ExceptionRuleReuseAnnotationOrder = "reuse-annotation-order"

// ExceptionRuleDedupe is the rule of the HISS-19 deduplication gate (internal/dedupe): an entry
// excuses one Go file whose duplicate function blocks sit only in excepted files, such as a code
// generator's repeated output.
const ExceptionRuleDedupe = "HISS-19"

// ExceptionRuleSecrets is the rule of the HISS-12 secret scan gate (internal/secretscan): an entry
// excuses one file by path and one gitleaks rule id by tool, with an optional fingerprint pinning
// one finding.
const ExceptionRuleSecrets = "HISS-12"

// ExceptionRuleDeclaredLinters is the rule of the declared-linters gate
// (internal/adopt.AuditDeclaredLinters): an entry excuses one declared linter
// that is not run by any CI workflow, naming the tool by tool.
const ExceptionRuleDeclaredLinters = "declared-linters"

// ExceptionRuleTestTimeout names the test-timeout rule.
const ExceptionRuleTestTimeout = "test-timeout"

// ExceptionRuleMutants is the rule for tracked mutation-tool configuration exclusions.
const ExceptionRuleMutants = "mutants"

// ExceptionRuleBacklogCapsDefects is the rule of the defects backlog cap gate.
const ExceptionRuleBacklogCapsDefects = "backlog.caps.defects"

// ExceptionRuleBacklogCapsTasks is the rule of the tasks backlog cap gate.
const ExceptionRuleBacklogCapsTasks = "backlog.caps.tasks"

// ExceptionRuleBacklogCapsQuestions is the rule of the questions backlog cap gate.
const ExceptionRuleBacklogCapsQuestions = "backlog.caps.questions"

// ExceptionRuleBacklogCapsForgeAlerts is the rule of the forge alerts backlog cap gate.
const ExceptionRuleBacklogCapsForgeAlerts = "backlog.caps.forge_alerts"

// ExceptionRulePlatformNeutrality is the rule of the HISS-21 platform neutrality gate.
const ExceptionRulePlatformNeutrality = "HISS-21"

// ExceptionRuleCodespell is the rule of the codespell exception gate.
const ExceptionRuleCodespell = "codespell"

// exceptionRules lists rules an exceptions entry may name. HISS IDs come from the single HISS
// registry; the other entries are the non-HISS gates that read this list.
var exceptionRules = append(hisscatalog.RuleIDs(),
	ExceptionRuleAPICompatibility, ExceptionRuleClangTidyCoverage, ExceptionRuleCredits,
	ExceptionRuleRootLicenseNotice, ExceptionRuleReuseAnnotationOrder, ExceptionRuleDeclaredLinters, ExceptionRuleTestTimeout, ExceptionRuleMutants,
	ExceptionRuleBacklogCapsDefects, ExceptionRuleBacklogCapsTasks, ExceptionRuleBacklogCapsQuestions, ExceptionRuleBacklogCapsForgeAlerts,
	ExceptionRuleCodespell,
)

// workflowRuleExamples maps each rule whose entries name one workflow file by path to the
// workflow a refusal names as an example.
var workflowRuleExamples = map[string]string{
	ExceptionRuleSupplyChain:      "release.yml",
	ExceptionRuleBuildWarnings:    "ci.yml",
	ExceptionRuleWorkflowTriggers: "release.yml",
	ExceptionRuleTestTimeout:      "portability.yml",
}

// Bounds of the exceptions list (HISS-02).
const (
	// MaxExceptions bounds the entries one manifest declares.
	MaxExceptions = 1024
	// MaxExceptionDays is how far ahead of today an entry may expire. An exception is a debt
	// with a due date, reviewed again when it falls due; scripts/npm_audit_gate.py applies the
	// same bound to its own list.
	MaxExceptionDays = 90
	// ExceptionExpiryWarningDays is how many days before an expiry the audit begins warning.
	ExceptionExpiryWarningDays = 14
	// MaxExceptionReasonBytes bounds one entry's reason.
	MaxExceptionReasonBytes = 1024
	// maxExceptionPathSegments bounds the segments of a path an entry is matched against.
	maxExceptionPathSegments = 256
)

// ExceptionDateLayout is the form of an entry's expires date.
const ExceptionDateLayout = "2006-01-02"

// globCharacters are the characters util.MatchGlobSegments reads as pattern syntax. A path
// entry names one file, so it may carry none of them.
const globCharacters = "*?[]\\"

// Exception is one entry of the manifest's exceptions list: one standard (Rule) waived for one
// file (Path) or for the files one glob matches (Glob), with the reason and the day after
// which the waiver stops holding (Expires, YYYY-MM-DD). The gate that owns Rule reads the
// entry; an expired entry excuses nothing, so the gate fails on its files as if the entry were
// missing (AGENTS.md rule 14).
type Exception struct {
	Rule        string `yaml:"rule"`
	Path        string `yaml:"path,omitempty"`
	Glob        string `yaml:"glob,omitempty"`
	Function    string `yaml:"function,omitempty"`
	Tool        string `yaml:"tool,omitempty"`
	Fingerprint string `yaml:"fingerprint,omitempty"`
	Reason      string `yaml:"reason"`
	Expires     string `yaml:"expires"`
	Category    string `yaml:"category,omitempty"`
	Entered     string `yaml:"entered,omitempty"`
}

// Target returns the path, glob or tool the entry names.
func (e Exception) Target() string {
	if e.Path != "" {
		return e.Path
	}
	if e.Glob != "" {
		return e.Glob
	}
	return e.Tool
}

// Expired reports whether the entry stopped holding before today; it still holds on its
// expires day. An expires value that is no date counts as expired, so an entry that slipped
// past validation excuses nothing.
func (e Exception) Expired(today time.Time) bool {
	expires, err := time.Parse(ExceptionDateLayout, e.Expires)
	return err != nil || expires.Before(ExceptionDay(today))
}

// Matches reports whether the entry names the slash-separated repository path rel: the same
// path, or a glob that matches it in full under util.MatchGlobSegments ("*" stays inside one
// segment, a "**" segment spans any number).
func (e Exception) Matches(rel string) bool {
	if e.Path != "" {
		return e.Path == rel
	}
	segments := strings.Split(rel, "/")
	if e.Glob == "" || len(segments) > maxExceptionPathSegments {
		return false
	}
	return util.MatchGlobSegments(strings.Split(e.Glob, "/"), segments)
}

// MatchesAny reports whether the entry's glob matches at least one of files, the repository's
// files as slash paths (#1259). A glob that matches no file excuses nothing, like a path that
// names no file. An entry without a glob reports false: a path is checked on disk by the caller,
// and a tool names no file.
func (e Exception) MatchesAny(files []string) bool {
	if e.Glob == "" {
		return false
	}
	for index := 0; index < len(files); index++ {
		if e.Matches(files[index]) {
			return true
		}
	}
	return false
}

// MatchesSecret reports whether the exception excuses finding: its rule must be HISS-12,
// its path must match findingPath, its tool must match ruleID, and if its fingerprint
// is set, it must match fingerprint.
func (e Exception) MatchesSecret(findingPath, ruleID, fingerprint string) bool {
	if e.Rule != ExceptionRuleSecrets {
		return false
	}
	entry := rules.Entry{
		Path:        e.Path,
		RuleID:      e.Tool,
		Fingerprint: e.Fingerprint,
	}
	return entry.MatchesFinding(findingPath, ruleID, fingerprint)
}

// ExceptionDay returns the calendar day of t, in t's location, as midnight UTC: the form an
// entry's expires date parses to, so the two compare as days.
func ExceptionDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// LoadExceptionsFor loads the repository manifest at root/.standards.yaml and returns the
// entries declaring rule. When root has no manifest, LoadExceptionsFor returns nil, nil.
func LoadExceptionsFor(ctx context.Context, root, rule string) ([]Exception, error) {
	if ctx == nil {
		return nil, errors.New("load exceptions requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ExceptionsFor(manifest.Exceptions, rule), nil
}

// ExceptionsFor returns the entries of entries that name rule, in declaration order: what the
// gate owning rule reads from the manifest's exceptions list.
func ExceptionsFor(entries []Exception, rule string) []Exception {
	var selected []Exception
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		if entries[index].Rule == rule {
			selected = append(selected, entries[index])
		}
	}
	return selected
}

// ExceptionFor returns what entries, the entries of one rule, say about the slash-separated
// repository path rel on today: the last live entry that names it (nil for none) and the first
// expired one that does (nil for none). A gate excuses rel when live is set, and otherwise fails
// on it, naming the expired entry when there is one. Every entry naming rel, live or expired, is
// marked in used, so the gate reports an entry no path marked as stale and an expired one only
// through its path. An entry beyond len(used) is judged but not marked.
func ExceptionFor(entries []Exception, rel string, today time.Time, used []bool) (live, expired *Exception) {
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		if !entries[index].Matches(rel) {
			continue
		}
		if index < len(used) {
			used[index] = true
		}
		switch {
		case !entries[index].Expired(today):
			live = &entries[index]
		case expired == nil:
			expired = &entries[index]
		}
	}
	return live, expired
}

// BacklogCapRule returns the exception rule for category, e.g. "backlog.caps.defects" for "defects".
func BacklogCapRule(category string) string {
	return "backlog.caps." + category
}

// IsBacklogCapRule reports whether rule is one of the backlog cap exception rules.
func IsBacklogCapRule(rule string) bool {
	switch rule {
	case ExceptionRuleBacklogCapsDefects, ExceptionRuleBacklogCapsTasks,
		ExceptionRuleBacklogCapsQuestions, ExceptionRuleBacklogCapsForgeAlerts:
		return true
	}
	return false
}

// BacklogExceptionFor returns the live and expired exceptions for category on today.
func BacklogExceptionFor(entries []Exception, category string, today time.Time) (live, expired *Exception) {
	rule := BacklogCapRule(category)
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		if entries[index].Rule != rule {
			continue
		}
		switch {
		case !entries[index].Expired(today):
			live = &entries[index]
		case expired == nil:
			expired = &entries[index]
		}
	}
	return live, expired
}

// ValidateExceptions refuses an exceptions list a gate could not apply as written: more than
// MaxExceptions entries, an unknown rule, an entry naming both or neither of path and glob, a
// path that is not one clean repository-relative file, a glob outside the StyleExclusionProblem
// rules, an empty or multi-line reason, an expires value that is no YYYY-MM-DD date or lies more
// than MaxExceptionDays after today, and a repeated entry. An expired entry is valid: the gate
// that owns its rule fails on its files instead.
func ValidateExceptions(entries []Exception, today time.Time) error {
	if len(entries) > MaxExceptions {
		return fmt.Errorf("exceptions has %d entries; maximum is %d", len(entries), MaxExceptions)
	}
	seen := make(map[[6]string]int, len(entries))
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		entry := entries[index]
		if problem := entry.problem(today); problem != "" {
			return fmt.Errorf("exceptions[%d] %s", index, problem)
		}
		key := [6]string{entry.Rule, entry.Path, entry.Glob, entry.Function, entry.Tool, entry.Fingerprint}
		if first, repeated := seen[key]; repeated {
			return fmt.Errorf("exceptions[%d] repeats exceptions[%d] (rule %s, %s)", index, first, entry.Rule, entry.Target())
		}
		seen[key] = index
	}
	return nil
}

// problem names why one entry is refused, or returns "".
func (e Exception) problem(today time.Time) string {
	if !slices.Contains(exceptionRules, e.Rule) {
		return fmt.Sprintf("rule %q is not a rule any gate reads from this list (known: %s)", e.Rule, strings.Join(exceptionRules, ", "))
	}
	if problem := e.targetProblem(); problem != "" {
		return problem
	}
	if problem := e.workflowTargetProblem(); problem != "" {
		return problem
	}
	if problem := e.rootLicenseTargetProblem(); problem != "" {
		return problem
	}
	if problem := e.apiModuleTargetProblem(); problem != "" {
		return problem
	}
	if problem := e.dedupeTargetProblem(); problem != "" {
		return problem
	}
	if problem := e.codespellProblem(); problem != "" {
		return problem
	}
	if problem := e.functionProblem(); problem != "" {
		return problem
	}
	if problem := exceptionReasonProblem(e.Reason); problem != "" {
		return "reason " + problem
	}
	return exceptionCategoryExpiryProblem(e, today)
}

func exceptionCategoryExpiryProblem(e Exception, today time.Time) string {
	if e.Category == "" {
		return uncategorizedExceptionExpiryProblem(e, today)
	}
	return categorizedExceptionExpiryProblem(e, today)
}

func uncategorizedExceptionExpiryProblem(e Exception, today time.Time) string {
	if e.Entered != "" {
		return "entered requires a category"
	}
	if e.Rule != ExceptionRuleMutants {
		return exceptionExpiryProblem(e.Expires, today)
	}
	if _, err := time.Parse(ExceptionDateLayout, e.Expires); err == nil {
		return ""
	}
	return fmt.Sprintf("expires %q must be a YYYY-MM-DD date", e.Expires)
}

func categorizedExceptionExpiryProblem(e Exception, today time.Time) string {
	return rules.CategoryExpiryProblem(e.Category, e.Entered, e.Expires, today)
}

// ExceptionCategoryDays returns the days allowed for category, and whether the category is valid.
func ExceptionCategoryDays(category string) (int, bool) {
	return rules.CategoryDays(category)
}

// Problem reports why e is refused as an exception entry, or "".
func (e Exception) Problem(today time.Time) string {
	return e.problem(today)
}

func (e Exception) functionProblem() string {
	if e.Function == "" {
		return ""
	}
	if !slices.Contains(hisscatalog.ScannerRuleIDs(), e.Rule) {
		return fmt.Sprintf("function is only valid for a HISS scanner rule, got %s", e.Rule)
	}
	if e.Path == "" || e.Glob != "" {
		return "function requires one file path and no glob"
	}
	if len(e.Function) > 256 || strings.TrimSpace(e.Function) != e.Function || strings.ContainsAny(e.Function, "\r\n\t") {
		return "function must be a single name of at most 256 bytes"
	}
	return ""
}

func (e Exception) targetProblem() string {
	if e.Rule == ExceptionRuleMutants {
		if e.Tool != "" || e.Glob != "" || e.Path == "" {
			return "rule mutants must name one mutation-tool configuration file by path"
		}
		return e.pathOrGlobTargetProblem()
	}
	if e.Rule == ExceptionRuleDeclaredLinters {
		return e.declaredLintersTargetProblem()
	}
	if IsBacklogCapRule(e.Rule) {
		return e.backlogCapTargetProblem()
	}
	if e.Rule == ExceptionRuleSecrets {
		return e.secretsTargetProblem()
	}
	if e.Tool != "" {
		return fmt.Sprintf("rule %s must not set tool", e.Rule)
	}
	return e.pathOrGlobTargetProblem()
}

func (e Exception) secretsTargetProblem() string {
	return rules.ValidateHISS12Target(e.Path, e.Tool, e.Fingerprint, e.Glob)
}

func (e Exception) backlogCapTargetProblem() string {
	if e.Tool != "backlog" {
		return "rule " + e.Rule + " must name tool: backlog"
	}
	if e.Path != "" || e.Glob != "" {
		return "rule " + e.Rule + " must not set path or glob"
	}
	return ""
}

func (e Exception) declaredLintersTargetProblem() string {
	if e.Tool == "" {
		return "rule declared-linters must name one tool"
	}
	if e.Path != "" || e.Glob != "" {
		return "rule declared-linters must not set path or glob"
	}
	return ""
}

func (e Exception) pathOrGlobTargetProblem() string {
	if slices.Contains(hisscatalog.ScannerRuleIDs(), e.Rule) && (e.Path == "" || e.Glob != "") {
		return fmt.Sprintf("rule %s must name one file by path, not a glob", e.Rule)
	}
	if (e.Path == "") == (e.Glob == "") {
		return "must name exactly one of path and glob"
	}
	if e.Path != "" {
		if !ValidRepositoryPath(e.Path) || strings.ContainsAny(e.Path, globCharacters) {
			return fmt.Sprintf("path %q must be one clean repository-relative file path of at most %d bytes, without glob characters",
				e.Path, maxRepositoryPath)
		}
		return ""
	}
	if problem := StyleExclusionProblem(e.Glob); problem != "" {
		return fmt.Sprintf("glob %q %s", e.Glob, problem)
	}
	return ""
}

// rootLicenseTargetProblem requires a root-license-notice or reuse-annotation-order entry to name
// one file at the repository root by path, the file the root licence gate keeps or the REUSE.toml
// the order gate excuses, one entry per file.
func (e Exception) rootLicenseTargetProblem() string {
	if (e.Rule == ExceptionRuleRootLicenseNotice || e.Rule == ExceptionRuleReuseAnnotationOrder) &&
		(e.Glob != "" || strings.Contains(e.Path, "/")) {
		return "rule " + e.Rule + " must name one file at the repository root by path, one entry per file"
	}
	return ""
}

// dedupeTargetProblem requires a HISS-19 entry to name one repository file by path, one entry per file.
func (e Exception) dedupeTargetProblem() string {
	if e.Rule == ExceptionRuleDedupe && e.Glob != "" {
		return "rule " + e.Rule + " must name one repository file by path, one entry per file"
	}
	return ""
}

// codespellProblem requires a codespell entry to name one file by path and declare a category.
func (e Exception) codespellProblem() string {
	if e.Rule != ExceptionRuleCodespell {
		return ""
	}
	if e.Category == "" {
		return "rule codespell requires a category"
	}
	if e.Path == "" || e.Glob != "" {
		return "rule codespell must name one file by path, not a glob"
	}
	return ""
}

// workflowTargetProblem requires an entry of a rule in workflowRuleExamples to name one
// workflow document directly in .github/workflows by path: the release workflow the HISS-11
// supply-chain gate measured, or the workflow holding the build lanes a HISS-10 entry excuses.
func (e Exception) workflowTargetProblem() string {
	example, scoped := workflowRuleExamples[e.Rule]
	if !scoped || ghworkflow.IsWorkflowPath(e.Path) {
		return ""
	}
	return fmt.Sprintf("rule %s must name one workflow file directly in %s by path, such as %s/%s",
		e.Rule, ghworkflow.Dir, ghworkflow.Dir, example)
}

// exceptionReasonProblem requires one non-empty line of at most MaxExceptionReasonBytes.
func exceptionReasonProblem(reason string) string {
	switch {
	case strings.TrimSpace(reason) == "":
		return "must say why the standard cannot hold for the file"
	case len(reason) > MaxExceptionReasonBytes:
		return fmt.Sprintf("exceeds %d bytes", MaxExceptionReasonBytes)
	case strings.ContainsFunc(reason, unicode.IsControl):
		return "must be one line without control characters"
	}
	return ""
}

// exceptionExpiryProblem requires a YYYY-MM-DD date at most MaxExceptionDays after today.
func exceptionExpiryProblem(expires string, today time.Time) string {
	return rules.ExpiryProblem(expires, today)
}

// DaysLeft returns how many calendar days remain until e expires, judged against today:
// 0 on the expires day itself, positive before, and negative after. If e.Expires is not a
// valid date, DaysLeft returns a negative number and the parsing error.
func (e Exception) DaysLeft(today time.Time) (int, error) {
	expires, err := time.Parse(ExceptionDateLayout, e.Expires)
	if err != nil {
		return -1, fmt.Errorf("expires %q must be a YYYY-MM-DD date: %w", e.Expires, err)
	}
	return int(expires.Sub(ExceptionDay(today)).Hours() / 24), nil
}

// ExpiringWithin reports whether the entry expires within days from today (including today,
// when 0 days remain) and has not already expired.
func (e Exception) ExpiringWithin(today time.Time, days int) bool {
	daysLeft, err := e.DaysLeft(today)
	if err != nil {
		return false
	}
	return daysLeft >= 0 && daysLeft <= days
}

// WarningDays returns how many days before an expiry the audit begins warning for e.
// Uncategorized entries warn for ExceptionExpiryWarningDays (14 days). Categorized
// entries warn for min(ExceptionExpiryWarningDays, interval/2), where interval is the
// category's duration in days (B: 3 -> 1, C: 10 -> 5, D: 120 -> 14). For category A,
// interval is the days from entered to expires when both parse; if either does not
// parse, WarningDays falls back to ExceptionExpiryWarningDays (14 days).
func (e Exception) WarningDays() int {
	if e.Category == "" {
		return ExceptionExpiryWarningDays
	}
	if e.Category == "A" {
		entered, err := time.Parse(ExceptionDateLayout, e.Entered)
		if err != nil {
			return ExceptionExpiryWarningDays
		}
		expires, err := time.Parse(ExceptionDateLayout, e.Expires)
		if err != nil {
			return ExceptionExpiryWarningDays
		}
		interval := max(0, int(expires.Sub(entered).Hours()/24)) // an expiry before entry warns for 0 days, never less
		return min(ExceptionExpiryWarningDays, interval/2)
	}
	interval, ok := rules.CategoryDays(e.Category)
	if !ok {
		return ExceptionExpiryWarningDays
	}
	return min(ExceptionExpiryWarningDays, interval/2)
}

// FormatDaysLeft formats the remaining days into human-readable text.
func FormatDaysLeft(days int) string {
	switch {
	case days == 1:
		return "1 day left"
	case days >= 0:
		return fmt.Sprintf("%d days left", days)
	case days == -1:
		return "expired 1 day ago (-1 days left)"
	default:
		return fmt.Sprintf("expired %d days ago (%d days left)", -days, days)
	}
}

// FilterExceptionsExpiringWithin returns the entries of entries whose expiry is at or before
// today + days (daysLeft <= days).
func FilterExceptionsExpiringWithin(entries []Exception, today time.Time, days int) ([]Exception, error) {
	var filtered []Exception
	for index := 0; index < len(entries) && index < MaxExceptions; index++ {
		daysLeft, err := entries[index].DaysLeft(today)
		if err != nil {
			return nil, err
		}
		if daysLeft <= days {
			filtered = append(filtered, entries[index])
		}
	}
	return filtered, nil
}
