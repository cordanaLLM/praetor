package router

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
)

func reservationArbiter(concurrency int) *ModelCapacityArbiter {
	model := taskModel("one", 1, 2, "tools")
	model.RPMLimit, model.TPMLimit = 10_000, 100_000
	cfg := taskConfig(model)
	cfg.Governance.MaxConcurrentSameModel = concurrency
	return NewModelCapacityArbiter(cfg, nil)
}

func reserveTask(t *testing.T, arbiter *ModelCapacityArbiter) *TaskReservation {
	t.Helper()
	reservation, err := arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 6, OutputTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}

func TestReservationCompletionAndUnknownUsage(t *testing.T) {
	for _, actual := range []*ReservationUsage{nil, {Tokens: 7, Cost: .003}, {}} {
		arbiter := reservationArbiter(1)
		reservation := reserveTask(t, arbiter)
		usage, observed := arbiter.Tracker.ObservedUsage("one")
		if usage.CurrentRPM != 1 || usage.CurrentTPM != 10 || observed {
			t.Fatalf("incorrect charge/provenance: %+v, %v", usage, observed)
		}
		if _, err := arbiter.ReserveForTask(context.Background(), reservation.Route().Request); !errors.Is(err, ErrNoEligibleModel) {
			t.Fatalf("concurrency cap ignored: %v", err)
		}
		if err := arbiter.Tracker.FinishReservation(reservation, actual); err != nil {
			t.Fatal(err)
		}
		got, observed := arbiter.Tracker.ObservedUsage("one")
		wantTokens, wantCost := 10, reservation.Route().EstimatedCost
		if actual != nil {
			wantTokens, wantCost = int(actual.Tokens), actual.Cost
		}
		if got.CurrentRPM != 1 || got.CurrentTPM != wantTokens || got.TotalSpend != wantCost || observed != (actual != nil) {
			t.Fatalf("completion accounting: %+v, observed=%v", got, observed)
		}
		if err := arbiter.Tracker.FinishReservation(reservation, actual); !errors.Is(err, ErrReservationInactive) {
			t.Fatalf("double finish: %v", err)
		}
		if after := arbiter.Tracker.GetUsage("one"); after != got {
			t.Fatal("double finish changed accounting")
		}
		next := reserveTask(t, arbiter)
		if err := arbiter.Tracker.FinishReservation(next, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReservationInvalidCompletionAndDetachedRoute(t *testing.T) {
	arbiter := reservationArbiter(1)
	reservation := reserveTask(t, arbiter)
	before := arbiter.Tracker.GetUsage("one")
	for _, actual := range []ReservationUsage{{Tokens: -1}, {Cost: -1}, {Cost: math.NaN()}, {Cost: math.Inf(1)}} {
		if err := arbiter.Tracker.FinishReservation(reservation, &actual); err == nil {
			t.Fatal("invalid actual usage accepted")
		}
		if arbiter.Tracker.GetUsage("one") != before {
			t.Fatal("invalid completion changed accounting")
		}
	}
	if err := NewLimitTracker().FinishReservation(reservation, nil); !errors.Is(err, ErrReservationInactive) {
		t.Fatal("foreign tracker accepted handle")
	}
	copyOfHandle := *reservation
	if err := arbiter.Tracker.FinishReservation(&copyOfHandle, nil); !errors.Is(err, ErrReservationInactive) {
		t.Fatal("copied handle accepted")
	}
	route := reservation.Route()
	route.Model.ID = "other"
	route.Model.Capabilities[0] = "altered"
	*route.ProjectedHeadroom = 0
	if reservation.Route().Model.ID != "one" || reservation.Route().Model.Capabilities[0] != "tools" || *reservation.Route().ProjectedHeadroom == 0 {
		t.Fatal("returned route aliases reservation")
	}
	if err := arbiter.Tracker.FinishReservation(reservation, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReservationConcurrentAdmission(t *testing.T) {
	arbiter := reservationArbiter(2)
	results := make(chan *TaskReservation, 64)
	failures := make(chan error, 64)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Go(func() {
			<-start
			reservation, err := arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 10})
			if err != nil {
				failures <- err
				return
			}
			results <- reservation
		})
	}
	close(start)
	group.Wait()
	close(results)
	close(failures)
	if len(results) != 2 || len(failures) != 62 {
		t.Fatalf("admitted=%d rejected=%d", len(results), len(failures))
	}
	for err := range failures {
		if !errors.Is(err, ErrNoEligibleModel) {
			t.Fatal(err)
		}
	}
	for reservation := range results {
		if err := arbiter.Tracker.FinishReservation(reservation, &ReservationUsage{Tokens: 5}); err != nil {
			t.Fatal(err)
		}
	}
	if got := arbiter.Tracker.GetUsage("one"); got.CurrentRPM != 2 || got.CurrentTPM != 10 {
		t.Fatalf("concurrent accounting: %+v", got)
	}
	if len(arbiter.Tracker.reservations) != 0 || len(arbiter.Tracker.active) != 0 {
		t.Fatal("active slots leaked")
	}
}

type admissionContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (ctx *admissionContext) Err() error {
	ctx.once.Do(func() { close(ctx.checked) })
	return ctx.Context.Err()
}

func TestReservationCancelledAdmissionHasNoWrites(t *testing.T) {
	arbiter := reservationArbiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := &admissionContext{Context: ctx, checked: make(chan struct{})}
	arbiter.Tracker.mu.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := arbiter.ReserveForTask(checked, TaskRequest{Task: "implement", InputTokens: 1})
		result <- err
	}()
	<-checked.checked
	cancel()
	arbiter.Tracker.mu.Unlock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lock waiter: %v", err)
	}
	if len(arbiter.Tracker.usage) != 0 || len(arbiter.Tracker.reservations) != 0 || len(arbiter.Tracker.active) != 0 {
		t.Fatal("cancelled admission mutated tracker")
	}
	if _, err := arbiter.ReserveForTask(ctx, TaskRequest{Task: "implement", InputTokens: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled admission: %v", err)
	}
}

func TestReservationExplicitConcurrencyAndTrackerBound(t *testing.T) {
	arbiter := reservationArbiter(0)
	if _, err := arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1}); err == nil {
		t.Fatal("missing concurrency cap accepted")
	}
	if len(arbiter.Tracker.usage) != 0 {
		t.Fatal("declined admission wrote counters")
	}
	arbiter.Config.Governance.MaxConcurrentSameModel = MaxActiveReservations + 1
	reservations := make([]*TaskReservation, 0, MaxActiveReservations)
	for i := 0; i < MaxActiveReservations; i++ {
		reservations = append(reservations, reserveTask(t, arbiter))
	}
	before := arbiter.Tracker.GetUsage("one")
	if _, err := arbiter.ReserveForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1}); err == nil {
		t.Fatal("tracker reservation bound ignored")
	}
	if arbiter.Tracker.GetUsage("one") != before {
		t.Fatal("full tracker admission changed counters")
	}
	for _, reservation := range reservations {
		if err := arbiter.Tracker.FinishReservation(reservation, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReservationOverflowReleasesSlotAndBlocksModel(t *testing.T) {
	arbiter := reservationArbiter(2)
	first, second := reserveTask(t, arbiter), reserveTask(t, arbiter)
	if err := arbiter.Tracker.FinishReservation(first, &ReservationUsage{Tokens: math.MaxInt64}); !errors.Is(err, ErrReservationOverflow) {
		t.Fatalf("actual usage overflow: %v", err)
	}
	if err := arbiter.Tracker.FinishReservation(second, &ReservationUsage{}); !errors.Is(err, ErrReservationOverflow) {
		t.Fatalf("saturated accounting forgotten: %v", err)
	}
	if len(arbiter.Tracker.active) != 0 || len(arbiter.Tracker.reservations) != 0 {
		t.Fatal("overflow leaked reservation slots")
	}
	if got := arbiter.Tracker.GetUsage("one"); got.CurrentTPM != math.MaxInt {
		t.Fatal("saturated usage was reclaimed")
	}
	if _, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1}); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("overflowed model admitted: %v", err)
	}
}

