package router

import (
	"errors"
	"math"
	"math/big"
	"time"
)

type taskCapacity struct {
	recorded, projected ModelUsage
	observed            bool
}

// projectTaskCapacity evaluates one coherent tracker snapshot, including existing
// reservations. The caller holds the tracker lock for the whole selection.
func (a *ModelCapacityArbiter) projectTaskCapacity(request TaskRequest, model ModelDescriptor) (*taskCapacity, error) {
	if a.Tracker.overflowed[model.ID] {
		return nil, nil
	}
	usage, observed := a.Tracker.observedUsageLocked(model.ID)
	if err := validateRecordedUsage(usage); err != nil {
		return nil, err
	}
	knownLimits := model.RPMLimit > 0 && model.TPMLimit > 0
	if request.RequireObservedCapacity && (!observed || !knownLimits) {
		return nil, nil
	}
	projected, fits := projectUsage(usage, request.InputTokens+request.OutputTokens)
	if !fits || !quotaEligible(projected, model, a.Config.Governance.ExhaustionThresholdPercent, time.Now()) {
		return nil, nil
	}
	return &taskCapacity{recorded: usage, projected: projected, observed: observed}, nil
}

func validateRecordedUsage(usage ModelUsage) error {
	if usage.CurrentRPM < 0 || usage.CurrentTPM < 0 || usage.ErrorCount < 0 {
		return errors.New("recorded usage cannot be negative")
	}
	if usage.TotalSpend < 0 || math.IsNaN(usage.TotalSpend) || math.IsInf(usage.TotalSpend, 0) {
		return errors.New("recorded spend must be finite and nonnegative")
	}
	return nil
}

// projectUsage checks integer capacity before adding either request charge.
func projectUsage(usage ModelUsage, tokens int64) (ModelUsage, bool) {
	if usage.CurrentRPM == math.MaxInt || tokens > int64(math.MaxInt-usage.CurrentTPM) {
		return ModelUsage{}, false
	}
	usage.CurrentRPM++
	usage.CurrentTPM += int(tokens)
	return usage, true
}

func quotaEligible(usage ModelUsage, model ModelDescriptor, percent float64, now time.Time) bool {
	if !usage.Last429Time.IsZero() && now.Sub(usage.Last429Time) < CooldownDuration {
		return false
	}
	return dimensionEligible(usage.CurrentRPM, model.RPMLimit, percent) && dimensionEligible(usage.CurrentTPM, model.TPMLimit, percent)
}

// The integer comparison uses the exact configured float's rational value.
// float64(counter)/float64(limit) would round away threshold crossings near MaxInt.
// A float64 ratio is used only for diagnostic headroom, never admission.
func dimensionEligible(current, limit int, percent float64) bool {
	if limit == 0 {
		return true // Explicitly unknown/disabled; callers enforce observation policy.
	}
	if current < 0 || current >= limit {
		return false
	}
	threshold := new(big.Rat).SetFloat64(percent)
	if threshold == nil || percent < 0 || percent > 100 {
		return false
	}
	threshold.Mul(threshold, new(big.Rat).SetInt64(int64(limit)))
	threshold.Quo(threshold, big.NewRat(100, 1))
	return new(big.Rat).SetInt64(int64(current)).Cmp(threshold) <= 0
}

func usageHeadroom(usage ModelUsage, model ModelDescriptor) float64 {
	var rpm, tpm float64
	if model.RPMLimit > 0 {
		rpm = float64(usage.CurrentRPM) / float64(model.RPMLimit)
	}
	if model.TPMLimit > 0 {
		tpm = float64(usage.CurrentTPM) / float64(model.TPMLimit)
	}
	return max(0, min(1, 1-max(rpm, tpm)))
}
