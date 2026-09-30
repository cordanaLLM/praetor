package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

func TestGateRun_Positive_JSONEmitsParseableJSONOnly(t *testing.T) {
	dir := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--json", "--dry-run", "--path=" + dir})
	})
	mustErrContain(t, err, "rejected by gating pipeline")
	if strings.Contains(out, "=== Praetor Anti-Direct-Merge Gating Pipeline ===") {
		t.Errorf("gate run --json must not print banner to stdout, got: %s", out)
	}
	if strings.Contains(out, "Target Repository:") {
		t.Errorf("gate run --json must not print target repository line to stdout, got: %s", out)
	}
	var rep gating.PipelineReport
	if unmarshalErr := json.Unmarshal([]byte(out), &rep); unmarshalErr != nil {
		t.Fatalf("gate run --json output must be parseable JSON: %v\nOutput: %s", unmarshalErr, out)
	}
	if rep.Status != gating.StatusRejected {
		t.Errorf("expected StatusRejected, got %s", rep.Status)
	}
}

func TestGateRun_Negative_JSONRejectionIsAnError(t *testing.T) {
	// A directory without a manifest fails the prefetch stage: the pipeline reports
	// REJECTED and the JSON path must still exit non-zero.
	dir := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--json", "--dry-run", "--path=" + dir})
	})
	mustErrContain(t, err, "rejected by gating pipeline")
	mustContain(t, out, `"REJECTED"`)

	// The human-readable path rejects as well.
	out, err = captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--dry-run", "--path=" + dir})
	})
	mustErrContain(t, err, "rejected by gating pipeline")
	mustContain(t, out, "Pipeline Result: REJECTED")
}

func TestGateRun_Negative_PositionalArguments(t *testing.T) {
	dir := t.TempDir()
	cases := [][]string{
		{"run", "--path=" + dir, "extra"},
		{"run", dir},
		{"verify", "--path=" + dir, "extra"},
		{"keygen", "extra"},
	}
	for _, args := range cases {
		_, err := captureStdout(t, func() error { return dispatchCommand("gate", args) })
		mustErrContain(t, err, "no positional arguments")
	}
	_, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{"bogus"}) })
	mustErrContain(t, err, "unknown gate subcommand")
}

func TestGateRun_Negative_BareAndFlagOnlyPrintsUsage(t *testing.T) {
	// Bare "gate" requires an explicit subcommand and prints usage.
	out, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{})
	})
	if err == nil {
		t.Fatal("bare gate must return an error")
	}
	mustContain(t, out, "usage: praetorctl gate <run|verify|deadline|keygen>")
	if strings.Contains(out, "=== Praetor Anti-Direct-Merge Gating Pipeline ===") {
		t.Errorf("bare gate must not run pipeline, got: %s", out)
	}

	// Flag-first "gate --json" without subcommand prints usage and errors.
	out, err = captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"--json"})
	})
	if err == nil {
		t.Fatal("gate --json without subcommand must return an error")
	}
	mustContain(t, out, "usage: praetorctl gate <run|verify|deadline|keygen>")
	if strings.Contains(out, "=== Praetor Anti-Direct-Merge Gating Pipeline ===") {
		t.Errorf("gate --json must not run pipeline, got: %s", out)
	}

	// "gate -h" prints usage and returns flag.ErrHelp.
	out, err = captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"-h"})
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("gate -h: expected flag.ErrHelp, got %v", err)
	}
	mustContain(t, out, "usage: praetorctl gate <run|verify|deadline|keygen>")

	for _, sub := range []string{"run", "verify", "deadline", "keygen"} {
		_, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{sub, "-h"}) })
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("gate %s -h: expected flag.ErrHelp, got %v", sub, err)
		}
	}
}

func TestGateRun_Boundary_SignalCancellationPropagates(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	origRootCtx := rootCtx
	rootCtx = cancelledCtx
	defer func() { rootCtx = origRootCtx }()

	var observedCtx context.Context
	origPipeline := gatedPipeline
	gatedPipeline = func(ctx context.Context, path string, _ gating.RunOptions) (*gating.PipelineReport, error) {
		observedCtx = ctx
		return &gating.PipelineReport{Status: gating.StatusRejected}, nil
	}
	defer func() { gatedPipeline = origPipeline }()

	dir := t.TempDir()
	_, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--dry-run", "--path=" + dir})
	})
	if err == nil {
		t.Fatal("expected error on rejected gating pipeline")
	}
	if observedCtx == nil {
		t.Fatal("gatedPipeline was not called")
	}
	if observedCtx.Err() == nil {
		t.Fatal("expected gating pipeline context to be cancelled via root context")
	}
}
