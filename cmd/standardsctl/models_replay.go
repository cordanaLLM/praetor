// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/replay"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

// modelReplayFlags holds CLI flags for `praetorctl models replay`.
type modelReplayFlags struct {
	pr      *int
	merge   *string
	cutoff  *string
	until   *string
	suite   *string
	log     *string
	repo    *string
	base    *string
	jsonOut *bool
	samples *string
	arms    repeatedStringFlag
	drafts  *repeatedStringFlag
}

// bundledReplaySamples pins two merged repository PRs with later blame-attributed repairs.
// Cutoffs are the first-parent commits immediately before the repair, keeping mapping windows bounded.
// Bases pin the first-parent tips at Until, including the post-merge repairs GroundTruth scans.
const bundledReplaySamples = `[
  {"pr":{"number":1837,"merge":"1cd4af6ad3575b328a37b4475446ed28270d916b","base":"a6fc702c3b00d97bebeb99fc52900cf2fb259865"},
   "cutoff":"2026-11-09T17:01:30Z","until":"2026-11-09T21:24:19Z"},
  {"pr":{"number":1857,"merge":"a2fa803aa1ba9450f08edb2e0073df5729f31114","base":"ff4e892f319ae06a2a283f04d9c947517a686d6c"},
   "cutoff":"2026-11-09T21:24:19Z","until":"2026-11-10T19:57:25Z"}
]`

// replayOnlyFlags enumerates flags accepted exclusively by `models replay`; --draft and --json are shared with merge-check.
var replayOnlyFlags = map[string]bool{
	"pr": true, "merge": true, "cutoff": true, "until": true,
	"arm": true, "suite": true, "log": true,
	"repo": true, "base": true, "samples": true,
}

// addModelReplayFlags registers the replay-only flags on fs and reads --draft and --json from the merge-check
// registration, so one FlagSet carries each flag name once.
func addModelReplayFlags(fs *flag.FlagSet, shared *modelMergeFlags) *modelReplayFlags {
	f := &modelReplayFlags{
		pr:      fs.Int("pr", 0, "models replay: pull request number (> 0)"),
		merge:   fs.String("merge", "", "models replay: pull request merge commit SHA or start..end range"),
		cutoff:  fs.String("cutoff", "", "models replay: RFC3339 cutoff timestamp"),
		until:   fs.String("until", "", "models replay: RFC3339 end timestamp for post-merge ground truth"),
		suite:   fs.String("suite", replay.DefaultSuite, "models replay: evaluation suite name"),
		log:     fs.String("log", router.DefaultOutcomeLogPath, "models replay: outcome JSONL log path"),
		repo:    fs.String("repo", ".", "models replay: repository checkout directory"),
		base:    fs.String("base", "", "models replay: base branch (defaults to repository default branch)"),
		samples: fs.String("samples", "", "models replay: JSON sample file or bundled starter corpus; replaces --pr/--merge/--cutoff/--until/--draft"),
		jsonOut: shared.json,
		drafts:  &shared.drafts,
	}
	fs.Var(&f.arms, "arm", "models replay: named arm command name=<command> (repeatable)")
	return f
}

// modelsParseError maps flag parse errors under `models replay` to exit code 2.
func modelsParseError(args []string, err error) error {
	if isModelsReplayArgs(args) && !errors.Is(err, flag.ErrHelp) {
		return exitStatusError{code: 2}
	}
	return err
}

// isModelsReplayArgs reports whether args targets the `replay` action before `--`.
func isModelsReplayArgs(args []string) bool {
	for i := 0; i < len(args) && i < maxCLIArgs; i++ {
		if args[i] == "--" {
			return false
		}
		if args[i] == "replay" {
			return true
		}
	}
	return false
}

// validateModelReplayFlags rejects replay flags on other actions and catalog flags on replay.
func validateModelReplayFlags(fs *flag.FlagSet, action string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if replayOnlyFlags[f.Name] && action != "replay" {
			err = fmt.Errorf("--%s requires models replay", f.Name)
			return
		}
		if replaySampleFlagConflict(fs, action, f.Name) {
			err = errors.New("--samples cannot be combined with per-sample flags")
			return
		}
		if action == "replay" && (f.Name == "config" || f.Name == "discover-local" || f.Name == "local-endpoints") {
			err = fmt.Errorf("--%s does not apply to models replay", f.Name)
		}
	})
	return err
}

