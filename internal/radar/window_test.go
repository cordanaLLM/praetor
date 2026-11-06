// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"testing"
	"time"
)

// Positive: a seven-day window ending at a UTC instant starts exactly seven days earlier, holds
// since, excludes now, and is the same window for the same instant in another zone.
func TestWindow_Positive_HalfOpenInterval(t *testing.T) {
	now := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	w, err := NewWindow(now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Since.Equal(now.AddDate(0, 0, -7)) || !w.Now.Equal(now) || w.Now.Location() != time.UTC {
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
	got, err := ParseNow(now.In(time.FixedZone("UTC+2", 2*3600)).Format(time.RFC3339))
	if err != nil || !got.Equal(now) || got.Location() != time.UTC {
		t.Fatalf("ParseNow timestamp = %v, %v", got, err)
	}
	date, err := ParseNow(now.Format(time.DateOnly))
	if err != nil || !date.Equal(now.Truncate(24*time.Hour)) {
		t.Fatalf("ParseNow date = %v, %v", date, err)
	}
}

// Negative: no now, a day count outside 1..MaxWindowDays and a --now value in neither spelling
// are refused.
func TestWindow_Negative_InvalidInput(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	if _, err := NewWindow(time.Time{}, 7); err == nil {
		t.Error("a zero now was accepted")
	}
	for _, days := range []int{0, -1, MaxWindowDays + 1} {
		if _, err := NewWindow(now, days); err == nil {
			t.Errorf("days %d accepted", days)
		}
	}
	for _, value := range []string{"", "yesterday",
		/* testdate: fact: recorded feed/release date; parser input, no clock comparison */
		"2026-10-07 00:00", "07.10.2026",
		/* testdate: fact: recorded feed/release date; parser input, no clock comparison */
		"2026-13-01"} {
		if _, err := ParseNow(value); err == nil {
			t.Errorf("ParseNow(%q) accepted", value)
		}
	}
}

// Boundary: consecutive windows meet exactly, so an instant at, just before or just after the
// shared boundary falls in exactly one; dates partition the same way; a window across a leap day
// is still whole days; one day and MaxWindowDays are accepted.
func TestWindow_Boundary_PartitionAndLimits(t *testing.T) {
	now := time.Date(2024, 3, 3, 12, 30, 0, 0, time.UTC)
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
	if !later.ContainsDate(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) || later.ContainsDate(time.Date(2024, 3, 3, 0, 0, 0, 0, time.UTC)) {
		t.Error("the leap day or now's date is placed wrongly")
	}
	leap, err := NewWindow(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), 1)
	if err != nil || !leap.Since.Equal(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf( /* testdate: fact: recorded feed/release date; parser input, no clock comparison */ "one day before 2024-03-01 = %v, %v; want the leap day", leap.Since, err)
	}
	year, err := NewWindow(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), MaxWindowDays)
	if err != nil || !year.Since.Equal(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("%d days = %v, %v", MaxWindowDays, year.Since, err)
	}
}
