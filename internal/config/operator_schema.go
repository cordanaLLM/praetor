// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds of the operator command-policy deny list. This package owns them: the settings
// loader applies them to every layer and to the merged list, and the hook policy validates
// through ValidateCommandPolicyDeny instead of keeping its own copy.
const (
	MaxCommandPolicyDenyPatterns = 64
	MaxCommandPolicyPatternBytes = 512
	// MaxPermissionGrants bounds one client's merged grant list.
	MaxPermissionGrants = 32
	// MaxBuilderKits bounds framework.targets.<language>.builder_kits.
	MaxBuilderKits = 8
	// MaxReconcileRepos bounds forge.reconcile_repos and the repository set of one
	// `issue reconcile` run.
	MaxReconcileRepos = 256
	// MaxOrgContainers bounds the merged topology.org_containers list.
	MaxOrgContainers = 64
	// MaxFrameworkModuleBytes bounds framework.targets.<language>.module.
	MaxFrameworkModuleBytes = 256

	maxGrantBytes        = 512
	maxSettingValueBytes = 4096
	maxPythonCandidates  = 8
	maxPythonArgs        = 8
	maxPythonArgBytes    = 64
	maxOperatorSettings  = 512
	workstationLayer     = "workstation"
)

type settingKind uint8

const (
	kindText    settingKind = iota // string scalar
	kindBool                       // boolean scalar, canonical "true" or "false"
	kindList                       // sequence of strings
	kindArgv                       // sequence of candidates: a string or a sequence of strings
	kindEntry                      // clients.selected.<id>, framework.targets.<language>: lists the entry, holds its keys
	kindMapping                    // a nested mapping with keys of its own
)

type mergeRule uint8

const (
	ruleReplace  mergeRule = iota // a later layer replaces the value
	ruleTighten                   // once a layer sets strict, a later layer cannot move away
	ruleAppend                    // lists append, de-duplicated, bound applied to the result
	ruleConflict                  // two different non-empty values are an error
)

type settingCheck func(layer string, setting OperatorSetting) error

type settingSpec struct {
	kind     settingKind
	rule     mergeRule
	strict   string // ruleTighten: the value a later layer cannot loosen
	host     bool   // host data: accepted in the workstation layer only
	maxItems int    // kindList: bound per layer and after merging
	check    settingCheck
}

// operatorSpecs is the schema, keyed by dotted path; `*` stands for the identifier a
// wildcard prefix (wildcardPrefixes) names. A key that is not listed here is an error naming
// the key.
var operatorSpecs = map[string]settingSpec{
	"clients":                               {kind: kindMapping},
	"clients.mode":                          {rule: ruleTighten, strict: ClientModeStrict, check: oneOf(ClientModeAdvisory, ClientModeStrict)},
	"clients.verified_max_age":              {check: durationWithin(time.Hour, 2160*time.Hour)},
	"clients.govern":                        {rule: ruleTighten, strict: GovernPresent, check: oneOf(GovernPresent, GovernListed)},
	"clients.selected":                      {kind: kindMapping},
	"clients.selected.*":                    {kind: kindEntry},
	"clients.selected.*.required":           {kind: kindBool, rule: ruleTighten, strict: "true"},
	"clients.selected.*.scopes":             {kind: kindList, maxItems: 2, check: scopeList},
	"clients.selected.*.plugin":             {kind: kindBool},
	"clients.selected.*.binary":             {host: true, check: hostPath},
	"clients.selected.*.config_root":        {host: true, check: hostPath},
	"clients.selected.*.registry":           {rule: ruleConflict, check: documentPath},
	"clients.selected.*.connection_profile": {check: documentPath},
	"clients.selected.*.permissions":        {kind: kindMapping},
	"clients.selected.*.permissions.manage": {kind: kindBool},
	"clients.selected.*.permissions.allow":  {kind: kindList, rule: ruleAppend, maxItems: MaxPermissionGrants, check: grantList},
	"hooks":                                 {kind: kindMapping},
	"hooks.scope":                           {rule: ruleTighten, strict: HookScopeAll, check: oneOf(HookScopeGoverned, HookScopeAll)},
	"hooks.command_policy":                  {kind: kindMapping},
	"hooks.command_policy.deny":             {kind: kindList, rule: ruleAppend, maxItems: MaxCommandPolicyDenyPatterns, check: denyList},
	"hooks.python":                          {kind: kindArgv, check: pythonCandidates},
	"update":                                {kind: kindMapping},
	"update.channel":                        {check: oneOf("push")},
	"update.source":                         {check: oneOf("checkout")},
	"update.checkout":                       {host: true, check: hostPath},
	"update.remote":                         {check: matching(remotePattern, "a remote name of 1..64 letters, digits, '.', '_' or '-'")},
	"update.branch":                         {check: branchName},
	"update.pin":                            {check: matching(pinPattern, "empty or a 40-character lowercase commit id")},
	"update.interval":                       {check: durationWithin(5*time.Minute, 24*time.Hour)},
	"update.require_signed":                 {kind: kindBool, rule: ruleTighten, strict: "true"},
	"update.allowed_signers":                {check: documentPath},
	"update.receipt_public_key":             {check: matching(receiptKeyPattern, "empty or 64 lowercase hex characters (Ed25519)")},
	"update.bin_dir":                        {host: true, check: hostPath},
	"framework":                             {kind: kindMapping},
	"framework.targets":                     {kind: kindMapping},
	"framework.targets.*":                   {kind: kindEntry},
	"framework.targets.*.module":            {check: frameworkModule},
	"framework.targets.*.builder_kits":      {kind: kindList, maxItems: MaxBuilderKits, check: repositoryList},
	"framework.targets.*.contract":          {check: documentPath},
	"framework.targets.*.checkout":          {host: true, check: frameworkCheckout},
	"framework.migration_branch":            {check: optional(branchName)},
	"forge":                                 {kind: kindMapping},
	"forge.default_owner":                   {check: optional(githubOwner)},
	"forge.reconcile_repos":                 {kind: kindList, maxItems: MaxReconcileRepos, check: repositoryList},
	"forge.review_bot":                      {check: optional(reviewBot)},
	"topology":                              {kind: kindMapping},
	"topology.org_containers":               {kind: kindList, rule: ruleAppend, maxItems: MaxOrgContainers, check: orgContainerList},
}

