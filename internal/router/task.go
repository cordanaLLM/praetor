package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// MaxTaskTokens bounds each supplied token estimate; estimates do not claim a
// model context-window limit or reserve quota.
const MaxTaskTokens = 1_000_000_000

// ErrNoEligibleModel means no explicitly declared candidate met task, capability
// and available recorded-capacity constraints. It never triggers a silent downgrade.
var ErrNoEligibleModel = errors.New("no eligible model for declared task and capabilities")

// TaskRequest states an explicit task class and optional token estimates for cost and quota.
// Both estimates zero asks for a tier-only route: candidates then rank by configured rates.
type TaskRequest struct {
	Task                    string   `json:"task"`
	Capabilities            []string `json:"capabilities,omitempty"`
	InputTokens             int64    `json:"input_tokens"`
	OutputTokens            int64    `json:"output_tokens"`
	RequireObservedCapacity bool     `json:"require_observed_capacity"`
}

// TaskRoute is a deterministic selection, not a provider request or reservation.
// EstimatedCost uses configured rates, not measured latency, quality or pricing.
type TaskRoute struct {
	Request           TaskRequest     `json:"request"`
	Tier              string          `json:"tier"`
	Model             ModelDescriptor `json:"model"`
	EstimatedCost     float64         `json:"estimated_cost"`
	CapacityObserved  bool            `json:"capacity_observed"`
	RecordedHeadroom  *float64        `json:"recorded_headroom,omitempty"`
	ProjectedHeadroom *float64        `json:"projected_headroom,omitempty"`
	QuotaLimitsKnown  bool            `json:"quota_limits_known"`
	Basis             string          `json:"basis"`
	// Lane is the executable lane of the pick; LaneNote says why there is none.
	Lane     *LaneRoute `json:"lane,omitempty"`
	LaneNote string     `json:"lane_note,omitempty"`
	// Skipped lists candidates of the task's tiers the gateway rules excluded, with the reason.
	Skipped []RouteSkip `json:"skipped,omitempty"`
	// rank orders candidates: the estimated cost, or the summed per-million rates without estimates.
	rank float64
}

// RouteSkip is one candidate excluded from a route and why.
type RouteSkip struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// admit reports whether the gateway rules keep a candidate; an excluded one is recorded.
func (p *taskPass) admit(model ModelDescriptor) bool {
	reason := gatewayExclusion(model, p.serves)
	if reason == "" {
		return true
	}
	if len(p.skips) < MaxRoutingModels {
		p.skips = append(p.skips, RouteSkip{Model: model.ID, Reason: reason})
	}
	return false
}

// maxErrorSkips bounds the skip reasons quoted in a no-eligible-model error.
const maxErrorSkips = 8

// taskPass is the state of one selection: the request, whether a reservation filters
// candidates, whether the gateway serves answering aliases, and the candidates skipped.
type taskPass struct {
	request   TaskRequest
	reserving bool
	serves    bool
	skips     []RouteSkip
}

// SelectForTask considers only tiers declaring the exact task and models declaring
// every requested capability. Eligible candidates sort by configured total cost,
// then model ID, then tier name. It preserves RPM/TPM and 429 cooldown checks.
func (a *ModelCapacityArbiter) SelectForTask(ctx context.Context, request TaskRequest) (*TaskRoute, error) {
	if err := a.validateTaskRoute(ctx, request); err != nil {
		return nil, err
	}
	a.Tracker.mu.RLock()
	defer a.Tracker.mu.RUnlock()
	return a.selectTaskLocked(ctx, request, false)
}

// selectTaskLocked requires the tracker's lock; reserving also filters active slots.
func (a *ModelCapacityArbiter) selectTaskLocked(ctx context.Context, request TaskRequest, reserving bool) (*TaskRoute, error) {
	names := make([]string, 0, len(a.Config.Tiers))
	for name := range a.Config.Tiers {
		names = append(names, name)
	}
	sort.Strings(names)
	pass := &taskPass{request: request, reserving: reserving, serves: gatewayServesAliases(a.Config)}
	var best *TaskRoute
	for i := 0; i < len(names) && i < MaxRoutingTiers; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := a.selectTaskTier(pass, names[i])
		if err != nil {
			return nil, err
		}
		if betterTaskRoute(candidate, best) {
			best = candidate
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: %s%s", ErrNoEligibleModel, request.Task, describeSkips(pass.skips))
	}
	best.Skipped = pass.skips
	if lane, ok := ResolveLane(a.Config, best.Tier, best.Model, request.Task); ok {
		best.Lane = lane
	} else {
		best.LaneNote = "no lane declared for the model or its tier " + best.Tier
	}
	return best, nil
}

