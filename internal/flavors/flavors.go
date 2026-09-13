package flavors

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

// Flavor defines a release track and its tag convention.
type Flavor struct {
	Description     string `yaml:"description"`
	SourceRef       string `yaml:"source_ref"`
	TagPattern      string `yaml:"tag_pattern"`
	UpdateFrequency string `yaml:"update_frequency"`
	Stability       string `yaml:"stability"`
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
	// Action is one of ActionCreate, ActionUpdate, ActionNoop or ActionUnresolved.
	Action string
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
// silent fallback to HEAD.
func PlanTransitions(cfg *Config, currentTags map[string]string, resolve RefResolver) []TagTransition {
	names := FlavorNames(cfg)
	transitions := make([]TagTransition, 0, len(names))

	for i := 0; i < len(names) && i < MaxFlavors; i++ {
		name := names[i]
		sourceRef := SourceRefFor(cfg.Flavors[name])

		var targetCommit string
		var resolved bool
		if resolve != nil {
			targetCommit, resolved = resolve(sourceRef)
		}

		transitions = append(transitions, TagTransition{
			FlavorName:   name,
			CurrentRef:   currentTags[name],
			TargetRef:    sourceRef,
			TargetCommit: targetCommit,
			Action:       classifyAction(currentTags[name], targetCommit, resolved),
		})
	}

	return transitions
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
