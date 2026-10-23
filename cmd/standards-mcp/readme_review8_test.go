// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestReview8MCPKeptRejection(t *testing.T) {
	v := adopt.BaselineRatchet{File: ".standards-baseline.json", Recorded: 3, Active: 5, Unbaselined: 2}
	r := &adopt.AdoptReport{BaselineStatus: "existing", BaselineRatchets: []adopt.BaselineRatchet{v}}
	out := string(formatAdoptMCPResult(r, false))
	if strings.Count(out, v.Line()) != 1 {
		t.Errorf("kept rejection missing or duplicated: %s", out)
	}
	r.BaselineStatus = "failed"
	out = string(formatAdoptMCPResult(r, false))
	if strings.Contains(out, "active infractions") {
		t.Errorf("ratchet printed after failed status: %s", out)
	}
}
