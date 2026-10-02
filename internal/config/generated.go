// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Bounds and defaults of the generated section (HISS-02, ADR-0017).
const (
	// MaxGeneratedArtefacts bounds the artefacts one manifest declares.
	MaxGeneratedArtefacts = 64
	// MaxGeneratedGlobs bounds the paths and the sources of one artefact.
	MaxGeneratedGlobs = 32
	// MaxGeneratedCommandArgs bounds the argument vector of one render command.
	MaxGeneratedCommandArgs = 32
	// MaxGeneratedArgBytes bounds one argument and one environment value.
	MaxGeneratedArgBytes = 1024
	// MaxGeneratedEnv bounds the environment entries one artefact sets.
	MaxGeneratedEnv = 16
	// MaxGeneratedDecline bounds the built-in artefacts one manifest declines.
	MaxGeneratedDecline = 32
	// MaxGeneratedMarkerBytes bounds a block marker, the branch prefix and the title type.
	MaxGeneratedMarkerBytes = 128
	// DefaultGeneratedTimeout bounds one render command that declares no timeout.
	DefaultGeneratedTimeout = 10 * time.Minute
	// MaxGeneratedTimeout is the longest timeout an artefact may declare.
	MaxGeneratedTimeout = 30 * time.Minute
	// DefaultRegenerationBranchPrefix is the branch prefix of a regeneration change when the
	// manifest declares none.
	DefaultRegenerationBranchPrefix = "regen/"
	// DefaultRegenerationTitleType is the conventional title type of a regeneration change when
	// the manifest declares none.
	DefaultRegenerationTitleType = "chore(generated)"
)

