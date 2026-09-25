package main

import (
	"errors"
	"flag"
	"testing"
)

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

func TestGateRun_Boundary_DefaultSubcommandAndHelp(t *testing.T) {
	dir := t.TempDir()
	// Flags without a subcommand select "run".
	_, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"--json", "--dry-run", "--path=" + dir})
	})
	mustErrContain(t, err, "rejected by gating pipeline")

	for _, sub := range []string{"run", "verify", "deadline", "keygen"} {
		_, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{sub, "-h"}) })
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("gate %s -h: expected flag.ErrHelp, got %v", sub, err)
		}
	}
}
