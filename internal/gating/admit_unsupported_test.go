// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// committedAdmitRepo commits the standards lockfiles and each marker, with a placeholder body, to
// a fresh hermetic repository, so a real pipeline run finds its tree clean at HEAD and reaches the
// receipt stage. The manifest declares pages-site, whose flavor stage is not applicable, so no
// stage before the receipt fails on the fixture.
func committedAdmitRepo(t *testing.T, markers ...string) string {
	t.Helper()
	dir := newHermeticGitRepo(t)
	writeLockfiles(t, dir)
	writeFile(t, filepath.Join(dir, ".standards.yaml"), pagesSiteManifest)
	for _, marker := range markers {
		writeFile(t, filepath.Join(dir, marker), "placeholder\n")
	}
	treeGit(t, dir, append([]string{"add", "-f", "--", ".standards.yaml", ".standards.lock"}, markers...)...)
	treeGit(t, dir, "commit", "-q", "-m", "admit fixture")
	return dir
}

// gatedRun runs the real pipeline over dir with opts and no signing key, and fails when the run
// carries a receipt signature or left a receipt file: none of these runs may sign one.
func gatedRun(t *testing.T, dir string, opts RunOptions) *PipelineReport {
	t.Helper()
	t.Setenv("PRAETOR_RECEIPT_KEY", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep, err := RunGatedPipeline(ctx, dir, opts)
	if err != nil {
		t.Fatalf("RunGatedPipeline: %v", err)
	}
	if rep.ReceiptSignature != "" {
		t.Fatal("the run carries a receipt signature")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the run wrote a receipt: %v", statErr)
	}
	return rep
}

// receiptOf returns the receipt stage the run recorded last, failing when the run stopped before it.
func receiptOf(t *testing.T, rep *PipelineReport) StageResult {
	t.Helper()
	if len(rep.Stages) == 0 || rep.Stages[len(rep.Stages)-1].Name != stageReceipt {
		t.Fatalf("want every stage through the receipt stage, got %+v", rep.Stages)
	}
	return rep.Stages[len(rep.Stages)-1]
}

// Positive (#648): RunGatedPipeline hands RunOptions.AdmitUnsupported to the receipt stage, so the
// flag `gate run --admit-unsupported` sets admits a committed Meson root through the real stages:
// admitted, marked admitted unverified, the receipt stage not applicable and naming Meson, and no
// receipt written.
func TestRunGatedPipeline_Positive_AdmitUnsupportedAdmitsMesonRoot(t *testing.T) {
	rep := gatedRun(t, committedAdmitRepo(t, "meson.build"), RunOptions{AdmitUnsupported: true})
	if rep.Status != StatusAdmitted || !rep.AdmittedUnverified || !rep.WorktreeClean {
		t.Fatalf("status %s, admitted unverified %v, clean %v; want an admitted, unverified run on a clean tree: %+v",
			rep.Status, rep.AdmittedUnverified, rep.WorktreeClean, rep.Stages)
	}
	got := receiptOf(t, rep)
	if got.Status != StageNotApplicable {
		t.Fatalf("receipt stage = %s %q, want not applicable", got.Status, got.Message)
	}
	for _, want := range []string{"meson (meson.build)", admittedUnsupportedNote} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("receipt reason %q does not name %q", got.Message, want)
		}
	}
}

// Negative: without the flag the same committed Meson root is rejected at the receipt stage, and
// with it a root that also holds a go.mod is still rejected, by its failing Go prefetch: the flag
// turns no failed stage into an admission and admits only a root holding neither toolchain marker.
// The receipt stage's own go.mod refusal under the flag is
// TestRunReceiptStage_Negative_AdmitUnsupportedKeepsToolchainRefusals; reaching it here would run
// govulncheck, which queries the vulnerability database.
func TestRunGatedPipeline_Negative_AdmitUnsupportedIsNeededAndNarrow(t *testing.T) {
	strict := gatedRun(t, committedAdmitRepo(t, "meson.build"), RunOptions{})
	got := receiptOf(t, strict)
	if strict.Status != StatusRejected || strict.AdmittedUnverified || got.Status != StageFailed ||
		!strings.Contains(got.Message, "meson (meson.build)") || strings.Contains(got.Message, admittedUnsupportedNote) {
		t.Fatalf("without the flag: status %s, admitted unverified %v, receipt %s %q; want the receipt stage failed naming meson",
			strict.Status, strict.AdmittedUnverified, got.Status, got.Message)
	}

	withGo := gatedRun(t, committedAdmitRepo(t, "go.mod", "meson.build"), RunOptions{AdmitUnsupported: true})
	last := withGo.Stages[len(withGo.Stages)-1]
	if withGo.Status != StatusRejected || withGo.AdmittedUnverified || last.Name != stagePrefetch || !last.Failed() {
		t.Fatalf("go.mod under the flag: status %s, admitted unverified %v; want rejected at the failed Go prefetch: %+v",
			withGo.Status, withGo.AdmittedUnverified, withGo.Stages)
	}
	for _, s := range withGo.Stages {
		if strings.Contains(s.Message, admittedUnsupportedNote) {
			t.Errorf("stage %q of a go.mod root reads as admitted: %q", s.Name, s.Message)
		}
	}
}

// Boundary: under the flag a dry run is recorded as a dry run, not as an admission, and a
// Cargo.toml without its Cargo.lock holds no toolchain marker, so the flag admits it and names the
// missing lockfile.
func TestRunGatedPipeline_Boundary_AdmitUnsupportedDryRunAndUnlockedCargo(t *testing.T) {
	dry := gatedRun(t, committedAdmitRepo(t, "meson.build"), RunOptions{DryRun: true, AdmitUnsupported: true})
	if got := receiptOf(t, dry); dry.Status != StatusAdmitted || dry.AdmittedUnverified || got.Status != StageSkipped {
		t.Fatalf("dry run: status %s, admitted unverified %v, receipt %s %q; want a skipped receipt, not an admission",
			dry.Status, dry.AdmittedUnverified, got.Status, got.Message)
	}

	unlocked := gatedRun(t, committedAdmitRepo(t, "Cargo.toml"), RunOptions{AdmitUnsupported: true})
	got := receiptOf(t, unlocked)
	want := "cargo without a committed Cargo.lock (Cargo.toml)"
	if unlocked.Status != StatusAdmitted || !unlocked.AdmittedUnverified || got.Status != StageNotApplicable ||
		!strings.Contains(got.Message, want) {
		t.Fatalf("Cargo.toml without Cargo.lock: status %s, admitted unverified %v, receipt %s %q; want admitted naming %q",
			unlocked.Status, unlocked.AdmittedUnverified, got.Status, got.Message, want)
	}
}
