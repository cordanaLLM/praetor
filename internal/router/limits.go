package router

import (
	"math"
	"sync"
	"time"
)

// ModelUsage holds recorded counters and cooldown data for one model endpoint.
type ModelUsage struct {
	CurrentRPM  int       `json:"current_rpm" yaml:"current_rpm"`
	CurrentTPM  int       `json:"current_tpm" yaml:"current_tpm"`
	TotalSpend  float64   `json:"total_spend" yaml:"total_spend"`
	Last429Time time.Time `json:"last_429_time" yaml:"last_429_time"`
	ErrorCount  int       `json:"error_count" yaml:"error_count"`
}

// LimitTracker protects recorded and reserved counters within this process only.
// Share one tracker across dispatchers; separate trackers cannot enforce a joint
// quota. Counters do not expire or establish live provider/fleet availability.
type LimitTracker struct {
	mu           sync.RWMutex
	usage        map[string]*ModelUsage
	active       map[string]int
	reservations map[*TaskReservation]struct{}
	unobserved   map[string]bool
	overflowed   map[string]bool
}

// NewLimitTracker initializes a limit tracker.
func NewLimitTracker() *LimitTracker {
	return &LimitTracker{
		usage:        make(map[string]*ModelUsage),
		active:       make(map[string]int),
		reservations: make(map[*TaskReservation]struct{}),
		unobserved:   make(map[string]bool),
		overflowed:   make(map[string]bool),
	}
}

// RecordUsage registers independent consumption. Reserved requests must instead
// use FinishReservation; calling both would charge the same request twice.
func (lt *LimitTracker) RecordUsage(modelID string, tokens int, cost float64) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.initializeLocked()

	u, ok := lt.usage[modelID]
	if !ok {
		u = &ModelUsage{}
		lt.usage[modelID] = u
		lt.unobserved[modelID] = true
	}

	actual := ReservationUsage{Tokens: int64(tokens), Cost: cost}
	if validateReservationUsage(actual) != nil || validateRecordedUsage(*u) != nil {
		lt.overflowed[modelID] = true
		return
	}
	lt.addRecordedUsageLocked(modelID, u, actual)
	delete(lt.unobserved, modelID)
}

// Independent consumption must not wrap reserved counters or make saturated
// accounting available again. RecordUsage's legacy void API fails closed.
func (lt *LimitTracker) addRecordedUsageLocked(modelID string, usage *ModelUsage, actual ReservationUsage) {
	projected, fits := projectUsage(*usage, actual.Tokens)
	if !fits || actual.Cost > math.MaxFloat64-usage.TotalSpend || lt.overflowed[modelID] {
		usage.CurrentRPM, usage.CurrentTPM, usage.TotalSpend = math.MaxInt, math.MaxInt, math.MaxFloat64
		lt.overflowed[modelID] = true
		return
	}
	projected.TotalSpend += actual.Cost
	*usage = projected
}

// Record429 records a rate-limit cooldown event for a model.
func (lt *LimitTracker) Record429(modelID string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.initializeLocked()

	u, ok := lt.usage[modelID]
	if !ok {
		u = &ModelUsage{}
		lt.usage[modelID] = u
		lt.unobserved[modelID] = true
	}

	u.Last429Time = time.Now()
	if u.ErrorCount < math.MaxInt {
		u.ErrorCount++
	}
}

// GetUsage retrieves current usage metrics for a model.
func (lt *LimitTracker) GetUsage(modelID string) ModelUsage {
	usage, _ := lt.ObservedUsage(modelID)
	return usage
}

// ObservedUsage returns counters including reservation estimates. Its boolean
// distinguishes actual/supplied observations from counters created only by estimates.
func (lt *LimitTracker) ObservedUsage(modelID string) (ModelUsage, bool) {
	lt.mu.RLock()
	defer lt.mu.RUnlock()
	return lt.observedUsageLocked(modelID)
}

func (lt *LimitTracker) observedUsageLocked(modelID string) (ModelUsage, bool) {
	u, ok := lt.usage[modelID]
	if !ok {
		return ModelUsage{}, false
	}
	return *u, !lt.unobserved[modelID]
}

// IsCoolingDown returns true if the model recently encountered a 429 within the cooldown window.
func (lt *LimitTracker) IsCoolingDown(modelID string, cooldownWindow time.Duration) bool {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	u, ok := lt.usage[modelID]
	if !ok {
		return false
	}
	return time.Since(u.Last429Time) < cooldownWindow
}

func (lt *LimitTracker) initializeLocked() {
	if lt.usage == nil {
		lt.usage = make(map[string]*ModelUsage)
	}
	if lt.active == nil {
		lt.active = make(map[string]int)
	}
	if lt.reservations == nil {
		lt.reservations = make(map[*TaskReservation]struct{})
	}
	if lt.unobserved == nil {
		lt.unobserved = make(map[string]bool)
	}
	if lt.overflowed == nil {
		lt.overflowed = make(map[string]bool)
	}
}
