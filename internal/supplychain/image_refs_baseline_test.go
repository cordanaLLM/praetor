// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
)

var testSupplychainToday = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func TestUnexcusedImageRefInfractions_FloatingRef(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine\n",
	})
	got, err := UnexcusedImageRefInfractions(t.Context(), root, nil, testSupplychainToday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 infraction, got %d: %+v", len(got), got)
	}
	want := baseline.Infraction{
		RuleID:      ImageRefRuleID,
		FilePath:    "Dockerfile",
		LineNumber:  1,
		Message:     "golang:1.27-alpine",
		Fingerprint: "Dockerfile:HISS-11:image:golang:1.27-alpine",
	}
	if got[0] != want {
		t.Fatalf("got infraction %+v, want %+v", got[0], want)
	}
}

func TestUnexcusedImageRefInfractions_UnresolvableRef(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "ARG BASE_IMAGE=alpine:3.19\nFROM ${BASE_IMAGE}\n",
	})
	got, err := UnexcusedImageRefInfractions(t.Context(), root, nil, testSupplychainToday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 infractions for unresolvable ref, got %d: %+v", len(got), got)
	}
}

func TestUnexcusedImageRefInfractions_LiveException(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine\n",
	})
	exceptions := []config.Exception{
		{
			Rule:     config.ExceptionRuleSupplyChain,
			Path:     "Dockerfile",
			Category: "A",
			Expires:  "2026-12-31",
			Reason:   "planned upgrade",
		},
	}
	got, err := UnexcusedImageRefInfractions(t.Context(), root, exceptions, testSupplychainToday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 infractions for live exception, got %d: %+v", len(got), got)
	}
}

func TestUnexcusedImageRefInfractions_ExpiredException(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine\n",
	})
	exceptions := []config.Exception{
		{
			Rule:    config.ExceptionRuleSupplyChain,
			Path:    "Dockerfile",
			Expires: "2026-01-01",
			Reason:  "expired exception",
		},
	}
	got, err := UnexcusedImageRefInfractions(t.Context(), root, exceptions, testSupplychainToday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 infraction for expired exception, got %d: %+v", len(got), got)
	}
	if got[0].RuleID != ImageRefRuleID || got[0].FilePath != "Dockerfile" {
		t.Fatalf("unexpected infraction: %+v", got[0])
	}
}

func TestRecordImageRefBaseline_Preserved(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile": "FROM golang:1.27-alpine\n",
	})
	res, err := RecordImageRefBaseline(t.Context(), ImageBaselineRecordOptions{
		Root:          root,
		AllowIncrease: true,
		Reason:        "adoption baseline",
		Today:         testSupplychainToday,
	})
	if err != nil {
		t.Fatalf("RecordImageRefBaseline failed: %v", err)
	}
	if res.Recorded != 1 {
		t.Fatalf("expected 1 recorded, got %d", res.Recorded)
	}
	b, err := baseline.LoadBaseline(filepath.Join(root, BaselineFile))
	if err != nil {
		t.Fatalf("load baseline failed: %v", err)
	}
	if b.Count() != 1 || b.Infractions[0].RuleID != ImageRefRuleID {
		t.Fatalf("unexpected baseline contents: %+v", b)
	}
}
