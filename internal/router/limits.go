package router

import (
	"sync"
	"time"
)

// ModelUsage tracks real-time usage metrics for an individual model endpoint.
type ModelUsage struct {
	CurrentRPM  int
	CurrentTPM  int
	TotalSpend  float64
	Last429Time time.Time
	ErrorCount  int
}

// LimitTracker maintains thread-safe concurrency and usage counters across model endpoints.
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
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	u, ok := lt.usage[modelID]
	if !ok {
		return ModelUsage{}
	}
	return *u
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
