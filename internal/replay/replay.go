// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

// preparedSample holds a verified cutoff tree, derived ground truth, and draft list for one sample.
type preparedSample struct {
	sample       Sample
	cutoffCommit string
	treeDir      string
	drafts       []string
	gt           GroundTruthResult
	cleanup      func() error
}

// Replay evaluates every arm twice per sample (original and reversed draft order) and logs outcomes.
func Replay(ctx context.Context, opts Options) (rep *Report, retErr error) {
	normOpts, err := normalizeReplayOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	prepared := make([]preparedSample, 0, len(normOpts.Samples))
	defer func() {
		for i := 0; i < len(prepared) && i < MaxCommitScan; i++ {
			if prepared[i].cleanup != nil {
				retErr = errors.Join(retErr, prepared[i].cleanup())
			}
		}
	}()
	for i := 0; i < len(normOpts.Samples) && i < MaxCommitScan; i++ {
		ps, prepErr := prepareOneSample(ctx, normOpts.RepoDir, normOpts.Base, i, normOpts.Samples[i])
		if prepErr != nil {
			return nil, prepErr
		}
		prepared = append(prepared, ps)
	}
	return executeReplayArms(ctx, normOpts, prepared)
}

// normalizeReplayOptions validates Options and populates defaults.
func normalizeReplayOptions(ctx context.Context, opts Options) (Options, error) {
	if ctx == nil {
		return Options{}, errors.New("replay requires a context")
	}
	if opts.RepoDir == "" {
		opts.RepoDir = "."
	}
	if opts.Suite == "" {
		opts.Suite = DefaultSuite
	}
	if !ValidReplayName(opts.Suite) {
		return Options{}, fmt.Errorf("invalid suite name %q", opts.Suite)
	}
	if opts.OutcomeLog == "" {
		opts.OutcomeLog = router.DefaultOutcomeLogPath
	}
	if len(opts.Samples) == 0 || len(opts.Samples) > MaxCommitScan {
		return Options{}, fmt.Errorf("replay requires 1..%d samples", MaxCommitScan)
	}
	if len(opts.Arms) == 0 || len(opts.Arms) > MaxArms {
		return Options{}, fmt.Errorf("replay requires 1..%d arms", MaxArms)
	}
	return opts, validateConfiguredArms(opts.Arms)
}

// validateConfiguredArms ensures every Arm carries a valid name and command vector.
func validateConfiguredArms(arms []Arm) error {
	for i := 0; i < len(arms) && i < MaxArms; i++ {
		if !ValidReplayName(arms[i].Name) {
			return fmt.Errorf("invalid arm name %q", arms[i].Name)
		}
		if err := validateArmTokens(arms[i].Name, arms[i].Command); err != nil {
			return err
		}
	}
	return nil
}

