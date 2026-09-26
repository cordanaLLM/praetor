package flavors

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

// Flavor defines a release track and its tag convention.
type Flavor struct {
	Description string `yaml:"description"`
	SourceRef   string `yaml:"source_ref"`
	TagPattern  string `yaml:"tag_pattern"`
	// UpdateFrequency decides whether a sync moves the flavor's tag on its own: one of the
	// Frequency constants, or empty for automatic.
	UpdateFrequency string `yaml:"update_frequency"`
	// Stability is the flavor's declared stability label; the plan reports it beside the tag.
	Stability string `yaml:"stability"`
}

// Update frequencies a flavor may declare. on_push, on_release and on_patch name the event
// that moves the flavor's source ref, and every sync follows the source automatically (the
// sync workflow runs on each push to main and on a schedule). manual holds the tag until an
// operator names the flavor for one sync. An undeclared frequency is automatic, which is how
// every flavor behaved before the field was read.
const (
	FrequencyOnPush    = "on_push"
	FrequencyOnRelease = "on_release"
	FrequencyOnPatch   = "on_patch"
	FrequencyManual    = "manual"
)

// IsManual reports whether the flavor's tag moves only when an operator names it.
func (f Flavor) IsManual() bool {
	return strings.TrimSpace(f.UpdateFrequency) == FrequencyManual
}

// validateFrequency refuses a frequency no sync understands. Reading an unknown value as
// automatic would move a tag its author meant to hold, and reading it as manual would
// freeze one they meant to follow; either silent guess is the defect a declared field
// exists to prevent.
func validateFrequency(name string, f Flavor) error {
	switch strings.TrimSpace(f.UpdateFrequency) {
	case "", FrequencyOnPush, FrequencyOnRelease, FrequencyOnPatch, FrequencyManual:
		return nil
	default:
		return fmt.Errorf("flavor %q: unknown update_frequency %q (supported: %s, %s, %s, %s)",
			name, f.UpdateFrequency, FrequencyOnPush, FrequencyOnRelease, FrequencyOnPatch, FrequencyManual)
	}
}

// Config represents .config/flavors.yaml.
type Config struct {
	Version int               `yaml:"version"`
	Flavors map[string]Flavor `yaml:"flavors"`
}

// TagTransition represents a proposed or applied moving tag update.
type TagTransition struct {
	FlavorName string
	// CurrentRef is the commit the flavor tag currently points at, empty when the tag
	// does not exist yet.
	CurrentRef string
	// TargetRef is the git ref (or ref pattern) declared as the flavor's source.
	TargetRef string
	// TargetCommit is the commit TargetRef resolves to, empty when it does not resolve.
	TargetCommit string
	// Action is one of ActionCreate, ActionUpdate, ActionNoop, ActionUnresolved or ActionHeld.
	Action string
	// UpdateFrequency and Stability carry the flavor's declared values into the report.
	UpdateFrequency string
	Stability       string
}

// Transition actions produced by PlanTransitions.
const (
	// ActionCreate means the flavor tag does not exist yet.
	ActionCreate = "create"
	// ActionUpdate means the flavor tag exists but points at a different commit.
	ActionUpdate = "update"
	// ActionNoop means the flavor tag already points at the target commit.
	ActionNoop = "noop"
	// ActionUnresolved means the declared source ref resolves to no commit. The moving
	// tag must not be retargeted in that case: force-moving latest or lts onto whatever
	// happens to be checked out silently destroys a release pointer.
	ActionUnresolved = "unresolved"
	// ActionHeld means the flavor declares update_frequency manual and this plan did not
	// name it, so its tag stays where it is whatever its source resolves to.
	ActionHeld = "held"
)

// MaxFlavors is the scalar upper bound (HISS-02) on the number of flavors a single plan
// considers. A configuration declaring more is truncated deterministically.
const MaxFlavors = 64

// RefResolver resolves a git ref or ref pattern to a commit SHA. ok is false when the ref
// does not exist in the repository.
type RefResolver func(ref string) (commit string, ok bool)

