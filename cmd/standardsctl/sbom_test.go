// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// #573: sbom reads a Go module's go.mod only, but neither its help nor the command list said
// so, and a repository without a root go.mod failed with a raw stat error.

func TestRunSBOM_Positive_HelpAndCommandListSayGoOnly(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return runSBOM([]string{tok}) })
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("sbom %s must be answered as help, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl sbom", "Go only", "sbom notices")
	}
	usage, err := captureStdout(t, func() error { printUsage(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, usage, "SBOM of a Go module (go.mod only)")
}

func TestRunSBOM_Negative_NoGoModIsAClearRefusal(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "Cargo.toml", "[workspace]\n")
	_, err := captureStdout(t, func() error { return runSBOM([]string{"--path", dir}) })
	if !errors.Is(err, supplychain.ErrNoGoModule) {
		t.Fatalf("error = %v, want %v", err, supplychain.ErrNoGoModule)
	}
	mustErrContain(t, err, "no go.mod in "+dir)
	if strings.Contains(err.Error(), "statat") {
		t.Errorf("the raw stat error must not be the message: %v", err)
	}
}

// A Go module root still generates, and a trailing -h is still help rather than a run.
func TestRunSBOM_Boundary_GoModuleRootStillGenerates(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "go.mod", "module example.com/app\n\ngo 1.27\n")
	out, err := captureStdout(t, func() error { return runSBOM([]string{"--path", dir, "--module-version", "v1.0.0"}) })
	if err != nil {
		t.Fatalf("sbom on a Go module root: %v", err)
	}
	mustContain(t, out, `"bomFormat": "CycloneDX"`, `"name": "example.com/app"`)
	if err := runSBOM([]string{"--path", dir, "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("a trailing -h must be answered as help, got %v", err)
	}
}