// prepareOneSample materializes or verifies the cutoff tree, checks leaks, and derives ground truth.
func prepareOneSample(ctx context.Context, repoDir, defaultBase string, index int, sample Sample) (preparedSample, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultPreparationTimeout)
	defer cancel()
	if err := validateSampleAnnotations(sample); err != nil {
		return preparedSample{}, err
	}
	prSpec := sample.PR
	if prSpec.Base == "" {
		prSpec.Base = defaultBase
	}
	treeDir, cutoffSHA, cleanup, err := materializeCutoffTree(ctx, repoDir, prSpec.Base, index, sample)
	if err != nil {
		return preparedSample{}, err
	}
	if err := VerifyTree(ctx, treeDir, sample.Cutoff); err != nil {
		return preparedSample{}, errors.Join(err, callCleanup(cleanup))
	}
	gt, err := GroundTruth(ctx, repoDir, prSpec, sample.Cutoff, sample.Until)
	if err != nil {
		return preparedSample{}, errors.Join(err, callCleanup(cleanup))
	}
	if err := verifyPRAtCutoff(ctx, repoDir, gt.MergeCommit, cutoffSHA); err != nil {
		return preparedSample{}, errors.Join(err, callCleanup(cleanup))
	}
	if gt.LaterCommits == 0 || len(gt.Fixes) == 0 || len(gt.Hunks) == 0 {
		msg := refusedf(ErrNoGroundTruth, "PR #%d (%s) has no cutoff-mappable post-merge repair hunks before %s",
			prSpec.Number, prSpec.Merge, sample.Until.UTC().Format(time.RFC3339))
		return preparedSample{}, errors.Join(msg, callCleanup(cleanup))
	}
	drafts, draftCleanup, err := prepareSampleDrafts(ctx, repoDir, gt.MergeCommit, sample.Drafts)
	if err != nil {
		return preparedSample{}, errors.Join(err, callCleanup(cleanup))
	}
	combinedCleanup := func() error { return errors.Join(callCleanup(draftCleanup), callCleanup(cleanup)) }
	return preparedSample{
		sample:       sample,
		cutoffCommit: cutoffSHA,
		treeDir:      treeDir,
		drafts:       drafts,
		gt:           *gt,
		cleanup:      combinedCleanup,
	}, nil
}

// callCleanup invokes fn when non-nil.
func callCleanup(fn func() error) error {
	if fn == nil {
		return nil
	}
	return fn()
}

// materializeCutoffTree verifies the selected source and snapshots only its cutoff commit.
func materializeCutoffTree(ctx context.Context, repoDir, base string, _ int, sample Sample) (string, string, func() error, error) {
	source := repoDir
	var sha string
	var err error
	if sample.TreeDir != "" {
		source = sample.TreeDir
		sha, err = util.ResolveGitCommit(ctx, source, "HEAD")
		if err == nil {
			err = verifyCleanTree(ctx, source)
		}
	} else {
		sha, err = Cutoff(ctx, repoDir, base, sample.Cutoff)
	}
	if err != nil {
		return "", "", nil, err
	}
	canonical, err := Cutoff(ctx, repoDir, base, sample.Cutoff)
	if err != nil {
		return "", "", nil, err
	}
	if sha != canonical {
		return "", "", nil, fmt.Errorf("%w: supplied tree HEAD %s differs from canonical cutoff %s", ErrRefused, sha, canonical)
	}
	if err := verifyCommitHistory(ctx, source, sha, sample.Cutoff); err != nil {
		return "", "", nil, err
	}
	dir, cleanup, err := snapshotCutoff(ctx, source, sha)
	return dir, sha, cleanup, err
}

// verifyCleanTree refuses tracked edits, ignored artifacts and untracked files before snapshotting.
func verifyCleanTree(ctx context.Context, dir string) error {
	res, err := util.RunGitProbe(ctx, dir, maxGitProbeBytes, "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil {
		return err
	}
	if len(res.Stdout) > 0 {
		return fmt.Errorf("%w: supplied cutoff tree is dirty", ErrRefused)
	}
	return nil
}

// prepareSampleDrafts validates explicit draft paths or writes synthetic diff drafts when empty.
func prepareSampleDrafts(ctx context.Context, repoDir, mergeSHA string, drafts []string) ([]string, func() error, error) {
	if len(drafts) > MaxDrafts {
		return nil, nil, fmt.Errorf("sample has %d drafts (max %d)", len(drafts), MaxDrafts)
	}
	if len(drafts) > 0 {
		resolved := make([]string, 0, len(drafts))
		for i := 0; i < len(drafts) && i < MaxDrafts; i++ {
			abs, err := filepath.Abs(drafts[i])
			if err != nil {
				return nil, nil, fmt.Errorf("resolve draft %q: %w", drafts[i], err)
			}
			resolved = append(resolved, abs)
		}
		return resolved, nil, nil
	}
	return synthesizePRDrafts(ctx, repoDir, mergeSHA)
}

