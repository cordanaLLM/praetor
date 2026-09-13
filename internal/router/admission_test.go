package router

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestTaskProjectedCapacity(t *testing.T) {
	for _, test := range []struct {
		name          string
		rpm, tpm      int
		input, output int64
		allowed       bool
	}{
		{"rpm threshold", 7, 0, 1, 0, true},
		{"rpm projected excess", 8, 0, 1, 0, false},
		{"combined tokens threshold", 0, 700, 60, 40, true},
		{"combined tokens excess", 0, 700, 60, 41, false},
		{"request alone exceeds capacity", 0, 0, 500, 301, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := NewLimitTracker()
			tracker.usage["one"] = &ModelUsage{CurrentRPM: test.rpm, CurrentTPM: test.tpm}
			arbiter := NewModelCapacityArbiter(taskConfig(taskModel("one", 1, 1)), tracker)
			_, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: test.input, OutputTokens: test.output})
			if test.allowed && err != nil {
				t.Fatal(err)
			}
			if !test.allowed && !errors.Is(err, ErrNoEligibleModel) {
				t.Fatalf("expected capacity rejection, got %v", err)
			}
			if got := tracker.GetUsage("one"); got.CurrentRPM != test.rpm || got.CurrentTPM != test.tpm {
				t.Fatal("advisory selection mutated counters")
			}
		})
	}
}

func TestTaskProjectedCapacityIntegerBounds(t *testing.T) {
	model := taskModel("one", 1, 1)
	model.RPMLimit, model.TPMLimit = math.MaxInt, math.MaxInt
	threshold := (math.MaxInt/5)*4 + (math.MaxInt%5)*4/5
	for _, current := range []int{threshold - 1, threshold, math.MaxInt} {
		tracker := NewLimitTracker()
		tracker.usage["one"] = &ModelUsage{CurrentRPM: current}
		_, err := NewModelCapacityArbiter(taskConfig(model), tracker).SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1})
		if current == threshold-1 && err != nil {
			t.Fatalf("exact integer threshold rejected: %v", err)
		}
		if current >= threshold && !errors.Is(err, ErrNoEligibleModel) {
			t.Fatalf("projected overflow/threshold accepted: %v", err)
		}
	}
}

func TestOrthogonalUsesConfiguredThreshold(t *testing.T) {
	for _, test := range []struct {
		threshold float64
		rpm       int
		allowed   bool
	}{{50, 6, false}, {90, 8, true}, {100, 10, false}, {100, 9, true}} {
		model := taskModel("auditor", 1, 1)
		cfg := taskConfig(model)
		cfg.Governance.ExhaustionThresholdPercent = test.threshold
		tracker := NewLimitTracker()
		tracker.usage[model.ID] = &ModelUsage{CurrentRPM: test.rpm}
		_, err := NewModelCapacityArbiter(cfg, tracker).SelectOrthogonalAuditor(FamilyAnthropic, "work")
		if test.allowed && err != nil {
			t.Fatalf("threshold %v/rpm %d: %v", test.threshold, test.rpm, err)
		}
		if !test.allowed && err == nil {
			t.Fatalf("threshold %v/rpm %d accepted", test.threshold, test.rpm)
		}
	}
}

func TestTaskUnknownQuotaDimensionsAndZeroThreshold(t *testing.T) {
	for _, limits := range [][2]int{{0, 1000}, {10, 0}, {0, 0}} {
		model := taskModel("one", 1, 1)
		model.RPMLimit, model.TPMLimit = limits[0], limits[1]
		tracker := NewLimitTracker()
		tracker.usage["one"] = &ModelUsage{}
		arbiter := NewModelCapacityArbiter(taskConfig(model), tracker)
		route := routeTask(t, arbiter, TaskRequest{Task: "implement", InputTokens: 1})
		if route.QuotaLimitsKnown || route.ProjectedHeadroom != nil {
			t.Fatal("unknown quota dimension reported as known")
		}
		if _, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1, RequireObservedCapacity: true}); !errors.Is(err, ErrNoEligibleModel) {
			t.Fatalf("observed counters substituted for quota limits: %v", err)
		}
	}
	cfg := taskConfig(taskModel("one", 1, 1))
	cfg.Governance.ExhaustionThresholdPercent = 0
	if _, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1}); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("zero threshold allowed projected usage: %v", err)
	}
}

func TestCooldownEventDoesNotInventQuotaObservation(t *testing.T) {
	tracker := NewLimitTracker()
	tracker.Record429("one")
	if _, observed := tracker.ObservedUsage("one"); observed {
		t.Fatal("429 event invented RPM/TPM observations")
	}
	tracker.usage["one"].Last429Time = time.Now().Add(-2 * CooldownDuration)
	arbiter := NewModelCapacityArbiter(taskConfig(taskModel("one", 1, 1)), tracker)
	request := TaskRequest{Task: "implement", InputTokens: 1, RequireObservedCapacity: true}
	if _, err := arbiter.SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("expired cooldown supplied invented observations: %v", err)
	}
	tracker.RecordUsage("one", 5, 0)
	tracker.Record429("one")
	if _, observed := tracker.ObservedUsage("one"); !observed {
		t.Fatal("429 erased actual consumption observation")
	}
}
