package router

import (
	"errors"
	"fmt"
	"time"
)

const CooldownDuration = 30 * time.Second

// ModelCapacityArbiter arbitrates model selection using real-time quota telemetry and fallback cascades.
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
	if a.Tracker.IsCoolingDown(m.ID, CooldownDuration) {
		return 0.0
	}

	usage := a.Tracker.GetUsage(m.ID)

	rpmRatio := 0.0
	if m.RPMLimit > 0 {
		rpmRatio = float64(usage.CurrentRPM) / float64(m.RPMLimit)
	}

	tpmRatio := 0.0
	if m.TPMLimit > 0 {
		tpmRatio = float64(usage.CurrentTPM) / float64(m.TPMLimit)
	}

	maxRatio := rpmRatio
	if tpmRatio > maxRatio {
		maxRatio = tpmRatio
	}

	headroom := 1.0 - maxRatio
	if headroom < 0.0 {
		return 0.0
	}
	return headroom
}

// SelectModel finds the best available model for a task, cascading to secondary tiers if primary is throttled.
func (a *ModelCapacityArbiter) SelectModel(targetTier string) (*ModelDescriptor, error) {
	if a.Config == nil || len(a.Config.Tiers) == 0 {
		return nil, errors.New("routing configuration is empty or nil")
	}

	currentTierName := targetTier
	threshold := a.Config.Governance.ExhaustionThresholdPercent / 100.0
	minHeadroom := 1.0 - threshold

	for currentTierName != "" {
		tier, exists := a.Config.Tiers[currentTierName]
		if !exists {
			return nil, fmt.Errorf("tier %s not found in configuration", currentTierName)
		}

		// Try models in current tier
		for _, model := range tier.Models {
			headroom := a.CalculateHeadroom(model)
			if headroom >= minHeadroom {
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
	tier, exists := a.Config.Tiers[preferredTier]
	if !exists {
		tier = a.Config.Tiers["frontier"]
	}

	for _, m := range tier.Models {
		if m.Family != authorFamily {
			headroom := a.CalculateHeadroom(m)
			if headroom >= 0.20 {
				return &m, nil
			}
		}
	}

	// Try any other tier for an orthogonal family
	for _, t := range a.Config.Tiers {
		for _, m := range t.Models {
			if m.Family != authorFamily {
				headroom := a.CalculateHeadroom(m)
				if headroom >= 0.20 {
					return &m, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no available orthogonal auditor found for family %s", authorFamily)
}