// reservedValues are accepted spellings whose feature has not shipped yet.
var reservedValues = map[string]string{
	"update.channel=release": "the release channel is reserved until release artifacts ship",
	"update.source=artifact": "artifact installs are reserved until release artifacts ship",
}

var (
	remotePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	branchPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	pinPattern        = regexp.MustCompile(`^(|[0-9a-f]{40})$`)
	receiptKeyPattern = regexp.MustCompile(`^(|[0-9a-f]{64})$`)
	bareCommand       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	settingKey        = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	orgContainerName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Wildcard prefixes: the schema path element after each is an identifier, written `*` in
// operatorSpecs and checked by the prefix's own parser.
const (
	selectedPrefix         = "clients.selected."
	frameworkTargetsPrefix = "framework.targets."
)

// wildcardPrefixes maps each wildcard prefix to the check its identifier must pass.
var wildcardPrefixes = [...]struct {
	prefix string
	check  func(id string) error
}{
	{selectedPrefix, func(id string) error {
		_, err := clientid.Parse(id)
		return err
	}},
	{frameworkTargetsPrefix, func(id string) error {
		_, err := ParseFrameworkLanguage(id)
		return err
	}},
}

// settingWildcard returns the wildcard prefix a path starts with, the identifier after it
// and the path of the key below the identifier ("" for the entry itself). A path outside
// every wildcard prefix returns three empty strings.
func settingWildcard(path string) (prefix, id, field string) {
	for _, wildcard := range wildcardPrefixes {
		rest, ok := strings.CutPrefix(path, wildcard.prefix)
		if !ok {
			continue
		}
		id, field, _ = strings.Cut(rest, ".")
		return wildcard.prefix, id, field
	}
	return "", "", ""
}

// genericSettingPath replaces the identifier of a wildcard path with `*`.
func genericSettingPath(path string) string {
	prefix, _, field := settingWildcard(path)
	if prefix == "" {
		return path
	}
	if field == "" {
		return prefix + "*"
	}
	return prefix + "*." + field
}

// settingSpecFor looks a concrete path up in the schema. An unknown key and an unknown
// wildcard identifier (a client, a framework language) are errors that name what was written.
func settingSpecFor(path string) (settingSpec, error) {
	generic := genericSettingPath(path)
	spec, ok := operatorSpecs[generic]
	if !ok {
		return spec, fmt.Errorf("unknown setting %q", path)
	}
	prefix, id, _ := settingWildcard(path)
	for _, wildcard := range wildcardPrefixes {
		if wildcard.prefix != prefix {
			continue
		}
		if err := wildcard.check(id); err != nil {
			return spec, fmt.Errorf("%s: %w", strings.TrimSuffix(prefix, "."), err)
		}
	}
	return spec, nil
}

// ValidateCommandPolicyDeny checks an operator deny list: at most 64 patterns, each 1..512
// bytes of valid UTF-8 without control bytes, each compiling as RE2. Patterns are exempt from
// the literal rule because a regular expression needs `$`; they are compiled, never expanded.
func ValidateCommandPolicyDeny(patterns []string) error {
	if len(patterns) > MaxCommandPolicyDenyPatterns {
		return fmt.Errorf("command policy deny list exceeds %d patterns", MaxCommandPolicyDenyPatterns)
	}
	for i := 0; i < len(patterns) && i < MaxCommandPolicyDenyPatterns; i++ {
		pattern := patterns[i]
		if pattern == "" || len(pattern) > MaxCommandPolicyPatternBytes || strings.ContainsFunc(pattern, isControl) {
			return fmt.Errorf("command policy deny pattern %d must be 1..%d bytes without control bytes", i, MaxCommandPolicyPatternBytes)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("command policy deny pattern %d: %w", i, err)
		}
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func oneOf(allowed ...string) settingCheck {
	return func(_ string, setting OperatorSetting) error {
		if slices.Contains(allowed, setting.Value) {
			return nil
		}
		if reason, ok := reservedValues[setting.Path+"="+setting.Value]; ok {
			return fmt.Errorf("%s: %s", setting.Path, reason)
		}
		return fmt.Errorf("%s must be one of %s", setting.Path, strings.Join(allowed, ", "))
	}
}

func durationWithin(low, high time.Duration) settingCheck {
	return func(_ string, setting OperatorSetting) error {
		value, err := time.ParseDuration(setting.Value)
		if err != nil || value < low || value > high {
			return fmt.Errorf("%s must be a duration from %s to %s", setting.Path, low, high)
		}
		return nil
	}
}

func matching(pattern *regexp.Regexp, want string) settingCheck {
	return func(_ string, setting OperatorSetting) error {
		if !pattern.MatchString(setting.Value) {
			return fmt.Errorf("%s must be %s", setting.Path, want)
		}
		return nil
	}
}

func branchName(_ string, setting OperatorSetting) error {
	if !branchPattern.MatchString(setting.Value) || strings.Contains(setting.Value, "..") {
		return fmt.Errorf("%s must be a branch name of 1..128 letters, digits, '.', '_', '/' or '-' without '..'", setting.Path)
	}
	return nil
}

// hostPath accepts an empty value (use the per-OS default) or a clean absolute path.
func hostPath(_ string, setting OperatorSetting) error {
	if setting.Value == "" || util.CleanAbsoluteLiteral(setting.Value, maxSettingValueBytes) {
		return nil
	}
	return fmt.Errorf("%s must be a clean absolute path", setting.Path)
}

// documentPath accepts an empty value, a clean absolute path, or a clean path relative to
// the file that sets it. Slashes are accepted on every OS.
func documentPath(_ string, setting OperatorSetting) error {
	native := filepath.FromSlash(setting.Value)
	if setting.Value == "" || filepath.Clean(native) == native {
		return nil
	}
	return fmt.Errorf("%s must be a clean relative or absolute path", setting.Path)
}

func scopeList(_ string, setting OperatorSetting) error {
	if len(setting.List) == 0 {
		return fmt.Errorf("%s requires at least one scope", setting.Path)
	}
	for i, scope := range setting.List {
		if scope != ScopeGlobal && scope != ScopeWorkspace || slices.Contains(setting.List[:i], scope) {
			return fmt.Errorf("%s accepts %s and %s, each once", setting.Path, ScopeGlobal, ScopeWorkspace)
		}
	}
	return nil
}

func grantList(_ string, setting OperatorSetting) error {
	for i, grant := range setting.List {
		if grant == "" || !util.LiteralString(grant, maxGrantBytes) {
			return fmt.Errorf("%s entry %d must be a literal of 1..%d bytes", setting.Path, i, maxGrantBytes)
		}
	}
	return nil
}

func denyList(_ string, setting OperatorSetting) error {
	if err := ValidateCommandPolicyDeny(setting.List); err != nil {
		return fmt.Errorf("%s: %w", setting.Path, err)
	}
	return nil
}

// pythonCandidates accepts up to eight interpreters, each a bare command name or, in the
// workstation layer only, a clean absolute path, followed by up to eight literal arguments.
func pythonCandidates(layer string, setting OperatorSetting) error {
	if len(setting.Argv) > maxPythonCandidates {
		return fmt.Errorf("%s accepts at most %d candidates", setting.Path, maxPythonCandidates)
	}
	for i, candidate := range setting.Argv {
		if err := pythonCandidate(layer, candidate); err != nil {
			return fmt.Errorf("%s candidate %d: %w", setting.Path, i, err)
		}
	}
	return nil
}

func pythonCandidate(layer string, candidate []string) error {
	if len(candidate) == 0 || len(candidate) > 1+maxPythonArgs {
		return fmt.Errorf("requires a command and at most %d arguments", maxPythonArgs)
	}
	command := candidate[0]
	absolute := util.CleanAbsoluteLiteral(command, maxSettingValueBytes)
	if !absolute && !bareCommand.MatchString(command) {
		return errors.New("command must be a bare name or a clean absolute path")
	}
	if absolute && layer != workstationLayer {
		return fmt.Errorf("an absolute interpreter path is host data; set it in the workstation layer, not %s", layer)
	}
	for _, arg := range candidate[1:] {
		if arg == "" || !util.LiteralString(arg, maxPythonArgBytes) {
			return fmt.Errorf("arguments must be literals of 1..%d bytes", maxPythonArgBytes)
		}
	}
	return nil
}

// optional accepts an empty value, which leaves the key unset, and applies check otherwise.
func optional(check settingCheck) settingCheck {
	return func(layer string, setting OperatorSetting) error {
		if setting.Value == "" {
			return nil
		}
		return check(layer, setting)
	}
}

// frameworkModule accepts an empty value or a module-path-shaped value of at most
// MaxFrameworkModuleBytes (IsModulePathShaped).
func frameworkModule(_ string, setting OperatorSetting) error {
	if setting.Value == "" {
		return nil
	}
	if len(setting.Value) > MaxFrameworkModuleBytes || !IsModulePathShaped(setting.Value) {
		return fmt.Errorf("%s must be a module path of at most %d bytes whose first element is a host, like example.com/acme/kit",
			setting.Path, MaxFrameworkModuleBytes)
	}
	return nil
}

// frameworkCheckout accepts a checkout for the go target only: the framework index is
// observed from a Go module checkout, and no other language has a source observer.
func frameworkCheckout(layer string, setting OperatorSetting) error {
	if _, language, _ := settingWildcard(setting.Path); setting.Value != "" && language != "go" {
		return fmt.Errorf("%s: a framework checkout is accepted for the go target only", setting.Path)
	}
	return hostPath(layer, setting)
}

// repositoryList accepts <owner>/<name> coordinates (util.SplitGitHubRepository), each once.
func repositoryList(_ string, setting OperatorSetting) error {
	for i, coordinate := range setting.List {
		if _, _, err := util.SplitGitHubRepository(coordinate); err != nil {
			return fmt.Errorf("%s entry %d: %w", setting.Path, i, err)
		}
		if slices.Contains(setting.List[:i], coordinate) {
			return fmt.Errorf("%s entry %d repeats %s", setting.Path, i, coordinate)
		}
	}
	return nil
}

func githubOwner(_ string, setting OperatorSetting) error {
	if err := util.ValidateGitHubOwner(setting.Value); err != nil {
		return fmt.Errorf("%s: %w", setting.Path, err)
	}
	return nil
}

// reviewBot accepts a GitHub owner, optionally followed by the [bot] suffix of an app account.
func reviewBot(_ string, setting OperatorSetting) error {
	if err := util.ValidateGitHubOwner(strings.TrimSuffix(setting.Value, "[bot]")); err != nil {
		return fmt.Errorf("%s must be a GitHub account, optionally with a [bot] suffix: %w", setting.Path, err)
	}
	return nil
}

// orgContainerList accepts folder names of 1..64 lowercase letters, digits, '.', '_' or '-'.
func orgContainerList(_ string, setting OperatorSetting) error {
	for i, name := range setting.List {
		if !orgContainerName.MatchString(name) {
			return fmt.Errorf("%s entry %d must be 1..64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit",
				setting.Path, i)
		}
	}
	return nil
}
