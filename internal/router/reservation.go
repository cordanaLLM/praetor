package router

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// MaxActiveReservations bounds retained admission handles in one tracker.
const MaxActiveReservations = MaxRoutingModels

var (
	ErrReservationInactive = errors.New("reservation is foreign, invalid or already finished")
	ErrReservationOverflow = errors.New("completion accounting overflow; saturated usage retained and model blocked")
)

// ReservationUsage is known actual completion usage, including an explicit zero.
// Tokens must be nonnegative; Cost must be finite and nonnegative in configured units.
type ReservationUsage struct {
	Tokens int64
	Cost   float64
}

// TaskReservation is an opaque, process-local handle. It cannot be restored from
// JSON or transferred to another tracker. The caller must finish it exactly once,
// including failed/cancelled dispatches. Retain the handle until execution stops.
type TaskReservation struct {
	tracker *LimitTracker
	route   TaskRoute
	tokens  int64
}

// Route returns a detached advisory description of the reserved candidate.
func (r *TaskReservation) Route() TaskRoute {
	if r == nil {
		return TaskRoute{}
	}
	result := r.route
	result.Request.Capabilities = append([]string(nil), r.route.Request.Capabilities...)
	result.Model.Capabilities = append([]string(nil), r.route.Model.Capabilities...)
	if r.route.RecordedHeadroom != nil {
		value := *r.route.RecordedHeadroom
		result.RecordedHeadroom = &value
	}
	if r.route.ProjectedHeadroom != nil {
		value := *r.route.ProjectedHeadroom
		result.ProjectedHeadroom = &value
	}
	return result
}

// ReserveForTask selects and charges one request under the shared tracker's lock.
// A positive configured concurrency cap is mandatory. Cancellation is checked
// again before charging; a successful admission must be finished even if its
// context is subsequently cancelled. No goroutine or provider is started.
func (a *ModelCapacityArbiter) ReserveForTask(ctx context.Context, request TaskRequest) (*TaskReservation, error) {
	if err := a.validateTaskRoute(ctx, request); err != nil {
		return nil, err
	}
	if a.Config.Governance.MaxConcurrentSameModel == 0 {
		return nil, errors.New("reservation requires a positive max_concurrent_same_model")
	}
	lt := a.Tracker
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(lt.reservations) >= MaxActiveReservations {
		return nil, fmt.Errorf("active reservations exceed tracker bound %d", MaxActiveReservations)
	}
	route, err := a.selectTaskLocked(ctx, request, true)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lt.chargeReservationLocked(route), nil
}

func (lt *LimitTracker) chargeReservationLocked(route *TaskRoute) *TaskReservation {
	lt.initializeLocked()
	modelID := route.Model.ID
	u, exists := lt.usage[modelID]
	if !exists {
		u = &ModelUsage{}
		lt.usage[modelID] = u
		lt.unobserved[modelID] = true
	}
	reservation := &TaskReservation{tracker: lt, route: *route, tokens: route.Request.InputTokens + route.Request.OutputTokens}
	reservation.route.Basis = "configured eligible candidate reserved on the shared process-local tracker; no live availability or provider dispatch claim"
	u.CurrentRPM++
	u.CurrentTPM += int(reservation.tokens)
	u.TotalSpend += route.EstimatedCost
	lt.active[modelID]++
	lt.reservations[reservation] = struct{}{}
	return reservation
}

func (a *ModelCapacityArbiter) reservationAvailableLocked(modelID string) bool {
	if a.Tracker.active[modelID] >= a.Config.Governance.MaxConcurrentSameModel {
		return false
	}
	return a.Tracker.usage[modelID] != nil || len(a.Tracker.usage) < MaxRoutingModels
}

// FinishReservation releases an active slot and replaces its estimate with known
// actual usage exactly once. A nil actual retains the charge. Invalid usage leaves
// the handle active so the caller can retry or finish with nil. Overflow releases
// the slot, retains conservative saturated accounting and blocks further admission.
// Finish deliberately needs no context: cancelled execution still requires cleanup.
func (lt *LimitTracker) FinishReservation(reservation *TaskReservation, actual *ReservationUsage) error {
	if lt == nil || reservation == nil || reservation.tracker != lt {
		return ErrReservationInactive
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if _, exists := lt.reservations[reservation]; !exists {
		return ErrReservationInactive
	}
	if actual != nil {
		if err := validateReservationUsage(*actual); err != nil {
			return err
		}
	}
	modelID := reservation.route.Model.ID
	delete(lt.reservations, reservation)
	lt.active[modelID]--
	if lt.active[modelID] == 0 {
		delete(lt.active, modelID)
	}
	if actual == nil {
		return nil
	}
	return lt.recordCompletionLocked(reservation, *actual)
}

func validateReservationUsage(actual ReservationUsage) error {
	if actual.Tokens < 0 || actual.Cost < 0 || math.IsNaN(actual.Cost) || math.IsInf(actual.Cost, 0) {
		return errors.New("actual usage must have nonnegative tokens and finite nonnegative cost")
	}
	return nil
}

func (lt *LimitTracker) recordCompletionLocked(reservation *TaskReservation, actual ReservationUsage) error {
	modelID := reservation.route.Model.ID
	if lt.overflowed[modelID] {
		return ErrReservationOverflow
	}
	u := lt.usage[modelID]
	if err := validateRecordedUsage(*u); err != nil {
		lt.overflowed[modelID] = true
		return fmt.Errorf("completion accounting invalid: %w", err)
	}
	remaining := max(0, int64(u.CurrentTPM)-reservation.tokens)
	overflow := actual.Tokens > int64(math.MaxInt)-remaining
	u.CurrentTPM = math.MaxInt
	if !overflow {
		u.CurrentTPM = int(remaining + actual.Tokens)
	}
	spend := max(0, u.TotalSpend-reservation.route.EstimatedCost)
	if actual.Cost > math.MaxFloat64-spend {
		u.TotalSpend = math.MaxFloat64
		overflow = true
	} else {
		u.TotalSpend = spend + actual.Cost
	}
	delete(lt.unobserved, modelID)
	if overflow {
		lt.overflowed[modelID] = true
		return ErrReservationOverflow
	}
	return nil
}
