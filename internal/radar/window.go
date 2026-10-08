// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"errors"
	"fmt"
	"time"
)

// MaxWindowDays caps the window length: one year, leap day included.
const MaxWindowDays = 366

// dayLength is one UTC day. UTC has no daylight saving shift and Go's clock has no leap
// seconds, so every day of the window is exactly this long.
const dayLength = 24 * time.Hour

// dateLayout spells a calendar date.
const dateLayout = "2006-01-02"

// Window is the half-open interval [Since, Now) in UTC that one collection covers. It is a pure
// function of now and the day count, so consecutive runs whose now values are one window apart
// partition time: every instant belongs to exactly one window.
type Window struct {
	Since time.Time
	Now   time.Time
}

// NewWindow returns the window of days whole days ending at now, exclusive. now is converted to
// UTC; the clock is never read, so the caller decides which window a run covers.
func NewWindow(now time.Time, days int) (Window, error) {
	if now.IsZero() {
		return Window{}, errors.New("radar window: now is required")
	}
	if days < 1 || days > MaxWindowDays {
		return Window{}, fmt.Errorf("radar window: days %d outside 1..%d", days, MaxWindowDays)
	}
	end := now.UTC()
	return Window{Since: end.Add(-time.Duration(days) * dayLength), Now: end}, nil
}

// Contains reports whether the instant t lies in [Since, Now).
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Since) && t.Before(w.Now)
}

// ContainsDate reports whether the UTC calendar date of t lies in [Since's date, Now's date).
// It places an item that carries only a date: such an item belongs to the run whose window
// starts on or before its date and ends after it, so consecutive windows partition dates too.
func (w Window) ContainsDate(t time.Time) bool {
	date := civilDate(t)
	return !date.Before(civilDate(w.Since)) && date.Before(civilDate(w.Now))
}

// civilDate is midnight UTC of t's UTC calendar date.
func civilDate(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// ParseNow reads the --now value of a collection: an RFC 3339 timestamp, or a calendar date,
// which means midnight UTC of that date. The result is UTC.
func ParseNow(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("radar window: now is required (RFC 3339 timestamp or YYYY-MM-DD)")
	}
	if date, err := time.Parse(dateLayout, value); err == nil {
		return date.UTC(), nil
	}
	instant, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("radar window: now %q is neither an RFC 3339 timestamp nor YYYY-MM-DD", value)
	}
	return instant.UTC(), nil
}