var (
	// generatedEnvName is the shape of an environment variable name an artefact may set.
	generatedEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// generatedTitleType is a conventional type with an optional scope, such as chore(generated).
	generatedTitleType = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}(\([a-z0-9][a-z0-9._/-]{0,63}\))?$`)
)

// GeneratedPolicy is the generated section of the manifest (ADR-0017): the files the
// repository renders from other files, the command that renders each and the sources it reads,
// and the marker that recognises the one change allowed to edit them. Praetor's own generated
// artefacts are built in (internal/generated); Decline leaves one of them out and Artefacts
// adds the repository's own. It is repository-only and stays out of ResolvedPolicy.
type GeneratedPolicy struct {
	Regeneration *RegenerationMarker `yaml:"regeneration,omitempty"`
	Decline      []string            `yaml:"decline,omitempty"`
	Artefacts    []GeneratedArtefact `yaml:"artefacts,omitempty"`
}

// RegenerationMarker recognises a regeneration change: its branch name starts with
// BranchPrefix and its title carries the conventional type TitleType. Both must match.
type RegenerationMarker struct {
	BranchPrefix string `yaml:"branch_prefix,omitempty"`
	TitleType    string `yaml:"title_type,omitempty"`
}

// GeneratedArtefact declares one generated artefact. Paths selects its files; with Block it is
// only the region between the block's marker lines inside each of them, so a hand-edited file
// that carries a generated block stays editable outside it. Command renders the artefact from
// the repository root, with Env added to the environment and within Timeout; a first word
// "praetorctl" runs the running praetorctl binary. Sources lists the globs it is rendered from.
type GeneratedArtefact struct {
	Name    string            `yaml:"name"`
	Paths   []string          `yaml:"paths"`
	Block   *GeneratedBlock   `yaml:"block,omitempty"`
	Command []string          `yaml:"command"`
	Env     map[string]string `yaml:"env,omitempty"`
	Sources []string          `yaml:"sources"`
	Timeout string            `yaml:"timeout,omitempty"`
}

// GeneratedBlock names the two whole lines that open and close a generated region.
type GeneratedBlock struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
}

// Marker returns the regeneration marker, each part the default where the manifest sets none.
// A nil policy returns both defaults.
func (p *GeneratedPolicy) Marker() RegenerationMarker {
	marker := RegenerationMarker{BranchPrefix: DefaultRegenerationBranchPrefix, TitleType: DefaultRegenerationTitleType}
	if p == nil || p.Regeneration == nil {
		return marker
	}
	if p.Regeneration.BranchPrefix != "" {
		marker.BranchPrefix = p.Regeneration.BranchPrefix
	}
	if p.Regeneration.TitleType != "" {
		marker.TitleType = p.Regeneration.TitleType
	}
	return marker
}

// RenderTimeout returns the artefact's declared timeout, or DefaultGeneratedTimeout when it
// declares none. ValidateGenerated has already refused a timeout that does not parse.
func (a GeneratedArtefact) RenderTimeout() time.Duration {
	if a.Timeout == "" {
		return DefaultGeneratedTimeout
	}
	parsed, err := time.ParseDuration(a.Timeout)
	if err != nil || parsed <= 0 || parsed > MaxGeneratedTimeout {
		return DefaultGeneratedTimeout
	}
	return parsed
}

// EnvList returns Env as sorted NAME=value entries, so two runs hand a command the same list.
func (a GeneratedArtefact) EnvList() []string {
	names := make([]string, 0, len(a.Env))
	for name := range a.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+a.Env[name])
	}
	return entries
}

// ValidateGenerated refuses a generated section the generated-artefact commands could not apply
// as written: an invalid marker, too many entries, a repeated or malformed name, an artefact
// without paths, sources or command, a glob that is not repository-relative, a malformed block,
// environment entry or timeout. A nil policy is valid.
func ValidateGenerated(p *GeneratedPolicy) error {
	if p == nil {
		return nil
	}
	if err := p.Regeneration.validate(); err != nil {
		return err
	}
	if err := validateGeneratedDecline(p.Decline); err != nil {
		return err
	}
	if len(p.Artefacts) > MaxGeneratedArtefacts {
		return fmt.Errorf("generated.artefacts has %d entries; maximum is %d", len(p.Artefacts), MaxGeneratedArtefacts)
	}
	names := make(map[string]int, len(p.Artefacts))
	for index := 0; index < len(p.Artefacts) && index < MaxGeneratedArtefacts; index++ {
		artefact := p.Artefacts[index]
		if err := artefact.validate(fmt.Sprintf("generated.artefacts[%d]", index)); err != nil {
			return err
		}
		if first, repeated := names[artefact.Name]; repeated {
			return fmt.Errorf("generated.artefacts[%d] repeats the name %q of generated.artefacts[%d]", index, artefact.Name, first)
		}
		names[artefact.Name] = index
	}
	return nil
}

// validate checks the marker; nil keeps both defaults.
func (m *RegenerationMarker) validate() error {
	if m == nil {
		return nil
	}
	if m.BranchPrefix != "" && (len(m.BranchPrefix) > MaxGeneratedMarkerBytes || !ValidBranchName(m.BranchPrefix)) {
		return fmt.Errorf("generated.regeneration.branch_prefix %q must be %s", m.BranchPrefix, branchNameRule)
	}
	if m.TitleType != "" && !generatedTitleType.MatchString(m.TitleType) {
		return fmt.Errorf("generated.regeneration.title_type %q must be a conventional type with an optional scope, such as %q",
			m.TitleType, DefaultRegenerationTitleType)
	}
	return nil
}

// validateGeneratedDecline checks the declined names' shape; which names exist is
// internal/generated's to say, since it owns the built-in list.
func validateGeneratedDecline(names []string) error {
	if len(names) > MaxGeneratedDecline {
		return fmt.Errorf("generated.decline has %d names; maximum is %d", len(names), MaxGeneratedDecline)
	}
	for index := 0; index < len(names) && index < MaxGeneratedDecline; index++ {
		if problem := docsSurfaceNameProblem(names[index]); problem != "" {
			return fmt.Errorf("generated.decline[%d] %s", index, problem)
		}
	}
	return nil
}

// validate checks one artefact; prefix places it in the error.
func (a GeneratedArtefact) validate(prefix string) error {
	if problem := docsSurfaceNameProblem(a.Name); problem != "" {
		return fmt.Errorf("%s.name %s", prefix, problem)
	}
	if err := validateGeneratedGlobs(prefix+".paths", a.Paths); err != nil {
		return err
	}
	if err := validateGeneratedGlobs(prefix+".sources", a.Sources); err != nil {
		return err
	}
	if err := validateGeneratedCommand(prefix+".command", a.Command); err != nil {
		return err
	}
	if err := a.Block.validate(prefix + ".block"); err != nil {
		return err
	}
	if err := validateGeneratedEnv(prefix+".env", a.Env); err != nil {
		return err
	}
	return validateGeneratedTimeout(prefix+".timeout", a.Timeout)
}

// validateGeneratedGlobs requires one to MaxGeneratedGlobs repository-relative globs under the
// StyleExclusionProblem rules the docs_surfaces globs follow.
func validateGeneratedGlobs(key string, globs []string) error {
	if len(globs) == 0 {
		return fmt.Errorf("%s must list at least one glob", key)
	}
	if len(globs) > MaxGeneratedGlobs {
		return fmt.Errorf("%s has %d globs; maximum is %d", key, len(globs), MaxGeneratedGlobs)
	}
	for index := 0; index < len(globs) && index < MaxGeneratedGlobs; index++ {
		if problem := StyleExclusionProblem(globs[index]); problem != "" {
			return fmt.Errorf("%s[%d] %s", key, index, problem)
		}
	}
	return nil
}

// validateGeneratedCommand requires an argument vector of one to MaxGeneratedCommandArgs
// arguments, each one line of at most MaxGeneratedArgBytes, the first one non-empty.
func validateGeneratedCommand(key string, command []string) error {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("%s must name the command to run as its first word", key)
	}
	if len(command) > MaxGeneratedCommandArgs {
		return fmt.Errorf("%s has %d words; maximum is %d", key, len(command), MaxGeneratedCommandArgs)
	}
	for index := 0; index < len(command) && index < MaxGeneratedCommandArgs; index++ {
		if problem := generatedValueProblem(command[index]); problem != "" {
			return fmt.Errorf("%s[%d] %s", key, index, problem)
		}
	}
	return nil
}

// generatedValueProblem names why one argument or environment value is refused, or returns "".
func generatedValueProblem(value string) string {
	switch {
	case len(value) > MaxGeneratedArgBytes:
		return fmt.Sprintf("exceeds %d bytes", MaxGeneratedArgBytes)
	case strings.ContainsFunc(value, unicode.IsControl):
		return "must not contain a control character"
	}
	return ""
}

// validate checks a block: two distinct single-line markers that are not blank.
func (b *GeneratedBlock) validate(key string) error {
	if b == nil {
		return nil
	}
	for _, marker := range []struct{ name, value string }{{"start", b.Start}, {"end", b.End}} {
		if strings.TrimSpace(marker.value) == "" || marker.value != strings.TrimSpace(marker.value) {
			return fmt.Errorf("%s.%s must be a non-empty line without surrounding whitespace", key, marker.name)
		}
		if len(marker.value) > MaxGeneratedMarkerBytes || strings.ContainsFunc(marker.value, unicode.IsControl) {
			return fmt.Errorf("%s.%s must be one line of at most %d bytes", key, marker.name, MaxGeneratedMarkerBytes)
		}
	}
	if b.Start == b.End {
		return fmt.Errorf("%s.start and %s.end must differ", key, key)
	}
	return nil
}

// validateGeneratedEnv checks the environment entries' count, names and values.
func validateGeneratedEnv(key string, env map[string]string) error {
	if len(env) > MaxGeneratedEnv {
		return fmt.Errorf("%s has %d entries; maximum is %d", key, len(env), MaxGeneratedEnv)
	}
	for name, value := range env {
		if !generatedEnvName.MatchString(name) {
			return fmt.Errorf("%s name %q must match %s", key, name, generatedEnvName)
		}
		if problem := generatedValueProblem(value); problem != "" {
			return fmt.Errorf("%s.%s %s", key, name, problem)
		}
	}
	return nil
}

// validateGeneratedTimeout accepts an empty timeout or a duration in (0, MaxGeneratedTimeout].
func validateGeneratedTimeout(key, timeout string) error {
	if timeout == "" {
		return nil
	}
	parsed, err := time.ParseDuration(timeout)
	if err != nil {
		return fmt.Errorf("%s %q is not a duration: %w", key, timeout, err)
	}
	if parsed <= 0 || parsed > MaxGeneratedTimeout {
		return fmt.Errorf("%s %s must be positive and at most %s", key, parsed, MaxGeneratedTimeout)
	}
	return nil
}
