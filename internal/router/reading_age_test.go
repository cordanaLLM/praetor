package router

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestQuotaReadingAgeHeadroomAndCascade(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary, fallback := taskModel("primary", 1, 1), taskModel("fallback", 2, 2)
		cfg := taskConfig(primary)
		tier := cfg.Tiers["work"]
		tier.FallbackTier = "backup"
		cfg.Tiers["work"] = tier
		cfg.Tiers["backup"] = Tier{Models: []ModelDescriptor{fallback}}
		arbiter := NewModelCapacityArbiter(cfg, nil)
		arbiter.Tracker.RecordUsage(primary.ID, 10, 0)
		if got := arbiter.CalculateHeadroom(primary); got <= 0 {
			t.Fatalf("fresh reading refused: %v", got)
		}
		time.Sleep(cfg.Governance.QuotaReadingMaxAge)
		if got := arbiter.CalculateHeadroom(primary); got <= 0 {
			t.Fatalf("exact-age reading refused: %v", got)
		}
		if got, err := arbiter.SelectModel("work"); err != nil || got.ID != primary.ID {
			t.Fatalf("exact-age selection: %+v %v", got, err)
		}
		time.Sleep(time.Nanosecond)
		if got := arbiter.CalculateHeadroom(primary); got != 0 {
			t.Fatalf("stale reading has headroom %v", got)
		}
		arbiter.Tracker.RecordUsage(fallback.ID, 10, 0)
		got, err := arbiter.SelectModel("work")
		if err != nil || got.ID != fallback.ID {
			t.Fatalf("fallback selection: %+v %v", got, err)
		}
		if len(got.Skipped) != 1 || got.Skipped[0] != (RouteSkip{Model: primary.ID, Reason: "stale quota reading"}) {
			t.Fatalf("missing stale path: %+v", got.Skipped)
		}
	})
}

func TestQuotaReadingTaskAndReservationExclusions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := taskConfig(taskModel("cheap", 1, 1), taskModel("backup", 2, 2))
		cfg.Governance.MaxConcurrentSameModel = 2
		arbiter := NewModelCapacityArbiter(cfg, nil)
		arbiter.Tracker.RecordUsage("cheap", 0, 0)
		request := TaskRequest{Task: "implement", RequireObservedCapacity: true}
		if got := routeTask(t, arbiter, request); got.Model.ID != "cheap" {
			t.Fatalf("fresh reading skipped: %+v", got)
		}
		time.Sleep(cfg.Governance.QuotaReadingMaxAge + time.Nanosecond)
		arbiter.Tracker.RecordUsage("backup", 0, 0)
		for _, required := range []bool{false, true} {
			request.RequireObservedCapacity = required
			got := routeTask(t, arbiter, request)
			if got.Model.ID != "backup" || len(got.Skipped) != 1 || got.Skipped[0].Reason != "stale quota reading" {
				t.Fatalf("stale task reading admitted/hidden: %+v", got)
			}
		}
		reservation, err := arbiter.ReserveForTask(context.Background(), request)
		if err != nil || reservation.Route().Model.ID != "backup" {
			t.Fatalf("stale reservation admitted: %+v %v", reservation, err)
		}
		if err := arbiter.Tracker.FinishReservation(reservation, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQuotaReadingAuditorAge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := taskConfig(taskModel("old", 1, 1), taskModel("fresh", 2, 2))
		arbiter := NewModelCapacityArbiter(cfg, nil)
		arbiter.Tracker.RecordUsage("old", 0, 0)
		time.Sleep(cfg.Governance.QuotaReadingMaxAge + time.Nanosecond)
		arbiter.Tracker.RecordUsage("fresh", 0, 0)
		auditor, err := arbiter.SelectOrthogonalAuditor(FamilyAnthropic, "work")
		if err != nil || auditor.ID != "fresh" {
			t.Fatalf("stale auditor admitted: %+v %v", auditor, err)
		}
	})
}

func TestQuotaReadingRejectionReasons(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		usage        *ModelUsage
	}{
		{"missing", "missing quota reading", nil},
		{"undated", "unreadable quota reading", &ModelUsage{}},
		{"future", "unreadable quota reading", &ModelUsage{ObservedAt: time.Now().Add(time.Hour)}},
		{"invalid", "unreadable quota reading", &ModelUsage{ObservedAt: time.Now(), CurrentRPM: -1}},
		{"threshold", "over threshold", &ModelUsage{ObservedAt: time.Now(), CurrentRPM: 9}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := taskModel("one", 1, 1)
			arbiter := NewModelCapacityArbiter(taskConfig(model), nil)
			if test.usage != nil {
				arbiter.Tracker.usage[model.ID] = test.usage
			}
			if got := arbiter.CalculateHeadroom(model); got != 0 {
				t.Fatalf("invalid reading admitted: %v", got)
			}
			if _, err := arbiter.SelectModel("work"); err == nil || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("missing selection reason %q: %v", test.reason, err)
			}
			request := TaskRequest{Task: "implement", RequireObservedCapacity: true}
			if _, err := arbiter.SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("missing task reason %q: %v", test.reason, err)
			}
		})
	}
}