// replaySampleFlagConflict rejects ambiguous per-sample overrides alongside a sample manifest.
func replaySampleFlagConflict(fs *flag.FlagSet, action, name string) bool {
	if action != "replay" || fs.Lookup("samples").Value.String() == "" {
		return false
	}
	switch name {
	case "pr", "merge", "cutoff", "until", "draft":
		return true
	}
	return false
}

// modelActionUsage keeps each action's usage exit contract: merge-check and replay report on stderr with status 2.
func modelActionUsage(action string, err error) error {
	if action == "merge-check" {
		return modelMergeError(err)
	}
	return wrapActionUsage(action, err)
}

// wrapActionUsage formats usage errors on stderr with exit code 2 when action is replay.
func wrapActionUsage(action string, err error) error {
	if action != "replay" {
		return err
	}
	return replayUsageError(err)
}

// replayUsageError writes err to stderr and returns exitStatusError{code: 2}.
func replayUsageError(err error) error {
	if _, writeErr := fmt.Fprintf(os.Stderr, "Error: %v\n", err); writeErr != nil {
		return exitStatusError{code: 2}
	}
	return exitStatusError{code: 2}
}

// handleModelsReplay executes `praetorctl models replay`.
func handleModelsReplay(ctx context.Context, flags *modelReplayFlags) error {
	opts, err := buildReplayCLIOptions(ctx, flags)
	if err != nil {
		return replayUsageError(err)
	}
	report, err := replay.Replay(ctx, opts)
	if err != nil {
		return err
	}
	return writeReplayCLIReport(report, opts.OutcomeLog, *flags.jsonOut)
}

// buildReplayCLIOptions validates CLI flags and constructs replay.Options.
func buildReplayCLIOptions(ctx context.Context, flags *modelReplayFlags) (replay.Options, error) {
	if *flags.samples != "" {
		return buildReplaySuiteOptions(ctx, flags)
	}
	if *flags.pr <= 0 {
		return replay.Options{}, errors.New("--pr must be a positive pull request number")
	}
	if strings.TrimSpace(*flags.merge) == "" {
		return replay.Options{}, errors.New("--merge is required")
	}
	cutoff, err := parseReplayTimestamp("--cutoff", *flags.cutoff)
	if err != nil {
		return replay.Options{}, err
	}
	until, err := parseReplayTimestamp("--until", *flags.until)
	if err != nil {
		return replay.Options{}, err
	}
	if until.Before(cutoff) {
		return replay.Options{}, errors.New("--until must not be earlier than --cutoff")
	}
	arms, err := parseReplayArms(flags.arms)
	if err != nil {
		return replay.Options{}, err
	}
	return assembleReplayOptions(flags, cutoff, until, arms)
}

// assembleReplayOptions validates suite/log/draft bounds and returns replay.Options.
func assembleReplayOptions(flags *modelReplayFlags, cutoff, until time.Time, arms []replay.Arm) (replay.Options, error) {
	suite := strings.TrimSpace(*flags.suite)
	if !replay.ValidReplayName(suite) {
		return replay.Options{}, fmt.Errorf("--suite %q is not a valid routing name", *flags.suite)
	}
	logPath := strings.TrimSpace(*flags.log)
	if logPath == "" {
		return replay.Options{}, errors.New("--log must not be empty")
	}
	if len(*flags.drafts) > replay.MaxDrafts {
		return replay.Options{}, fmt.Errorf("at most %d --draft flags are supported", replay.MaxDrafts)
	}
	return replay.Options{
		RepoDir:    *flags.repo,
		Base:       *flags.base,
		Suite:      suite,
		OutcomeLog: logPath,
		Samples: []replay.Sample{{
			PR:     replay.PRSpec{Number: *flags.pr, Merge: *flags.merge, Base: *flags.base},
			Cutoff: cutoff,
			Until:  until,
			Drafts: append([]string(nil), *flags.drafts...),
		}},
		Arms: arms,
	}, nil
}

// parseReplayTimestamp parses a required RFC3339 timestamp flag.
func parseReplayTimestamp(flagName, raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("%s is required (RFC3339)", flagName)
	}
	ts, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not valid RFC3339: %w", flagName, raw, err)
	}
	return ts.UTC(), nil
}

