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
	a.Tracker.mu.RLock()
	defer a.Tracker.mu.RUnlock()
	usage, _ := a.Tracker.observedUsageLocked(m.ID)
	if a.Tracker.overflowed[m.ID] || validateRecordedUsage(usage) != nil || !quotaEligible(usage, m, 100, time.Now()) {
		return 0
	}
	return usageHeadroom(usage, m)
}

// SelectModel finds the best available model for a task, cascading to secondary tiers if primary is throttled.
func (a *ModelCapacityArbiter) SelectModel(targetTier string) (*ModelDescriptor, error) {
	if a.Config == nil || len(a.Config.Tiers) == 0 {
		return nil, errors.New("routing configuration is empty or nil")
	}

	currentTierName := targetTier
	threshold := a.Config.Governance.ExhaustionThresholdPercent / 100.0
	minHeadroom := 1.0 - threshold

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

		// Try models in current tier
		for _, model := range tier.Models {
			headroom := a.CalculateHeadroom(model)
			if headroom > 0 && headroom >= minHeadroom {
				return &model, nil
			}
		}

		// Current tier capacity exhausted; cascade to fallback tier
		currentTierName = tier.FallbackTier
	}

	return nil, errors.New("all tiers and fallback models have exceeded capacity thresholds")
}

// SelectOrthogonalAuditor selects an auditor model from a different family than the author model.
func (a *ModelCapacityArbiter) SelectOrthogonalAuditor(authorFamily ModelFamily, preferredTier string) (*ModelDescriptor, error) {
	if a.Config == nil || len(a.Config.Tiers) == 0 {
		return nil, errors.New("routing configuration is empty or nil")
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
	for _, name := range names {
		if model := a.orthogonalCandidate(a.Config.Tiers[name], authorFamily); model != nil {
			return model, nil
		}
	}
	return nil, fmt.Errorf("no available orthogonal auditor found for family %s", authorFamily)
}

func (a *ModelCapacityArbiter) orthogonalCandidate(tier Tier, family ModelFamily) *ModelDescriptor {
	minimum := 1 - a.Config.Governance.ExhaustionThresholdPercent/100
	for _, model := range tier.Models {
		headroom := a.CalculateHeadroom(model)
		if model.Family != family && headroom > 0 && headroom >= minimum {
			return &model
		}
	}
	return nil
}
