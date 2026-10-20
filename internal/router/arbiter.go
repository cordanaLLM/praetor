package router

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

const CooldownDuration = 30 * time.Second

// ModelCapacityArbiter uses operator configuration and process-local counters.
// Keep Config immutable while the arbiter is shared between concurrent callers.
type ModelCapacityArbiter struct {
	Config  *RoutingConfig
	Tracker *LimitTracker
	// Windows decides governed subscription lanes; nil means no readings were supplied, so
	// every candidate on a lane that names an account is refused as unknown.
	Windows *WindowGovernor
}

// NewModelCapacityArbiter creates an arbiter with active limit tracking.
func NewModelCapacityArbiter(cfg *RoutingConfig, tracker *LimitTracker) *ModelCapacityArbiter {
	if tracker == nil {
		tracker = NewLimitTracker()
	}
	return &ModelCapacityArbiter{
		Config:  cfg,
		Tracker: tracker,
	}
}

// CalculateHeadroom evaluates available capacity headroom C_avail(M) in [0.0, 1.0].
func (a *ModelCapacityArbiter) CalculateHeadroom(m ModelDescriptor) float64 {
	if a == nil || a.Tracker == nil || a.Config == nil || validateRoutingGovernance(a.Config.Governance) != nil {
		return 0
	}
	a.Tracker.mu.RLock()
	defer a.Tracker.mu.RUnlock()
	usage, _, reason := a.capacityLocked(m, true, time.Now())
	if reason != "" {
		return 0
	}
	return usageHeadroom(usage, m)
}

// SelectModel finds the best available model for a task, cascading to secondary tiers if primary is throttled.
func (a *ModelCapacityArbiter) SelectModel(targetTier string) (*ModelDescriptor, error) {
	if a.Config == nil || len(a.Config.Tiers) == 0 {
		return nil, errors.New("routing configuration is empty or nil")
	}

	if err := validateRoutingGovernance(a.Config.Governance); err != nil {
		return nil, err
	}
	a.Tracker.mu.RLock()
	defer a.Tracker.mu.RUnlock()
	currentTierName := targetTier
	pass := &taskPass{}

	seen := make(map[string]bool)
	for step := 0; currentTierName != "" && step < MaxRoutingTiers; step++ {
		if seen[currentTierName] {
			return nil, errors.New("model fallback tiers contain a cycle")
		}
		seen[currentTierName] = true
		tier, exists := a.Config.Tiers[currentTierName]
		if !exists {
			return nil, fmt.Errorf("tier %s not found in configuration", currentTierName)
		}

		if model := a.selectTierLocked(pass, tier); model != nil {
			return model, nil
		}

		// Current tier capacity exhausted; cascade to fallback tier
		currentTierName = tier.FallbackTier
	}

	return nil, fmt.Errorf("all tiers and fallback models have exceeded capacity thresholds%s", describeSkips(pass.skips))
}

func (a *ModelCapacityArbiter) selectTierLocked(pass *taskPass, tier Tier) *ModelDescriptor {
	for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
		model := tier.Models[i]
		if _, _, reason := a.capacityLocked(model, true, time.Now()); reason != "" {
			pass.skip(model, reason)
			continue
		}
		model.Skipped = pass.skips
		return &model
	}
	return nil
}

// SelectOrthogonalAuditor selects an auditor model from a different family than the author model.
func (a *ModelCapacityArbiter) SelectOrthogonalAuditor(authorFamily ModelFamily, preferredTier string) (*ModelDescriptor, error) {
	if a.Config == nil || len(a.Config.Tiers) == 0 {
		return nil, errors.New("routing configuration is empty or nil")
	}
	if err := validateRoutingGovernance(a.Config.Governance); err != nil {
		return nil, err
	}

	tier, exists := a.Config.Tiers[preferredTier]
	if !exists {
		tier = a.Config.Tiers["heavy-frontier"]
	}

	if model := a.orthogonalCandidate(tier, authorFamily); model != nil {
		return model, nil
	}
	names := make([]string, 0, len(a.Config.Tiers))
	for name := range a.Config.Tiers {
		names = append(names, name)
	}
	sort.Strings(names)
	for i := 0; i < len(names) && i < MaxRoutingTiers; i++ {
		if model := a.orthogonalCandidate(a.Config.Tiers[names[i]], authorFamily); model != nil {
			return model, nil
		}
	}
	return nil, fmt.Errorf("no available orthogonal auditor found for family %s", authorFamily)
}

func (a *ModelCapacityArbiter) orthogonalCandidate(tier Tier, family ModelFamily) *ModelDescriptor {
	for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
		model := tier.Models[i]
		headroom := a.CalculateHeadroom(model)
		if model.Family != family && headroom > 0 {
			return &model
		}
	}
	return nil
}
