// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Plant a day thirty days beyond the host clock. A real-time gate incorrectly
// accepts yesterday's exception; the test gate must use the supplied UTC day.
func TestGateExceptionExpiryUsesControlledDay(t *testing.T) {
	today := time.Now().UTC().AddDate(0, 0, 30)
	h := newGateHarnessAt(t, today)
	f := newFixture(t, h)
	base := f.widget()
	f.commit("break lib", map[string]string{"lib/lib.go": brokenLib})
	for _, tc := range []struct {
		name         string
		days, status int
	}{
		{"expired yesterday", -1, 2},
		{"expiry day inclusive", 0, 0},
		{"future expiry", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expires := today.AddDate(0, 0, tc.days).Format(time.DateOnly)
			run := h.run(t, f.dir, exceptionsEnvEntry(t, libException(expires)), "-base="+base)
			if run.status != tc.status {
				t.Errorf("controlled day %s, expiry %s: exit %d, want %d: %s", today.Format(time.DateOnly), expires, run.status, tc.status, run.stderr)
			}
			if tc.status == 2 && !strings.Contains(run.stderr, "expired on "+expires) {
				t.Errorf("missing expired date: %s", run.stderr)
			}
		})
	}
}

// Freeze only the test binary's wall-clock read. Production keeps its real UTC clock.
func newGateHarnessAt(t *testing.T, today time.Time) gateHarness {
	t.Helper()
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(source), "time.Now()") != 1 {
		t.Fatal("gate clock seam changed: expected exactly one wall-clock read")
	}
	frozen := strings.Replace(string(source), "time.Now()", fmt.Sprintf("time.Unix(%d, %d)", today.Unix(), today.Nanosecond()), 1)
	return buildGateHarness(t, frozen)
}