// parseReplayArms parses 1..MaxArms repeatable --arm specifications.
func parseReplayArms(specs []string) ([]replay.Arm, error) {
	if len(specs) == 0 || len(specs) > replay.MaxArms {
		return nil, fmt.Errorf("provide 1..%d --arm name=<command> flags", replay.MaxArms)
	}
	seen := make(map[string]bool, len(specs))
	arms := make([]replay.Arm, 0, len(specs))
	for i := 0; i < len(specs) && i < replay.MaxArms; i++ {
		arm, err := replay.ParseArm(specs[i])
		if err != nil {
			return nil, fmt.Errorf("--arm: %w", err)
		}
		if seen[arm.Name] {
			return nil, fmt.Errorf("duplicate --arm name %q", arm.Name)
		}
		seen[arm.Name] = true
		arms = append(arms, arm)
	}
	return arms, nil
}

// writeReplayCLIReport prints either the JSON report or one human summary line per arm.
func writeReplayCLIReport(report *replay.Report, logPath string, jsonOut bool) error {
	if jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode replay report: %w", err)
		}
		_, err = fmt.Fprintln(os.Stdout, string(data))
		return err
	}
	for i := 0; i < len(report.Arms) && i < replay.MaxArms; i++ {
		a := report.Arms[i]
		if _, err := fmt.Fprintf(os.Stdout,
			"arm=%s suite=%s cutoff=%s retained=%d/%d verdict_match=%t order_sensitive=%t samples=%d added_verified_findings=%s fabrication=%s log=%s\n",
			a.Arm, a.Suite, replayCutoffLabel(a.Cutoff), a.RetainedDefects, a.TotalDefects, a.VerdictMatch, a.OrderSensitive, a.SampleCount,
			replayMetricLabel(a.AddedVerifiedFindings), replayMetricLabel(a.Fabrication), logPath); err != nil {
			return err
		}
	}
	return nil
}

// modelsInvocationTimeout includes sequential arm orders and bounded preparation for every sample.
func modelsInvocationTimeout(action string, flags *modelReplayFlags) time.Duration {
	if action != "replay" {
		return modelsTimeout
	}
	samples := 1
	if *flags.samples != "" {
		samples = replay.MaxCommitScan
	}
	arms := min(len(flags.arms), replay.MaxArms)
	return time.Duration(samples)*(replay.DefaultPreparationTimeout+2*time.Duration(arms)*replay.DefaultArmTimeout) + replay.DefaultArmTimeout
}

// buildReplaySuiteOptions reads a bounded sample array through the existing snapshot reader.
func buildReplaySuiteOptions(ctx context.Context, flags *modelReplayFlags) (replay.Options, error) {
	arms, err := parseReplayArms(flags.arms)
	if err != nil {
		return replay.Options{}, err
	}
	opts, err := assembleReplayOptions(flags, time.Time{}, time.Time{}, arms)
	if err != nil {
		return replay.Options{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	raw := []byte(bundledReplaySamples)
	if *flags.samples != "bundled" {
		raw, err = contextopt.ReadSnapshot(ctx, *flags.samples)
		if err != nil {
			return replay.Options{}, err
		}
	}
	opts.Samples, err = decodeReplaySamples(raw, *flags.samples == "bundled")
	return opts, err
}

// decodeReplaySamples decodes one bounded manifest, distinguishing bundled and custom samples.
func decodeReplaySamples(raw []byte, bundled bool) ([]replay.Sample, error) {
	var samples []replay.Sample
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&samples); err != nil {
		return nil, fmt.Errorf("decode --samples: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("--samples must contain one JSON array")
	}
	if len(samples) == 0 || len(samples) > replay.MaxCommitScan {
		return nil, errors.New("--samples must contain 1..256 samples")
	}
	for i := 0; i < len(samples) && i < replay.MaxCommitScan; i++ {
		if bundled && !util.ValidGitObjectID(samples[i].PR.Base) {
			return nil, fmt.Errorf("bundled sample PR %d base %q must be a full Git object ID", samples[i].PR.Number, samples[i].PR.Base)
		}
	}
	return samples, nil
}

// replayCutoffLabel points multi-sample consumers at the per-sample report metadata.
func replayCutoffLabel(cutoff time.Time) string {
	if cutoff.IsZero() {
		return "per-sample"
	}
	return cutoff.UTC().Format(time.RFC3339)
}

// replayMetricLabel distinguishes an unmeasured annotation dimension from measured zero.
func replayMetricLabel(value *int) string {
	if value == nil {
		return "unmeasured"
	}
	return fmt.Sprint(*value)
}