// synthesizePRDrafts writes two deterministic review-context draft files when --draft is omitted.
func synthesizePRDrafts(ctx context.Context, repoDir, mergeSHA string) ([]string, func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultArmTimeout)
	defer cancel()
	res, err := util.RunGitProbe(ctx, repoDir, maxGitProbeBytes, "diff", "--no-ext-diff", "--no-color", "--stat", "--patch", mergeSHA+"^1", mergeSHA, "--")
	if err != nil {
		return nil, nil, fmt.Errorf("synthesize drafts for %s: %w", mergeSHA, err)
	}
	tmpDir, err := os.MkdirTemp("", "praetor-replay-drafts-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create draft temp dir: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmpDir) }
	drafts, err := writeSyntheticDrafts(ctx, tmpDir, mergeSHA, res.Stdout)
	if err != nil {
		return nil, nil, errors.Join(err, cleanup())
	}
	return drafts, cleanup, nil
}

// writeSyntheticDrafts writes deterministic patch and commit inputs under a cancellable I/O budget.
func writeSyntheticDrafts(ctx context.Context, dir, mergeSHA string, patch []byte) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultArmTimeout)
	defer cancel()
	draftA := filepath.Join(dir, "draft-00-patch.diff")
	draftB := filepath.Join(dir, "draft-01-commit.txt")
	if err := contextopt.WriteSnapshot(ctx, draftA, patch, 0o600); err != nil {
		return nil, err
	}
	if err := contextopt.WriteSnapshot(ctx, draftB, []byte("merge: "+mergeSHA+"\n"), 0o600); err != nil {
		return nil, err
	}
	return []string{draftA, draftB}, nil
}

// executeReplayArms runs every arm forward and reversed across prepared samples and logs outcomes.
func executeReplayArms(ctx context.Context, opts Options, prepared []preparedSample) (*Report, error) {
	cutoffUTC := prepared[0].sample.Cutoff.UTC()
	report := &Report{
		Suite:        opts.Suite,
		Cutoff:       cutoffUTC,
		CutoffCommit: prepared[0].cutoffCommit,
		SampleCount:  len(prepared),
		GroundTruth:  make([]GroundTruthResult, 0, len(prepared)),
		Arms:         make([]ArmScore, 0, len(opts.Arms)),
		Outcomes:     make([]router.Outcome, 0, len(opts.Arms)),
	}
	for i := 0; i < len(prepared) && i < MaxCommitScan; i++ {
		report.GroundTruth = append(report.GroundTruth, prepared[i].gt)
	}
	ctxDigest, err := digestSamples(ctx, opts.Suite, prepared)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(opts.Arms) && i < MaxArms; i++ {
		score, err := scoreOneArm(ctx, opts.Suite, cutoffUTC, opts.Arms[i], prepared)
		if err != nil {
			return nil, err
		}
		outcome := buildReplayOutcome(score, opts.Arms[i], ctxDigest)
		router.PrepareOutcome(&outcome)
		report.Arms = append(report.Arms, score)
		report.Outcomes = append(report.Outcomes, outcome)
	}
	if len(prepared) > 1 {
		report.Cutoff = time.Time{}
		report.CutoffCommit = ""
	}
	if err := publishReplayOutcomes(ctx, opts.OutcomeLog, report.Outcomes); err != nil {
		return nil, err
	}
	return report, nil
}