func TestSharedTrackerReservationsConsumeProjectedQuota(t *testing.T) {
	cfg := taskConfig(taskModel("one", 1, 1))
	cfg.Governance.MaxConcurrentSameModel = 100
	tracker := &LimitTracker{} // The zero value supports the same atomic API.
	a, b := NewModelCapacityArbiter(cfg, tracker), NewModelCapacityArbiter(cfg, tracker)
	request := TaskRequest{Task: "implement", InputTokens: 400}
	first, err := a.ReserveForTask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.ReserveForTask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReserveForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("pending token charges ignored: %v", err)
	}
	if _, err := b.SelectForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("advisory path ignored reservations: %v", err)
	}
	if err := tracker.FinishReservation(first, &ReservationUsage{Tokens: 50}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishReservation(second, nil); err != nil {
		t.Fatal(err)
	}
	if got := tracker.GetUsage("one"); got.CurrentRPM != 2 || got.CurrentTPM != 450 {
		t.Fatalf("usage was double charged: %+v", got)
	}
	request.InputTokens = 350
	third, err := a.ReserveForTask(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishReservation(third, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReservationUnknownCompletionDoesNotInventObservation(t *testing.T) {
	arbiter := reservationArbiter(1)
	reservation := reserveTask(t, arbiter)
	if err := arbiter.Tracker.FinishReservation(reservation, nil); err != nil {
		t.Fatal(err)
	}
	request := reservation.Route().Request
	request.RequireObservedCapacity = true
	if _, err := arbiter.ReserveForTask(context.Background(), request); !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("retained estimate became an observation: %v", err)
	}
	if len(arbiter.Tracker.reservations) != 0 {
		t.Fatal("declined admission retained handle")
	}
}

func TestReservationConcurrentFinishRecordsOnce(t *testing.T) {
	arbiter := reservationArbiter(1)
	reservation := reserveTask(t, arbiter)
	results := make(chan error, 32)
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Go(func() {
			results <- arbiter.Tracker.FinishReservation(reservation, &ReservationUsage{Tokens: 12, Cost: .1})
		})
	}
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrReservationInactive) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("completion succeeded %d times", succeeded)
	}
	if got := arbiter.Tracker.GetUsage("one"); got.CurrentRPM != 1 || got.CurrentTPM != 12 || got.TotalSpend != .1 {
		t.Fatalf("completion recorded more than once: %+v", got)
	}
}

func TestIndependentUsageCannotWrapReservations(t *testing.T) {
	arbiter := reservationArbiter(1)
	reservation := reserveTask(t, arbiter)
	arbiter.Tracker.RecordUsage("one", math.MaxInt, 0)
	if err := arbiter.Tracker.FinishReservation(reservation, &ReservationUsage{Tokens: 2}); !errors.Is(err, ErrReservationOverflow) {
		t.Fatalf("external overflow was lost: %v", err)
	}
	if got := arbiter.Tracker.GetUsage("one"); got.CurrentRPM != math.MaxInt || got.CurrentTPM != math.MaxInt {
		t.Fatalf("overflow wrapped counters: %+v", got)
	}
}