// SourceRefFor returns the git ref whose commit the flavor's moving tag must point at.
// A flavor that declares no source_ref tracks the checked-out commit.
func SourceRefFor(f Flavor) string {
	if ref := strings.TrimSpace(f.SourceRef); ref != "" {
		return ref
	}
	return "HEAD"
}

// LoadConfig reads and parses .config/flavors.yaml.
func LoadConfig(path string) (*Config, error) {
	return LoadConfigContext(context.Background(), path)
}

// LoadConfigContext reads bounded configuration without following symbolic links.
func LoadConfigContext(ctx context.Context, path string) (*Config, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read flavors config at %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse flavors config at %s: %w", path, err)
	}
	names := FlavorNames(&cfg)
	for i := 0; i < len(names) && i < MaxFlavors; i++ {
		if err := validateFrequency(names[i], cfg.Flavors[names[i]]); err != nil {
			return nil, fmt.Errorf("invalid flavors config at %s: %w", path, err)
		}
	}

	return &cfg, nil
}

// FlavorNames returns the declared flavor names in deterministic order, bounded by
// MaxFlavors.
func FlavorNames(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Flavors))
	for name := range cfg.Flavors {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > MaxFlavors {
		names = names[:MaxFlavors]
	}
	return names
}

// PlanTransitions computes moving tag updates for all declared flavors.
//
// currentTags maps a flavor name to the commit its tag currently points at; a flavor
// absent from the map has no tag yet. resolve turns the flavor's declared source ref into
// a commit, so the noop comparison is commit against commit rather than name against
// version string. A source ref that does not resolve yields ActionUnresolved instead of a
// silent fallback to HEAD. Every manual flavor is held; PlanSelected moves one by name.
func PlanTransitions(cfg *Config, currentTags map[string]string, resolve RefResolver) []TagTransition {
	return PlanSelected(cfg, currentTags, resolve, nil)
}

// PlanSelected is PlanTransitions restricted to the named flavors. A nil or empty selection
// plans every declared flavor, holding the manual ones; a selection plans only the flavors it
// names, and naming a manual flavor is what lets it move. Names are checked by
// UnknownFlavors before a plan is made.
func PlanSelected(cfg *Config, currentTags map[string]string, resolve RefResolver, selected []string) []TagTransition {
	names := FlavorNames(cfg)
	transitions := make([]TagTransition, 0, len(names))

	for i := 0; i < len(names) && i < MaxFlavors; i++ {
		name := names[i]
		named := slices.Contains(selected, name)
		if len(selected) > 0 && !named {
			continue
		}
		flavor := cfg.Flavors[name]
		sourceRef := SourceRefFor(flavor)

		var targetCommit string
		var resolved bool
		if resolve != nil {
			targetCommit, resolved = resolve(sourceRef)
		}

		action := classifyAction(currentTags[name], targetCommit, resolved)
		if flavor.IsManual() && !named {
			action = ActionHeld
		}
		transitions = append(transitions, TagTransition{
			FlavorName:      name,
			CurrentRef:      currentTags[name],
			TargetRef:       sourceRef,
			TargetCommit:    targetCommit,
			Action:          action,
			UpdateFrequency: strings.TrimSpace(flavor.UpdateFrequency),
			Stability:       strings.TrimSpace(flavor.Stability),
		})
	}

	return transitions
}

// UnknownFlavors returns the names in selected that the configuration does not declare, so
// a misspelled --flavor fails instead of planning nothing.
func UnknownFlavors(cfg *Config, selected []string) []string {
	var declared map[string]Flavor
	if cfg != nil {
		declared = cfg.Flavors
	}
	var unknown []string
	for i := 0; i < len(selected) && i < MaxFlavors; i++ {
		if _, ok := declared[selected[i]]; !ok {
			unknown = append(unknown, selected[i])
		}
	}
	return unknown
}

func classifyAction(current, targetCommit string, resolved bool) string {
	switch {
	case !resolved || targetCommit == "":
		return ActionUnresolved
	case current == "":
		return ActionCreate
	case current == targetCommit:
		return ActionNoop
	default:
		return ActionUpdate
	}
}