// scoreOneArm executes arm twice per sample (given order and reversed order) and aggregates metrics.
func scoreOneArm(ctx context.Context, suite string, cutoff time.Time, arm Arm, prepared []preparedSample) (ArmScore, error) {
	start := time.Now()
	score := ArmScore{
		Arm:          arm.Name,
		Suite:        suite,
		Cutoff:       cutoff,
		VerdictMatch: true,
		SampleCount:  len(prepared),
	}
	for i := 0; i < len(prepared) && i < MaxCommitScan; i++ {
		fwd, normFwd, err := runArmOnce(ctx, arm, prepared[i].treeDir, prepared[i].drafts)
		if err != nil {
			return ArmScore{}, err
		}
		reversed := slices.Clone(prepared[i].drafts)
		slices.Reverse(reversed)
		rev, normRev, err := runArmOnce(ctx, arm, prepared[i].treeDir, reversed)
		if err != nil {
			return ArmScore{}, err
		}
		sampleScore := scoreSample(prepared[i], fwd, rev, normFwd, normRev)
		score.Samples = append(score.Samples, sampleScore)
		score.OrderSensitive = score.OrderSensitive || normFwd != normRev
		score.VerdictMatch = score.VerdictMatch && normFwd == VerdictChangesRequested && normRev == VerdictChangesRequested
		score.RetainedDefects += sampleScore.RetainedDefects
		score.TotalDefects += sampleScore.TotalDefects
	}
	if len(prepared) == 1 {
		score.ForwardVerdict = score.Samples[0].ForwardVerdict
		score.ReverseVerdict = score.Samples[0].ReverseVerdict
	} else {
		score.Cutoff = time.Time{}
	}
	score.AddedVerifiedFindings = aggregateAnnotations(score.Samples, true)
	score.Fabrication = aggregateAnnotations(score.Samples, false)
	score.DurationMS = time.Since(start).Milliseconds()
	return score, nil
}

// buildReplayOutcome converts an ArmScore into a router.Outcome record for AppendOutcome.
func buildReplayOutcome(score ArmScore, arm Arm, contextDigest string) router.Outcome {
	result := router.OutcomeOK
	if !score.VerdictMatch || score.OrderSensitive || hasFabrication(score.Fabrication) {
		result = router.OutcomeFail
	}
	for i := 0; i < len(score.Samples) && i < MaxCommitScan; i++ {
		if hasFabrication(score.Samples[i].Fabrication) {
			result = router.OutcomeFail
		}
	}
	cutoff := score.Cutoff.UTC()
	retained := score.RetainedDefects
	verdictMatch := score.VerdictMatch
	orderSensitive := score.OrderSensitive
	return router.Outcome{
		Time:            time.Now().UTC(),
		Lane:            "replay",
		Task:            "review",
		Target:          arm.Name,
		ResolvedModel:   arm.Name,
		Result:          result,
		DurationMS:      score.DurationMS,
		Arm:             arm.Name,
		Suite:           score.Suite,
		Cutoff:          optionalCutoff(cutoff),
		Note:            annotationNote(score),
		SampleCount:     score.SampleCount,
		RetainedDefects: &retained,
		VerdictMatch:    &verdictMatch,
		OrderSensitive:  &orderSensitive,
		Identity: router.RunIdentity{
			PhysicalModel:  arm.Name,
			Harness:        "praetor-replay",
			HarnessVersion: "1",
			PromptDigest:   sha256Prefixed(strings.Join(arm.Command, "\x00")),
			ContextDigest:  contextDigest,
		},
	}
}

// digestSamples returns a deterministic sha256:<hex> digest over the replayed samples.
func digestSamples(ctx context.Context, suite string, prepared []preparedSample) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%q\x00", suite)
	for i := 0; i < len(prepared) && i < MaxCommitScan; i++ {
		p := prepared[i]
		metadata, err := json.Marshal(p.sample)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%q\x00%s\x00%s\x00", metadata, p.cutoffCommit, p.gt.MergeCommit)
		for j := 0; j < len(p.drafts) && j < MaxDrafts; j++ {
			raw, err := contextopt.ReadSnapshot(ctx, p.drafts[j])
			if err != nil {
				return "", fmt.Errorf("digest draft: %w", err)
			}
			fmt.Fprintf(&b, "%s\x00", sha256Prefixed(string(raw)))
		}
	}
	return sha256Prefixed(b.String()), nil
}

// sha256Prefixed computes "sha256:<64-hex>" over s.
func sha256Prefixed(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}
