// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// admitRepo gives a fresh directory the standards lockfiles and markers, each with a placeholder
// body: the gate reads only whether a marker exists.
func admitRepo(t *testing.T, markers ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeLockfiles(t, dir)
	for _, marker := range markers {
		writeFile(t, filepath.Join(dir, marker), "placeholder\n")
	}
	return dir
}

// receiptVerdict runs the toolchain stages and then the receipt stage over a clean tree, with
// admitUnsupported as given and no signing key, and returns the receipt stage's recorded result
// and the error that stopped the pipeline, if any.
func receiptVerdict(t *testing.T, cfg *stageConfig, admitUnsupported bool) (StageResult, error) {
	t.Helper()
	t.Setenv("PRAETOR_RECEIPT_KEY", "")
	cfg.admitUnsupported = admitUnsupported
	cfg.rep.WorktreeClean = true
	if _, err := runToolchainStages(t, cfg); err != nil {
		t.Fatalf("toolchain stages: %v", err)
	}
	err := executeStage(t.Context(), stage{stageReceipt, runReceiptStage}, cfg)
	got := cfg.rep.Stages[len(cfg.rep.Stages)-1]
	if got.Name != stageReceipt {
		t.Fatalf("last recorded stage = %q, want the receipt stage", got.Name)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.repoDir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a run with no toolchain stage wrote a receipt: %v", statErr)
	}
	return got, err
}

// Positive (#648): with --admit-unsupported a Meson root, and a root the gate recognises nothing
// at, is admitted without a receipt: the receipt stage is not applicable, names what the gate
// could not verify and why no receipt exists, the pipeline carries on, and the report says the
// run was admitted unverified. No signing key is needed.
func TestRunReceiptStage_Positive_AdmitUnsupportedNamesUnverifiedLanguages(t *testing.T) {
	cases := map[string]struct {
		markers []string
		want    string
	}{
		"meson":     {markers: []string{"meson.build"}, want: "unsupported languages at the repository root: meson (meson.build)"},
		"cmake":     {markers: []string{"CMakeLists.txt"}, want: "unsupported languages at the repository root: cmake (CMakeLists.txt)"},
		"no marker": {want: "no language marker the gate recognises is at the repository root"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, recorded := newTestConfig(t, admitRepo(t, tc.markers...), false)
			got, err := receiptVerdict(t, cfg, true)
			if err != nil {
				t.Fatalf("an admitted run stopped the pipeline: %v", err)
			}
			if got.Status != StageNotApplicable {
				t.Fatalf("receipt stage = %s %q, want not applicable", got.Status, got.Message)
			}
			for _, want := range []string{tc.want, "no Exit-0 receipt is signed", admittedUnsupportedNote} {
				if !strings.Contains(got.Message, want) {
					t.Errorf("reason %q does not name %q", got.Message, want)
				}
			}
			if !cfg.rep.AdmittedUnverified || cfg.rep.ReceiptSignature != "" {
				t.Errorf("report: admitted unverified %v, signature %q", cfg.rep.AdmittedUnverified, cfg.rep.ReceiptSignature)
			}
			if len(*recorded) != 0 {
				t.Errorf("commands ran for a root holding no toolchain marker: %+v", *recorded)
			}
		})
	}
}

// Negative: the flag relaxes nothing where the gate runs a toolchain. A Cargo.lock whose stages
// could not run, and a go.mod whose stages all skipped, are refused with it as without it; and a
// Meson root is refused without the flag, which CI and the gatekeeper do not pass.
func TestRunReceiptStage_Negative_AdmitUnsupportedKeepsToolchainRefusals(t *testing.T) {
	cargo := seedCargoWorkspace(t, t.TempDir(), true)
	cases := map[string]struct {
		dir    string
		admit  bool
		absent []string
		want   string
	}{
		"Cargo.lock, cargo absent": {dir: cargo, admit: true, absent: []string{"cargo", "cargo-audit"},
			want: "Cargo.lock is present, but no Cargo stage ran here"},
		"meson without the flag": {dir: admitRepo(t, "meson.build"),
			want: "unsupported languages at the repository root: meson (meson.build)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, _ := newTestConfig(t, tc.dir, false)
			cfg.lookPath = lookPathWithout(tc.absent...)
			got, err := receiptVerdict(t, cfg, tc.admit)
			if !errors.Is(err, ErrNothingVerified) || got.Status != StageFailed || !strings.Contains(got.Message, tc.want) {
				t.Fatalf("want the nothing-verified refusal naming %q, got %s %q (%v)", tc.want, got.Status, got.Message, err)
			}
			if cfg.rep.AdmittedUnverified || strings.Contains(got.Message, admittedUnsupportedNote) {
				t.Errorf("a refused run reads as admitted: %+v", cfg.rep)
			}
		})
	}
	goRepo := admitRepo(t, "go.mod", "meson.build")
	cfg, _ := newTestConfig(t, goRepo, false)
	cfg.admitUnsupported = true
	cfg.rep.WorktreeClean = true
	if _, err := runReceiptStage(t.Context(), cfg); !errors.Is(err, ErrNothingVerified) || cfg.rep.AdmittedUnverified {
		t.Fatalf("a go.mod whose stages all skipped must be refused under the flag, got %v", err)
	}
}

// Boundary: a dry run mints nothing and is recorded as a dry run whatever the flag says, and a
// Cargo.toml without its Cargo.lock is a language the gate cannot run, so the flag admits it and
// names the missing lockfile.
func TestRunReceiptStage_Boundary_AdmitUnsupportedDryRunAndUnlockedCargo(t *testing.T) {
	dry, _ := newTestConfig(t, admitRepo(t, "meson.build"), true)
	got, err := receiptVerdict(t, dry, true)
	if err != nil || got.Status != StageSkipped || got.Message != "dry run: no Exit-0 receipt minted" || dry.rep.AdmittedUnverified {
		t.Fatalf("dry run: %s %q (%v), admitted unverified %v", got.Status, got.Message, err, dry.rep.AdmittedUnverified)
	}
	unlocked, _ := newTestConfig(t, seedCargoWorkspace(t, t.TempDir(), false), false)
	got, err = receiptVerdict(t, unlocked, true)
	want := "unsupported languages at the repository root: cargo without a committed Cargo.lock (Cargo.toml)"
	if err != nil || got.Status != StageNotApplicable || !strings.Contains(got.Message, want) {
		t.Fatalf("Cargo.toml without Cargo.lock: %s %q (%v), want not applicable naming %q", got.Status, got.Message, err, want)
	}
}
