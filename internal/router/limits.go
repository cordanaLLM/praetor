package router

import (
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

// LimitTracker protects recorded usage counters; it does not reserve concurrency.
type LimitTracker struct {
	mu    sync.RWMutex
	usage map[string]*ModelUsage
}

// NewLimitTracker initializes a limit tracker.
func NewLimitTracker() *LimitTracker {
	return &LimitTracker{
		usage: make(map[string]*ModelUsage),
	}
}

// RecordUsage registers token and request consumption.
func (lt *LimitTracker) RecordUsage(modelID string, tokens int, cost float64) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	u, ok := lt.usage[modelID]
	if !ok {
		u = &ModelUsage{}
		lt.usage[modelID] = u
	}

	u.CurrentRPM++
	u.CurrentTPM += tokens
	u.TotalSpend += cost
}

// Record429 records a rate-limit cooldown event for a model.
func (lt *LimitTracker) Record429(modelID string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	u, ok := lt.usage[modelID]
	if !ok {
		u = &ModelUsage{}
		lt.usage[modelID] = u
	}

	u.Last429Time = time.Now()
	u.ErrorCount++
}

// GetUsage retrieves current usage metrics for a model.
func (lt *LimitTracker) GetUsage(modelID string) ModelUsage {
	usage, _ := lt.ObservedUsage(modelID)
	return usage
}

// ObservedUsage distinguishes a recorded zero from absent capacity information.
func (lt *LimitTracker) ObservedUsage(modelID string) (ModelUsage, bool) {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	u, ok := lt.usage[modelID]
	if !ok {
		return ModelUsage{}, false
	}
	return *u, true
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
