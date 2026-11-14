// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/replay"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// modelsReplayDocsSourceLink matches relative Go source links outside the documentation tree.
var modelsReplayDocsSourceLink = regexp.MustCompile(`\]\((?:\.\.?/)[^)]*\.go(?:#[^)]*)?\)`)

// TestModelsReplayBundledCorpus loads pinned repository repairs through the sample manifest path.
func TestModelsReplayBundledCorpus(t *testing.T) {
	flags := replayTestFlags(flag.NewFlagSet("test", flag.ContinueOnError))
	*flags.samples = "bundled"
	flags.arms = append(flags.arms, "arm=reviewer")
	opts, err := buildReplayCLIOptions(t.Context(), flags)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Samples) != 2 {
		t.Fatalf("starter corpus: got %d samples, want 2", len(opts.Samples))
	}
	wantPRs := []int{1837, 1857}
	for i := 0; i < len(opts.Samples) && i < replay.MaxCommitScan; i++ {
		checkBundledReplaySample(t, opts.Samples[i], wantPRs[i])
	}
}

// checkBundledReplaySample requires a pinned sample (merge, base and window) with mapped repairs.
func checkBundledReplaySample(t *testing.T, sample replay.Sample, wantPR int) {
	t.Helper()
	pinned := sample.PR.Number == wantPR && util.ValidGitObjectID(sample.PR.Merge) &&
		util.ValidGitObjectID(sample.PR.Base) && sample.Until.After(sample.Cutoff)
	if !pinned {
		t.Fatalf("unpinned corpus sample: %+v", sample)
	}
	gt, err := replay.GroundTruth(t.Context(), "../..", sample.PR, sample.Cutoff, sample.Until)
	if err != nil || len(gt.Hunks) == 0 || len(gt.Fixes) == 0 {
		t.Fatalf("corpus PR %d has no mapped repairs: %+v %v", sample.PR.Number, gt, err)
	}
}

// TestModelsReplayBundledCorpusBases rejects unpinned bundled bases while preserving custom refs.
func TestModelsReplayBundledCorpusBases(t *testing.T) {
	cases := []struct {
		name, base string
		wantErr    bool
	}{
		{"branch", "main", true},
		{"short_sha", "a6fc702", true},
		{"empty", "", true},
		{"invalid_hex", strings.Repeat("g", 40), true},
		{"full_sha", "a6fc702c3b00d97bebeb99fc52900cf2fb259865", false},
	}
	for i := 0; i < len(cases) && i < 5; i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			tc := cases[i]
			raw := []byte(fmt.Sprintf(`[{"pr":{"number":1837,"base":%q}}]`, tc.base))
			if _, err := decodeReplaySamples(raw, true); (err != nil) != tc.wantErr {
				t.Fatalf("bundled base %q: err=%v, wantErr=%t", tc.base, err, tc.wantErr)
			}
			flags := replayTestFlags(flag.NewFlagSet("test", flag.ContinueOnError))
			flags.arms = append(flags.arms, "arm=reviewer")
			*flags.samples = writeFixtureFile(t, t.TempDir(), "samples.json", string(raw))
			opts, err := buildReplayCLIOptions(t.Context(), flags)
			if err != nil || len(opts.Samples) != 1 || opts.Samples[0].PR.Base != tc.base {
				t.Fatalf("custom base %q changed or refused: %+v %v", tc.base, opts.Samples, err)
			}
		})
	}
}

// TestModelsReplayBundledCorpusMissingManifest refuses a missing custom file without substitution.
func TestModelsReplayBundledCorpusMissingManifest(t *testing.T) {
	flags := replayTestFlags(flag.NewFlagSet("test", flag.ContinueOnError))
	flags.arms = append(flags.arms, "arm=reviewer")
	*flags.samples = filepath.Join(t.TempDir(), "missing.json")
	if _, err := buildReplayCLIOptions(t.Context(), flags); err == nil {
		t.Fatal("missing custom manifest silently replaced by corpus")
	}
}

// TestModelsReplayDocsUseCodeSourcePaths rejects source links outside the documentation tree.
func TestModelsReplayDocsUseCodeSourcePaths(t *testing.T) {
	raw, err := contextopt.ReadSnapshot(t.Context(), "../../docs/standards/model-routing-and-fanout.md")
	if err != nil {
		t.Fatal(err)
	}
	if modelsReplayDocsSourceLink.Match(raw) {
		t.Fatal("relative source link outside documentation tree")
	}
}

