// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEfficiencyTo_Outcomes_JoinsRunIdentityOrFails(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsPath, []byte(efficiencyRecords), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "outcomes.jsonl")
	if _, _, err := runOutcomeCLI(t, outcomeArgsWith(log, nil, "--branch=feat/main")); err != nil {
		t.Fatal(err)
	}
	var js bytes.Buffer
	if err := runEfficiencyTo([]string{"--forge-records=" + prsPath, "--outcomes=" + log, "--json"}, &js); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Units []struct {
			ResolvedModel string `json:"resolved_model"`
			IdentityKey   string `json:"identity_key"`
			EstimateError string `json:"estimate_error"`
		} `json:"units"`
	}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil || len(decoded.Units) != 1 {
		t.Fatalf("json: %v %s", err, js.String())
	}
	unit := decoded.Units[0]
	if unit.ResolvedModel != "claude-3-5-sonnet" || !strings.HasPrefix(unit.IdentityKey, "sha256:") || unit.EstimateError != "$+0.0100" {
		t.Fatalf("--outcomes must join the run identity onto its unit: %+v", unit)
	}
	var out bytes.Buffer
	if err := runEfficiencyTo([]string{"--forge-records=" + prsPath, "--outcomes=" + filepath.Join(dir, "absent.jsonl")}, &out); err == nil {
		t.Fatal("an explicit --outcomes path that does not exist must fail")
	}
}
