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
	kindEntry                      // clients.selected.<id>: lists the client, holds its keys
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

// operatorSpecs is the schema, keyed by dotted path; `*` stands for a client identifier.
// A key that is not listed here is an error naming the key.
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
)

const selectedPrefix = "clients.selected."

// genericSettingPath replaces the client identifier of a clients.selected path with `*`.
func genericSettingPath(path string) string {
	rest, ok := strings.CutPrefix(path, selectedPrefix)
	if !ok {
		return path
	}
	_, tail, nested := strings.Cut(rest, ".")
	if !nested {
		return selectedPrefix + "*"
	}
	return selectedPrefix + "*." + tail
}

// settingClient returns the client identifier of a clients.selected path, or "".
func settingClient(path string) clientid.ID {
	rest, ok := strings.CutPrefix(path, selectedPrefix)
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, ".")
	return clientid.ID(id)
}

// settingSpecFor looks a concrete path up in the schema. An unknown key and an unknown
// client are errors that name what was written.
func settingSpecFor(path string) (settingSpec, error) {
	generic := genericSettingPath(path)
	spec, ok := operatorSpecs[generic]
	if !ok {
		return spec, fmt.Errorf("unknown setting %q", path)
	}
	if client := settingClient(path); client != "" {
		if _, err := clientid.Parse(string(client)); err != nil {
			return spec, fmt.Errorf("clients.selected: %w", err)
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
