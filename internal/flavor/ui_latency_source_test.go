// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
)

func setupCompleteUIRepo(t *testing.T) string {
	t.Helper()
	dir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	renderUISpecFixture(t, dir)
	writeMatrixFixture(t, dir, "{}", true)
	return dir
}

func TestAuditUIGates_LatencySource_ValidSourcePasses(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	m := &config.Manifest{UI: &config.UIPolicy{
		InputLatencyMs:     100,
		InputLatencySource: validUISourceFixture,
		Routes:             []string{"/"},
		ScreenMatrix:       "screen-matrix.json",
	}}
	line, err := flavor.AuditUIGates(dir, m)
	if err != nil {
		t.Fatalf("AuditUIGates unexpected error: %v", err)
	}
	if !strings.Contains(line, "[PASS] UI gates:") {
		t.Fatalf("expected [PASS], got: %q", line)
	}
	if !strings.Contains(line, "source="+validUISourceFixture) {
		t.Fatalf("expected PASS line to name source, got: %q", line)
	}
}

func TestAuditUIGates_LatencySource_MissingSourceFails(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	m := &config.Manifest{UI: &config.UIPolicy{
		InputLatencyMs: 100,
		Routes:         []string{"/"},
		ScreenMatrix:   "screen-matrix.json",
	}}
	_, err := flavor.AuditUIGates(dir, m)
	if err == nil {
		t.Fatal("AuditUIGates with missing source must fail")
	}
	const wantGap = "ui.input_latency_source missing: pin the measurement evidence the threshold comes from"
	if !strings.Contains(err.Error(), wantGap) {
		t.Fatalf("error = %v, want substring %q", err, wantGap)
	}
}

func TestAuditUIGates_LatencySource_LiveExceptionPasses(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	today := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	exc := config.Exception{
		Rule:     config.ExceptionRuleUILatencySource,
		Category: "C",
		Entered:  "2026-10-28",
		Expires:  "2026-11-03",
		Reason:   "threshold schema owner cordanaLLM/Aegis-OS M28, parked until 2026-11-03",
	}
	m := &config.Manifest{
		UI: &config.UIPolicy{
			InputLatencyMs: 100,
			Routes:         []string{"/"},
			ScreenMatrix:   "screen-matrix.json",
		},
		Exceptions: []config.Exception{exc},
	}
	line, err := flavor.AuditUIGatesToday(dir, m, today)
	if err != nil {
		t.Fatalf("AuditUIGatesToday with live exception unexpected error: %v", err)
	}
	if !strings.Contains(line, "exception=ui-latency-source (owner cordanaLLM/Aegis-OS, expires 2026-11-03)") {
		t.Fatalf("expected PASS line to name exception details, got: %q", line)
	}
}

func TestAuditUIGates_LatencySource_ExpiredExceptionFails(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	afterExpiry := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	exc := config.Exception{
		Rule:     config.ExceptionRuleUILatencySource,
		Category: "C",
		Entered:  "2026-10-28",
		Expires:  "2026-11-03",
		Reason:   "threshold schema owner cordanaLLM/Aegis-OS M28, parked until 2026-11-03",
	}
	m := &config.Manifest{
		UI: &config.UIPolicy{
			InputLatencyMs: 100,
			Routes:         []string{"/"},
			ScreenMatrix:   "screen-matrix.json",
		},
		Exceptions: []config.Exception{exc},
	}
	_, err := flavor.AuditUIGatesToday(dir, m, afterExpiry)
	if err == nil {
		t.Fatal("expired exception must not excuse missing source")
	}
	const wantGap = "ui.input_latency_source missing: pin the measurement evidence the threshold comes from"
	if !strings.Contains(err.Error(), wantGap) {
		t.Fatalf("error = %v, want substring %q", err, wantGap)
	}
}

func TestAuditUIGates_LatencySource_ExceptionWithoutOwnerFails(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	today := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	exc := config.Exception{
		Rule:     config.ExceptionRuleUILatencySource,
		Category: "C",
		Entered:  "2026-10-28",
		Expires:  "2026-11-03",
		Reason:   "category C: temporary waiver without naming supplier",
	}
	m := &config.Manifest{
		UI: &config.UIPolicy{
			InputLatencyMs: 100,
			Routes:         []string{"/"},
			ScreenMatrix:   "screen-matrix.json",
		},
		Exceptions: []config.Exception{exc},
	}
	_, err := flavor.AuditUIGatesToday(dir, m, today)
	if err == nil {
		t.Fatal("exception without named owner must not excuse missing source")
	}
	const wantGap = "ui.input_latency_source missing: pin the measurement evidence the threshold comes from"
	if !strings.Contains(err.Error(), wantGap) {
		t.Fatalf("error = %v, want substring %q", err, wantGap)
	}
}

func TestAuditUIGates_LatencySource_MissingLatencyUnchanged(t *testing.T) {
	dir := setupCompleteUIRepo(t)
	m := &config.Manifest{UI: &config.UIPolicy{
		InputLatencySource: validUISourceFixture,
		Routes:             []string{"/"},
		ScreenMatrix:       "screen-matrix.json",
	}}
	_, err := flavor.AuditUIGates(dir, m)
	if err == nil {
		t.Fatal("missing latency must fail")
	}
	if !strings.Contains(err.Error(), "missing required ui.input_latency_ms") {
		t.Fatalf("error = %v, want missing required ui.input_latency_ms", err)
	}
}

func TestAuditUIGates_PlantedRepoPair_RedWithoutKey_GreenWithKey(t *testing.T) {
	redDir := setupCompleteUIRepo(t)
	redManifest := &config.Manifest{UI: &config.UIPolicy{
		InputLatencyMs: 100,
		Routes:         []string{"/"},
		ScreenMatrix:   "screen-matrix.json",
	}}
	_, redErr := flavor.AuditUIGates(redDir, redManifest)
	if redErr == nil || !strings.Contains(redErr.Error(), "ui.input_latency_source missing") {
		t.Fatalf("red repo must fail on missing source, got err = %v", redErr)
	}

	greenDir := setupCompleteUIRepo(t)
	greenManifest := &config.Manifest{UI: &config.UIPolicy{
		InputLatencyMs:     100,
		InputLatencySource: validUISourceFixture,
		Routes:             []string{"/"},
		ScreenMatrix:       "screen-matrix.json",
	}}
	passLine, greenErr := flavor.AuditUIGates(greenDir, greenManifest)
	if greenErr != nil {
		t.Fatalf("green repo must pass, got err = %v", greenErr)
	}
	if !strings.Contains(passLine, "[PASS] UI gates:") || !strings.Contains(passLine, "source="+validUISourceFixture) {
		t.Fatalf("green repo output unexpected: %q", passLine)
	}
}