func TestQuotaReadingGovernanceBounds(t *testing.T) {
	for _, test := range []struct {
		fields string
		valid  bool
	}{
		{"", true},
		{"  quota_reading_max_age: 1ns\n", true},
		{"  quota_reading_max_age: 2m\n", true},
		{"  quota_reading_max_age: 0s\n", false},
		{"  quota_reading_max_age: -1s\n", false},
		{"  quota_reading_max_age: invalid\n", false},
	} {
		cfg, err := ParseRoutingConfig([]byte(routingFixture+test.fields), "test")
		if (err == nil) != test.valid {
			t.Fatalf("max age %q: %v", test.fields, err)
		}
		if test.fields == "" && cfg.Governance.QuotaReadingMaxAge != DefaultQuotaReadingMaxAge {
			t.Fatalf("omitted age default: %+v", cfg.Governance)
		}
	}
	cfg := taskConfig(taskModel("one", 1, 1))
	for _, threshold := range []float64{85, 86} {
		cfg.Governance.ExhaustionThresholdPercent = threshold
		if err := ValidateRoutingConfig(cfg); (err == nil) != (threshold == 85) {
			t.Fatalf("threshold %v: %v", threshold, err)
		}
	}
	cfg.Governance.ExhaustionThresholdPercent = 85
	cfg.Governance.QuotaReadingMaxAge = 0
	if err := ValidateRoutingConfig(cfg); err == nil {
		t.Fatal("zero maximum reading age accepted")
	}
}

func TestQuotaSnapshotObservationTimes(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-DefaultQuotaReadingMaxAge - time.Second)
	body := `{"version":1,"captured_at":"` + now.Format(time.RFC3339Nano) + `","models":{` +
		`"new":{"current_rpm":0,"current_tpm":0},` +
		`"old":{"current_rpm":0,"current_tpm":0,"observed_at":"` + older.Format(time.RFC3339Nano) + `"}}}`
	snapshot, err := LoadUsageSnapshot(context.Background(), routingInput(t, body))
	if err != nil {
		t.Fatal(err)
	}
	cfg := taskConfig(taskModel("new", 2, 2), taskModel("old", 1, 1))
	tracker, err := TrackerFromSnapshot(cfg, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !tracker.GetUsage("new").ObservedAt.Equal(now) || !tracker.GetUsage("old").ObservedAt.Equal(older) {
		t.Fatal("snapshot replaced observation times with load time")
	}
	got := routeTask(t, NewModelCapacityArbiter(cfg, tracker), TaskRequest{Task: "implement", RequireObservedCapacity: true})
	if got.Model.ID != "new" || len(got.Skipped) != 1 || got.Skipped[0].Reason != "stale quota reading" {
		t.Fatalf("old per-model reading hidden by fresh header: %+v", got)
	}
	snapshot.CapturedAt = older
	delete(snapshot.Models, "old")
	tracker, err = TrackerFromSnapshot(cfg, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := NewModelCapacityArbiter(cfg, tracker).CalculateHeadroom(cfg.Tiers["work"].Models[0]); got != 0 {
		t.Fatalf("old snapshot refreshed on import: %v", got)
	}
}

func TestQuotaObservationTimestampUpdates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		arbiter := reservationArbiter(2)
		tracker := arbiter.Tracker
		tracker.RecordUsage("one", 0, 0)
		observed := time.Now()
		time.Sleep(time.Minute)
		reservation, err := arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement"})
		if err != nil {
			t.Fatal(err)
		}
		tracker.Record429("one")
		if err := tracker.FinishReservation(reservation, nil); err != nil {
			t.Fatal(err)
		}
		if !tracker.GetUsage("one").ObservedAt.Equal(observed) {
			t.Fatal("estimate, nil completion or 429 refreshed observation")
		}
		time.Sleep(CooldownDuration)
		reservation, err = arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement"})
		if err != nil {
			t.Fatal(err)
		}
		if err := tracker.FinishReservation(reservation, &ReservationUsage{}); err != nil {
			t.Fatal(err)
		}
		if !tracker.GetUsage("one").ObservedAt.Equal(time.Now()) {
			t.Fatal("actual completion did not timestamp observation")
		}
	})
}
