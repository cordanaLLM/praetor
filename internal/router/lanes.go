package router

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	// MaxLanes bounds the lane table.
	MaxLanes = 32
	// MaxLaneArgs bounds one lane command's argument vector.
	MaxLaneArgs = 64
	// maxLaneArgBytes bounds one argument of a lane command.
	maxLaneArgBytes = 1024
)

// Lane command placeholders: TargetPlaceholder expands to the alias of an alias entry or
// the model ID of a pinned one, TaskPlaceholder to the task label.
const (
	TargetPlaceholder = "{target}"
	TaskPlaceholder   = "{task}"
)

// placeholderShape matches every {lower_snake} token, so an unknown one is refused instead of
// being passed to a harness as literal text.
var placeholderShape = regexp.MustCompile(`\{[a-z_]+\}`)

// Lane is one execution lane: the harness that runs a task and its headless invocation.
// Command is an argument vector, never a shell line, so a label or alias cannot inject syntax.
type Lane struct {
	Harness string   `yaml:"harness" json:"harness"`
	Command []string `yaml:"command" json:"command"`
}

// LaneRoute is the executable lane of a route: the lane name, its harness, the target the
// harness is told to use and the exact argument vector.
type LaneRoute struct {
	Name    string   `json:"name"`
	Harness string   `json:"harness"`
	Target  string   `json:"target"`
	Command []string `json:"command"`
}

func validateLanes(cfg *RoutingConfig) error {
	if len(cfg.Lanes) > MaxLanes {
		return fmt.Errorf("%w: more than %d lanes", ErrInvalidRoutingConfig, MaxLanes)
	}
	for name, lane := range cfg.Lanes {
		if !routingName(name) || !routingName(lane.Harness) {
			return fmt.Errorf("%w: invalid lane or harness name %q", ErrInvalidRoutingConfig, name)
		}
		if err := validateLaneCommand(name, lane.Command); err != nil {
			return err
		}
	}
	return validateLaneRefs(cfg)
}

func validateLaneCommand(name string, command []string) error {
	if len(command) == 0 || len(command) > MaxLaneArgs {
		return fmt.Errorf("%w: lane %s needs 1..%d command arguments", ErrInvalidRoutingConfig, name, MaxLaneArgs)
	}
	for i := 0; i < len(command) && i < MaxLaneArgs; i++ {
		if err := validateLaneArg(name, i, command[i]); err != nil {
			return err
		}
	}
	return nil
}

func validateLaneArg(name string, index int, arg string) error {
	if arg == "" || len(arg) > maxLaneArgBytes || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: lane %s argument %d is empty, oversized or has control characters", ErrInvalidRoutingConfig, name, index)
	}
	for _, token := range placeholderShape.FindAllString(arg, MaxLaneArgs) {
		if token != TargetPlaceholder && token != TaskPlaceholder {
			return fmt.Errorf("%w: lane %s uses unknown placeholder %s", ErrInvalidRoutingConfig, name, token)
		}
	}
	return nil
}

// validateLaneRefs refuses a tier or model naming a lane the table does not declare.
func validateLaneRefs(cfg *RoutingConfig) error {
	for tierName, tier := range cfg.Tiers {
		if err := checkLaneRef(cfg, tierName, tier.Lane); err != nil {
			return err
		}
		for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
			if err := checkLaneRef(cfg, tier.Models[i].ID, tier.Models[i].Lane); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkLaneRef(cfg *RoutingConfig, owner, lane string) error {
	if lane == "" {
		return nil
	}
	if _, ok := cfg.Lanes[lane]; !ok {
		return fmt.Errorf("%w: %s names undeclared lane %s", ErrInvalidRoutingConfig, owner, lane)
	}
	return nil
}

// ModelTarget is what a harness is told to call: the alias of an alias entry, else the model ID.
func ModelTarget(model ModelDescriptor) string {
	if model.Alias != "" {
		return model.Alias
	}
	return model.ID
}

// ResolveLane returns the lane of a selected model: the model's own lane, else its tier's.
// ok is false when neither declares one, so a route reports the gap instead of inventing a lane.
func ResolveLane(cfg *RoutingConfig, tier string, model ModelDescriptor, task string) (*LaneRoute, bool) {
	name := model.Lane
	if name == "" {
		name = cfg.Tiers[tier].Lane
	}
	lane, ok := cfg.Lanes[name]
	if name == "" || !ok {
		return nil, false
	}
	target := ModelTarget(model)
	command := make([]string, 0, len(lane.Command))
	for i := 0; i < len(lane.Command) && i < MaxLaneArgs; i++ {
		arg := strings.ReplaceAll(lane.Command[i], TargetPlaceholder, target)
		command = append(command, strings.ReplaceAll(arg, TaskPlaceholder, task))
	}
	return &LaneRoute{Name: name, Harness: lane.Harness, Target: target, Command: command}, true
}
