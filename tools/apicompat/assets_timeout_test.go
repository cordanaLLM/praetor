// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// apiCompatibilityAdopterP95 is the measured p95 duration in minutes of the rendered
// api-compatibility job per adopter: 15 green default-branch runs each, read 2026-11-06
// (order praetor-job-timeouts-emitted-assets-20261106). The slowest adopter sets the bound;
// the fastest sets the runaway ceiling of the job-timeout gate.
var apiCompatibilityAdopterP95 = map[string]float64{
	"cordanaLLM/praetor": 1.6, "cordanaLLM/Tribunus": 0.9, "golusoris/goenvoy": 1.8,
	"golusoris/golusoris": 12.3, "lusoris/20-watts-was-enough": 0.7, "lusoris/SnatchArr": 1.0,
	"plurano/amussis": 0.5, "plurano/pertica": 1.1, "tekmora/epitrop": 0.9, "vmafx/vmafx": 3.9,
}

// Positive: the rendered job declares max(5, ceil(2 x slowest p95)). Boundary: the bound sits
// inside every adopter's job-timeout window, at least 1.25 x its p95 and at most
// max(30, 4 x its p95). Negative: a bound sized for the fastest adopter alone (5) would cancel
// the slowest one.
func TestAPICompatibilityJobTimeoutFitsEveryAdopter(t *testing.T) {
	spec, err := ghworkflow.Parse([]byte(Workflow))
	if err != nil {
		t.Fatal(err)
	}
	declared, err := strconv.Atoi(strings.TrimSpace(spec.Jobs["api-compatibility"].TimeoutMinutes.Value))
	if err != nil {
		t.Fatalf("api-compatibility timeout-minutes unreadable: %v", err)
	}
	slowest := 0.0
	for _, p95 := range apiCompatibilityAdopterP95 {
		slowest = math.Max(slowest, p95)
	}
	if want := int(math.Max(5, math.Ceil(2*slowest))); declared != want {
		t.Errorf("api-compatibility timeout-minutes = %d, want max(5, ceil(2 x %.1f)) = %d", declared, slowest, want)
	}
	for repo, p95 := range apiCompatibilityAdopterP95 {
		if float64(declared) < 1.25*p95 || float64(declared) > math.Max(30, 4*p95) {
			t.Errorf("%s: %d minutes outside the job-timeout window for p95 %.1f", repo, declared, p95)
		}
	}
	if 5 >= 1.25*slowest {
		t.Errorf("negative case lost: a 5-minute bound must cancel the slowest adopter (p95 %.1f)", slowest)
	}
}
