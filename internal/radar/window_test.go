// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"testing"
	"time"
)

// at parses an RFC 3339 instant for a test.
func at(t *testing.T, value string) time.Time {
	t.Helper()
	instant, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return instant
}

// Positive: a seven-day window ending at a UTC instant starts exactly seven days earlier, holds
// since, excludes now, and is the same window for the same instant in another zone.
func TestWindow_Positive_HalfOpenInterval(t *testing.T) {
	now := at(t, "2026-10-07T06:00:00Z")
	w, err := NewWindow(now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Since.Equal(at(t, "2026-09-30T06:00:00Z")) || !w.Now.Equal(now) || w.Now.Location() != time.UTC {
		t.Fatalf("window = %v .. %v", w.Since, w.Now)
	}
	if !w.Contains(w.Since) || w.Contains(w.Now) || !w.Contains(w.Now.Add(-time.Second)) || w.Contains(w.Since.Add(-time.Nanosecond)) {
		t.Fatalf("boundaries wrong: since in %v, now in %v, now-1s in %v, since-1ns in %v",
			w.Contains(w.Since), w.Contains(w.Now), w.Contains(w.Now.Add(-time.Second)), w.Contains(w.Since.Add(-time.Nanosecond)))
	}
	zoned, err := NewWindow(now.In(time.FixedZone("UTC+5", 5*3600)), 7)
	if err != nil || zoned != w {
		t.Fatalf("zoned window = %+v, %v; want %+v", zoned, err, w)
	}
	got, err := ParseNow("2026-10-07T08:00:00+02:00")
	if err != nil || !got.Equal(now) || got.Location() != time.UTC {
		t.Fatalf("ParseNow timestamp = %v, %v", got, err)
	}
	date, err := ParseNow("2026-10-07")
	if err != nil || !date.Equal(at(t, "2026-10-07T00:00:00Z")) {
		t.Fatalf("ParseNow date = %v, %v", date, err)
	}
}

// Negative: no now, a day count outside 1..MaxWindowDays and a --now value in neither spelling
// are refused.
func TestWindow_Negative_InvalidInput(t *testing.T) {
	now := at(t, "2026-10-07T00:00:00Z")
	if _, err := NewWindow(time.Time{}, 7); err == nil {
		t.Error("a zero now was accepted")
	}
	for _, days := range []int{0, -1, MaxWindowDays + 1} {
		if _, err := NewWindow(now, days); err == nil {
			t.Errorf("days %d accepted", days)
		}
	}
	for _, value := range []string{"", "yesterday", "2026-10-07 00:00", "07.10.2026", "2026-13-01"} {
		if _, err := ParseNow(value); err == nil {
			t.Errorf("ParseNow(%q) accepted", value)
		}
	}
}

// Boundary: consecutive windows meet exactly, so an instant at, just before or just after the
// shared boundary falls in exactly one; dates partition the same way; a window across a leap day
// is still whole days; one day and MaxWindowDays are accepted.
func TestWindow_Boundary_PartitionAndLimits(t *testing.T) {
	now := at(t, "2024-03-03T12:30:00Z")
	later, err := NewWindow(now, 7)
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := NewWindow(later.Since, 7)
	if err != nil || !earlier.Now.Equal(later.Since) {
		t.Fatalf("earlier window = %+v, %v", earlier, err)
	}
	for _, offset := range []time.Duration{-time.Second, -time.Nanosecond, 0, time.Nanosecond, time.Second} {
		instant := later.Since.Add(offset)
		if earlier.Contains(instant) == later.Contains(instant) {
			t.Errorf("instant %v: earlier %v, later %v; want exactly one", instant, earlier.Contains(instant), later.Contains(instant))
		}
		date := civilDate(instant)
		if earlier.ContainsDate(date) == later.ContainsDate(date) {
			t.Errorf("date %v: earlier %v, later %v; want exactly one", date, earlier.ContainsDate(date), later.ContainsDate(date))
		}
	}
	if !later.ContainsDate(at(t, "2024-02-29T00:00:00Z")) || later.ContainsDate(at(t, "2024-03-03T00:00:00Z")) {
		t.Error("the leap day or now's date is placed wrongly")
	}
	leap, err := NewWindow(at(t, "2024-03-01T00:00:00Z"), 1)
	if err != nil || !leap.Since.Equal(at(t, "2024-02-29T00:00:00Z")) {
		t.Errorf("one day before 2024-03-01 = %v, %v; want the leap day", leap.Since, err)
	}
	year, err := NewWindow(at(t, "2025-03-01T00:00:00Z"), MaxWindowDays)
	if err != nil || !year.Since.Equal(at(t, "2024-02-29T00:00:00Z")) {
		t.Errorf("%d days = %v, %v", MaxWindowDays, year.Since, err)
	}
}