func describeSkips(skips []RouteSkip) string {
	if len(skips) == 0 {
		return ""
	}
	parts := make([]string, 0, maxErrorSkips)
	for i := 0; i < len(skips) && i < maxErrorSkips; i++ {
		parts = append(parts, skips[i].Model+": "+skips[i].Reason)
	}
	return " (skipped: " + strings.Join(parts, "; ") + ")"
}

func (a *ModelCapacityArbiter) validateTaskRoute(ctx context.Context, request TaskRequest) error {
	if ctx == nil {
		return errors.New("task routing requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.Tracker == nil {
		return errors.New("task router and limit tracker are required")
	}
	if err := ValidateRoutingConfig(a.Config); err != nil {
		return err
	}
	if err := validateTaskRequest(request); err != nil {
		return err
	}
	return nil
}

func validateTaskRequest(request TaskRequest) error {
	if !routingName(request.Task) {
		return errors.New("task must be an explicit nonempty task label")
	}
	if request.InputTokens < 0 || request.InputTokens > MaxTaskTokens || request.OutputTokens < 0 || request.OutputTokens > MaxTaskTokens {
		return fmt.Errorf("token estimates must be between 0 and %d", MaxTaskTokens)
	}
	return validateRoutingTags(request.Capabilities)
}

func (a *ModelCapacityArbiter) selectTaskTier(pass *taskPass, name string) (*TaskRoute, error) {
	request, reserving := pass.request, pass.reserving
	tier := a.Config.Tiers[name]
	if !hasRoutingTag(tier.TargetTasks, request.Task) {
		return nil, nil
	}
	var best *TaskRoute
	for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
		model := tier.Models[i]
		if !hasTaskCapabilities(model.Capabilities, request.Capabilities) {
			continue
		}
		if !pass.admit(model) {
			continue
		}
		if reserving && !a.reservationAvailableLocked(model.ID) {
			continue
		}
		candidate, err := a.taskCandidate(request, name, model)
		if err != nil {
			return nil, err
		}
		if betterTaskRoute(candidate, best) {
			best = candidate
		}
	}
	return best, nil
}

func (a *ModelCapacityArbiter) taskCandidate(request TaskRequest, tier string, model ModelDescriptor) (*TaskRoute, error) {
	capacity, err := a.projectTaskCapacity(request, model)
	if err != nil || capacity == nil {
		return nil, err
	}
	cost := model.CostPerMIn*(float64(request.InputTokens)/1_000_000) + model.CostPerMOut*(float64(request.OutputTokens)/1_000_000)
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		return nil, errors.New("configured token cost overflows")
	}
	if cost > math.MaxFloat64-capacity.recorded.TotalSpend {
		return nil, nil
	}
	return newTaskRoute(request, tier, model, capacity, cost), nil
}

func newTaskRoute(request TaskRequest, tier string, model ModelDescriptor, capacity *taskCapacity, cost float64) *TaskRoute {
	knownLimits := model.RPMLimit > 0 && model.TPMLimit > 0
	request.Capabilities = append([]string(nil), request.Capabilities...)
	model.Capabilities = append([]string(nil), model.Capabilities...)
	rank := cost
	tierOnly := request.InputTokens+request.OutputTokens == 0
	if tierOnly {
		rank = model.CostPerMIn + model.CostPerMOut
	}
	result := &TaskRoute{Request: request, rank: rank, Tier: tier, Model: model, EstimatedCost: cost, CapacityObserved: capacity.observed,
		QuotaLimitsKnown: knownLimits,
		Basis:            "lowest configured token cost among explicitly eligible candidates after projected request checks; unknown counters or limits remain provisional; no live availability, quota reservation or dispatch"}
	if tierOnly {
		result.Basis += "; no token estimates, so candidates rank by configured per-million rates"
	}
	if capacity.observed {
		headroom := usageHeadroom(capacity.recorded, model)
		result.RecordedHeadroom = &headroom
	}
	if knownLimits {
		headroom := usageHeadroom(capacity.projected, model)
		result.ProjectedHeadroom = &headroom
	}
	return result
}

func hasRoutingTag(tags []string, wanted string) bool {
	for i := 0; i < len(tags) && i < MaxRoutingTags; i++ {
		if tags[i] == wanted {
			return true
		}
	}
	return false
}

func hasTaskCapabilities(declared, required []string) bool {
	for i := 0; i < len(required) && i < MaxRoutingTags; i++ {
		if !hasRoutingTag(declared, required[i]) {
			return false
		}
	}
	return true
}

func betterTaskRoute(candidate, best *TaskRoute) bool {
	if candidate == nil {
		return false
	}
	if best == nil {
		return true
	}
	if candidate.rank != best.rank {
		return candidate.rank < best.rank
	}
	if candidate.Model.ID != best.Model.ID {
		return candidate.Model.ID < best.Model.ID
	}
	return candidate.Tier < best.Tier
}