// TestModelsReplayDocsSourceLinkMatcher pins prohibited links and allowed code-span paths.
func TestModelsReplayDocsSourceLinkMatcher(t *testing.T) {
	cases := []struct {
		name, text string
		wantMatch  bool
	}{
		{"prohibited_parent_link", "[source](../../internal/replay/replay.go)", true},
		{"prohibited_fragment_link", "[source](./models_replay.go#L1)", true},
		{"allowed_code_span", "`internal/replay/replay.go`", false},
		{"empty", "", false},
	}
	for i := 0; i < len(cases) && i < 4; i++ {
		t.Run(cases[i].name, func(t *testing.T) {
			tc := cases[i]
			if got := modelsReplayDocsSourceLink.MatchString(tc.text); got != tc.wantMatch {
				t.Errorf("source link matcher on %q: got %t, want %t", tc.text, got, tc.wantMatch)
			}
		})
	}
}

// TestHelperModelsReplayArm emits a deterministic review verdict JSON when invoked by CLI tests.
func TestHelperModelsReplayArm(t *testing.T) {
	sep := slices.Index(os.Args[:min(len(os.Args), maxCLIArgs)], "--")
	if sep < 0 || sep+4 > len(os.Args) {
		return
	}
	mode, file := os.Args[sep+1], os.Args[sep+2]
	var line int
	if _, err := fmt.Sscanf(os.Args[sep+3], "%d", &line); err != nil {
		os.Exit(2)
	}
	verdict := replay.VerdictChangesRequested
	if mode == "flip" && len(os.Args) > sep+5 && strings.Contains(filepath.Base(os.Args[sep+5]), "draft-b") {
		verdict = replay.VerdictApprove
	}
	payload := replay.ArmVerdict{
		Verdict:  verdict,
		Findings: []replay.Finding{{File: file, Line: line, Summary: "found"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		os.Exit(3)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

// cliReplayArmSpec builds a command template invoking the deterministic CLI arm helper.
func cliReplayArmSpec(t *testing.T, name, mode, file string, line int) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s=%q -test.run=^TestHelperModelsReplayArm$ -- %s %s %d {tree} {drafts}",
		name, exe, mode, file, line)
}

// setupCLIReplayRepo creates a hermetic repaired PR with deterministic commit dates.
func setupCLIReplayRepo(t *testing.T) (string, string, time.Time, time.Time) {
	t.Helper()
	dir := t.TempDir()
	env := testsupport.HermeticGitEnv(t)
	runAt := func(when time.Time, args ...string) string {
		e := env
		if !when.IsZero() {
			ts := when.UTC().Format(time.RFC3339)
			e = append(append([]string(nil), env...), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
		}
		ctx, err := util.WithCommandEnvironment(t.Context(), e)
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := util.RunGit(runCtx, dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	t0 := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	runAt(time.Time{}, "init", "--quiet", "-b", "main")
	writeFixtureFile(t, dir, "auth.go", "package auth\nfunc Check() bool { return true }\n")
	runAt(t0, "add", "-A")
	runAt(t0, "commit", "--quiet", "-m", "seed")

	cutoff := t0.Add(time.Hour)
	writeFixtureFile(t, dir, "auth.go", "package auth\nfunc Check(u string) bool { return len(u) >= 0 }\n")
	runAt(cutoff, "add", "-A")
	runAt(cutoff, "commit", "--quiet", "-m", "PR #12")
	mergeSHA := runAt(cutoff, "rev-parse", "HEAD")

	until := cutoff.Add(time.Hour)
	writeFixtureFile(t, dir, "auth.go", "package auth\nfunc Check(u string) bool { return len(u) > 0 }\n")
	runAt(until, "add", "-A")
	runAt(until, "commit", "--quiet", "-m", "Fix empty user in #12")
	return dir, mergeSHA, cutoff, until
}

// TestModelsReplayCLISuccessWritesRowsAndJSON pins exit 0, JSON output, and outcome log rows.
func TestModelsReplayCLISuccessWritesRowsAndJSON(t *testing.T) {
	repoDir, mergeSHA, cutoff, until := setupCLIReplayRepo(t)
	draftA := writeFixtureFile(t, t.TempDir(), "draft-a.json", `{"a":1}`)
	draftB := writeFixtureFile(t, t.TempDir(), "draft-b.json", `{"b":2}`)
	logPath := filepath.Join(t.TempDir(), "outcomes.jsonl")
	args := []string{
		"replay",
		"--repo=" + repoDir,
		"--pr=12",
		"--merge=" + mergeSHA,
		"--cutoff=" + cutoff.Format(time.RFC3339),
		"--until=" + until.Format(time.RFC3339),
		"--suite=cli-suite",
		"--log=" + logPath,
		"--draft=" + draftA,
		"--draft=" + draftB,
		"--arm", cliReplayArmSpec(t, "stable", "stable", "auth.go", 2),
		"--arm", cliReplayArmSpec(t, "flaky", "flip", "auth.go", 2),
		"--json",
	}
	stdout, _, err := runOutcomeCLI(t, args)
	if err != nil {
		t.Fatalf("models replay failed: %v", err)
	}
	assertCLIReplayJSONAndOutcomes(t, stdout, logPath, cutoff)
}

// assertCLIReplayJSONAndOutcomes checks CLI report decoding and outcome readback.
func assertCLIReplayJSONAndOutcomes(t *testing.T, stdout, logPath string, cutoff time.Time) {
	t.Helper()
	var rep replay.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("decode JSON report: %v\n%s", err, stdout)
	}
	if len(rep.Arms) != 2 || rep.Arms[0].OrderSensitive || !rep.Arms[1].OrderSensitive {
		t.Fatalf("unexpected arm scores: %+v", rep.Arms)
	}
	assertCLIReplayFirstRow(t, logPath, cutoff)
}

// assertCLIReplayFirstRow checks the first persisted CLI arm outcome.
func assertCLIReplayFirstRow(t *testing.T, logPath string, cutoff time.Time) {
	t.Helper()
	rows, err := router.ReadOutcomes(t.Context(), logPath)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ReadOutcomes: %+v %v", rows, err)
	}
	if rows[0].Arm != "stable" || rows[0].Suite != "cli-suite" || rows[0].SampleCount != 1 {
		t.Fatalf("row 0 metadata mismatch: %+v", rows[0])
	}
	if rows[0].Cutoff == nil || !rows[0].Cutoff.Equal(cutoff) {
		t.Fatalf("row 0 cutoff mismatch: %+v", rows[0])
	}
}

// TestModelsReplayCLIExitCodesRefusedAndUsage pins exit code 1 on refused replay and 2 on usage errors.
func TestModelsReplayCLIExitCodesRefusedAndUsage(t *testing.T) {
	repoDir, mergeSHA, cutoff, until := setupCLIReplayRepo(t)
	logPath := filepath.Join(t.TempDir(), "outcomes.jsonl")
	arm := cliReplayArmSpec(t, "stable", "stable", "auth.go", 2)

	// Refused replay: cutoff == until leaves zero post-cutoff commits -> exit 1.
	_, _, refusedErr := runOutcomeCLI(t, []string{
		"replay", "--repo=" + repoDir, "--pr=12", "--merge=" + mergeSHA,
		"--cutoff=" + until.Format(time.RFC3339), "--until=" + until.Format(time.RFC3339),
		"--log=" + logPath, "--arm", arm,
	})
	var errBuf bytes.Buffer
	if refusedErr == nil || commandExitCode(&errBuf, refusedErr) != 1 {
		t.Fatalf("refused replay must exit 1, got err=%v code=%d", refusedErr, commandExitCode(&errBuf, refusedErr))
	}

	usageCases := [][]string{
		{"replay"},
		{"replay", "--unknown-flag"},
		{"replay", "extra", "--pr=12"},
		{"replay", "--pr=0", "--merge=" + mergeSHA, "--cutoff=" + cutoff.Format(time.RFC3339), "--until=" + until.Format(time.RFC3339), "--arm", arm},
		{"replay", "--pr=12", "--merge=" + mergeSHA, "--cutoff=not-rfc3339", "--until=" + until.Format(time.RFC3339), "--arm", arm},
		{"replay", "--pr=12", "--merge=" + mergeSHA, "--cutoff=" + until.Format(time.RFC3339), "--until=" + cutoff.Format(time.RFC3339), "--arm", arm},
		{"replay", "--pr=12", "--merge=" + mergeSHA, "--cutoff=" + cutoff.Format(time.RFC3339), "--until=" + until.Format(time.RFC3339)},
		{"replay", "--config=routing.yaml", "--pr=12"},
	}
	for _, args := range usageCases {
		_, _, err := runOutcomeCLI(t, args)
		if err == nil || commandExitCode(&errBuf, err) != 2 {
			t.Errorf("args %v: expected exit code 2, got err=%v", args, err)
		}
	}
}

// TestReviewCLISampleSuite accepts a bounded JSON array of historical PR samples.
func TestReviewCLISampleSuite(t *testing.T) {
	repo, merge, cutoff, until := setupCLIReplayRepo(t)
	sample := replay.Sample{PR: replay.PRSpec{Number: 12, Merge: merge}, Cutoff: cutoff, Until: until}
	data, err := json.Marshal([]replay.Sample{sample, sample})
	if err != nil {
		t.Fatal(err)
	}
	path := writeFixtureFile(t, t.TempDir(), "samples.json", string(data))
	output, _, err := runOutcomeCLI(t, []string{"replay", "--repo=" + repo, "--base=main", "--samples=" + path, "--arm", cliReplayArmSpec(t, "stable", "stable", "auth.go", 2), "--log=" + filepath.Join(t.TempDir(), "rows.jsonl"), "--json"})
	if err != nil {
		t.Fatal(err)
	}
	var rep replay.Report
	if err := json.Unmarshal([]byte(output), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SampleCount != 2 {
		t.Fatalf("suite truncated: %+v", rep)
	}
}

// TestReviewCLIReplayBudget includes both orders for every configured arm and sample.
func TestReviewCLIReplayBudget(t *testing.T) {
	flags := replayTestFlags(flag.NewFlagSet("test", flag.ContinueOnError))
	flags.arms = append(flags.arms, "a=x", "b=x")
	want := replay.DefaultPreparationTimeout + 4*replay.DefaultArmTimeout
	if got := modelsInvocationTimeout("replay", flags); got != want+replay.DefaultArmTimeout {
		t.Fatalf("budget %s, want %s", got, want)
	}
	*flags.samples = "samples.json"
	if got := modelsInvocationTimeout("replay", flags); got != replay.MaxCommitScan*want+replay.DefaultArmTimeout {
		t.Fatalf("suite budget %s", got)
	}
	if got := modelsInvocationTimeout("list", flags); got != modelsTimeout {
		t.Fatalf("list budget %s", got)
	}
}

// TestReviewHelperArgumentBound refuses a separator beyond the scalar argument scan bound.
func TestReviewHelperArgumentBound(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestHelperModelsReplayArm$"}
	args = append(args, slices.Repeat([]string{"padding"}, maxCLIArgs)...)
	args = append(args, "--", "stable", "file", "2", "tree", "draft")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := util.RunCommandBytes(ctx, t.TempDir(), exe, 4096, args...)
	// The first line is go test's verdict; a -cover run adds a coverage line after it.
	first, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
	if err != nil || first != "PASS" {
		t.Fatalf("unbounded helper scan: %s %v", res.Stdout, err)
	}
}

// TestReviewCLISampleSuiteRefusals rejects malformed, ambiguous and over-limit manifests.
func TestReviewCLISampleSuiteRefusals(t *testing.T) {
	cases := []string{"{}", "[]", "[] {}", `[{"unknown":1}]`, "[" + strings.Repeat("{},", replay.MaxCommitScan) + "{}]"}
	for i := 0; i < len(cases) && i < 5; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags := replayTestFlags(fs)
			flags.arms = append(flags.arms, "arm=cmd")
			*flags.samples = writeFixtureFile(t, t.TempDir(), "samples.json", cases[i])
			if _, err := buildReplayCLIOptions(t.Context(), flags); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	replayTestFlags(fs)
	if err := fs.Set("samples", "samples.json"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("pr", "1"); err != nil {
		t.Fatal(err)
	}
	if err := validateModelReplayFlags(fs, "replay"); err == nil {
		t.Fatal("ambiguous sample flags accepted")
	}
}

// replayTestFlags registers the shared merge-check flags first, as runModels does.
func replayTestFlags(fs *flag.FlagSet) *modelReplayFlags {
	return addModelReplayFlags(fs, addModelMergeFlags(fs))
}

// TestModelsSharedDraftFlags keeps --draft and --json registered once for merge-check and replay, and refuses
// each action's own flags on the other action with exit status 2.
func TestModelsSharedDraftFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags := replayTestFlags(fs)
	for _, name := range []string{"draft", "json"} {
		if err := fs.Set(name, map[string]string{"draft": "d.json", "json": "true"}[name]); err != nil {
			t.Fatal(err)
		}
	}
	if len(*flags.drafts) != 1 || !*flags.jsonOut {
		t.Fatalf("replay does not read the shared flags: %v %v", *flags.drafts, *flags.jsonOut)
	}
	for _, action := range []string{"replay", "merge-check"} {
		if err := validateMergeAction(fs, action, 1); err != nil {
			t.Fatalf("%s refused shared flags: %v", action, err)
		}
		if err := validateModelReplayFlags(fs, action); err != nil {
			t.Fatalf("%s refused shared flags: %v", action, err)
		}
	}
	if err := validateMergeAction(fs, "route", 1); err == nil {
		t.Fatal("route accepted --draft")
	}
	for _, args := range [][]string{
		{"replay", "--final", "f.json"},
		{"merge-check", "--pr", "1", "--draft", "d.json", "--final", "f.json"},
	} {
		if _, _, err := runOutcomeCLI(t, args); exitCode(err) != 2 {
			t.Fatalf("%v: exit %d, want 2: %v", args, exitCode(err), err)
		}
	}
}
