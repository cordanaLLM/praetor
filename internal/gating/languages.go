// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The languages whose own toolchain the prefetch, security and test stages run. A language
// counts as present when its marker is at the repository root: go.mod for Go, Cargo.lock for
// Cargo, which is also what its commands hold the build to (--locked).
const (
	languageGo    = "go"
	languageCargo = "cargo"
	// supportedLanguages names them with their markers for the refusal message.
	supportedLanguages = "Go (go.mod) and Cargo (Cargo.lock)"
	// AdmitUnsupportedFlag names the `gate run` flag (RunOptions.AdmitUnsupported) the pre-push
	// hook adoption renders passes (internal/adopt/hooks.go), so a rename changes both.
	AdmitUnsupportedFlag = "admit-unsupported"
	// admittedUnsupportedNote ends the receipt stage's reason when admitUnsupported applies.
	admittedUnsupportedNote = "admitted without a receipt (--" + AdmitUnsupportedFlag +
		"): verify these languages with the repository's own entry point, such as make verify-all"
)

// RunOptions selects how RunGatedPipeline runs.
type RunOptions struct {
	// DryRun runs only the read-only stages and mints no receipt.
	DryRun bool
	// AdmitUnsupported admits, without a receipt, a run in which no toolchain stage ran because
	// the repository root holds neither a go.mod nor a Cargo.lock: the receipt stage is recorded
	// as not applicable and names the languages the gate runs no toolchain for, instead of
	// rejecting the run. A root holding either marker is refused as before when nothing ran for
	// it, so a Go or Cargo repository whose toolchain stages could not run still fails. The
	// pre-push hook adoption renders passes it, so a repository in a language the gate has no
	// runner for (Meson, CMake, npm, Python) is not refused on every push (#648); a caller that
	// needs a receipt, such as CI or the gatekeeper, leaves it unset.
	AdmitUnsupported bool
}

// ErrNothingVerified reports a run in which no toolchain stage ran its checks for any language.
// The receipt stage refuses to sign such a run: a receipt certifying prefetch, security scans
// and tests that were each not applicable or skipped attests to nothing but the HISS scan.
var ErrNothingVerified = errors.New("no verification stage ran for any language")

// unsupportedMarkers are the root files that identify a language the gate runs no toolchain
// for. They are named when a receipt is refused, so the refusal says what the repository holds
// rather than only what the gate lacks. Cargo.toml counts here only without a Cargo.lock.
//
// Adoption's verification planner reads a similar set of markers to choose build commands
// (internal/adopt/verification_inputs.go), but it imports this package, so the gate cannot call
// it; this table only names languages in a refusal and selects no command.
var unsupportedMarkers = [...]struct{ file, language string }{
	{"Cargo.toml", "cargo without a committed Cargo.lock"},
	{"package.json", "node"},
	{"pyproject.toml", "python"},
	{"requirements.txt", "python"},
	{"setup.py", "python"},
	{"pom.xml", "java"},
	{"build.gradle", "java"},
	{"build.gradle.kts", "kotlin"},
	{"Gemfile", "ruby"},
	{"composer.json", "php"},
	{"mix.exs", "elixir"},
	{"Package.swift", "swift"},
	{"pubspec.yaml", "dart"},
	{"meson.build", "meson"},
	{"CMakeLists.txt", "cmake"},
}

// languagePart is one language's share of a toolchain stage, carried the way a stage carries
// its outcome: a nil err passed, a *stageSkip was skipped or not applicable, and any other
// error failed.
type languagePart struct {
	language string
	msg      string
	err      error
}

// isFailure reports whether err fails a stage rather than recording a skip.
func isFailure(err error) bool {
	return partStatus(err) == StageFailed
}

// partStatus is the verdict a part's err records.
func partStatus(err error) StageStatus {
	if err == nil {
		return StagePassed
	}
	if skip, ok := errors.AsType[*stageSkip](err); ok {
		return skip.status
	}
	return StageFailed
}

// noteVerified records that part's language ran a toolchain stage and passed it.
func (c *stageConfig) noteVerified(part languagePart) {
	if part.err == nil && !slices.Contains(c.verified, part.language) {
		c.verified = append(c.verified, part.language)
	}
}

// withCargo completes a toolchain stage whose Go part has already run, by running its Cargo
// part where a Cargo.lock is present.
//
// Without a Cargo.lock it returns the Go part exactly as the stage returned it before Cargo
// support, message and verdict alike, so a Go repository's stage lines and signed output are
// unchanged. A Go part that failed stops the stage before Cargo runs, as any failure stops the
// pipeline. Otherwise each present language contributes a "language: outcome" clause to the
// stage's reason (combineParts), which is how the signed stage output records what ran for which
// language.
func withCargo(ctx context.Context, cfg *stageConfig, goPart languagePart,
	cargo func(context.Context, *stageConfig) (string, error)) (string, error) {
	cfg.noteVerified(goPart)
	if !util.FileExists(filepath.Join(cfg.repoDir, CargoLockFile)) || isFailure(goPart.err) {
		return goPart.msg, goPart.err
	}
	msg, err := cargo(ctx, cfg)
	if isFailure(err) {
		return "", fmt.Errorf("%s: %w", languageCargo, err)
	}
	cargoPart := languagePart{language: languageCargo, msg: msg, err: err}
	cfg.noteVerified(cargoPart)
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return combineParts(cargoPart)
	}
	return combineParts(goPart, cargoPart)
}

// combineParts folds the parts of the languages a repository holds into one stage verdict, none
// of them failed. The stage passed only when every part passed. When some ran and some did not,
// it is recorded as skipped, never as passed: a Cargo audit that did not run must not read as a
// pass because the Go scanners beside it did. The reason names each language's outcome.
func combineParts(parts ...languagePart) (string, error) {
	clauses := make([]string, 0, len(parts))
	passed, skippedAny := 0, false
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		outcome := part.msg
		switch partStatus(part.err) {
		case StagePassed:
			passed++
			if outcome == "" {
				outcome = "passed"
			}
		case StageSkipped:
			skippedAny = true
			outcome = part.err.Error()
		default:
			outcome = part.err.Error()
		}
		clauses = append(clauses, part.language+": "+outcome)
	}
	reason := strings.Join(clauses, "; ")
	switch {
	case passed == len(parts):
		return reason, nil
	case passed > 0 || skippedAny:
		return "", skipped(reason)
	default:
		return "", notApplicable(reason)
	}
}

// requireVerification refuses a receipt for a run in which no toolchain stage ran for any
// language: the prefetch, security and test stages were each not applicable or skipped. Under
// RunOptions.AdmitUnsupported a root holding no toolchain marker is not applicable instead: the
// same reason, ending in admittedUnsupportedNote, and cfg.rep.AdmittedUnverified set.
func requireVerification(cfg *stageConfig) error {
	if len(cfg.verified) > 0 {
		return nil
	}
	refusal := nothingVerifiedError(cfg.repoDir)
	if !cfg.admitUnsupported || holdsToolchainMarker(cfg.repoDir) {
		return refusal
	}
	cfg.rep.AdmittedUnverified = true
	return notApplicable(refusal.Error() + "; " + admittedUnsupportedNote)
}

// holdsToolchainMarker reports whether the repository root holds the marker of a language whose
// toolchain the gate runs: go.mod for Go, Cargo.lock for Cargo.
func holdsToolchainMarker(repoDir string) bool {
	return util.FileExists(filepath.Join(repoDir, "go.mod")) || util.FileExists(filepath.Join(repoDir, CargoLockFile))
}

// nothingVerifiedError names why nothing ran: a Cargo.lock whose stages could not run here, the
// languages at the repository root the gate runs no toolchain for, or that it recognises none.
func nothingVerifiedError(repoDir string) error {
	notes := make([]string, 0, 2)
	if util.FileExists(filepath.Join(repoDir, CargoLockFile)) {
		notes = append(notes, "Cargo.lock is present, but no Cargo stage ran here; each stage's reason says why")
	}
	if unsupported := unsupportedLanguages(repoDir); len(unsupported) > 0 {
		notes = append(notes, "unsupported languages at the repository root: "+strings.Join(unsupported, ", "))
	}
	if len(notes) == 0 {
		notes = append(notes, "no language marker the gate recognises is at the repository root")
	}
	return fmt.Errorf("%w: %s, %s and %s each ran nothing, so no Exit-0 receipt is signed; "+
		"the gate runs the toolchains of %s; %s",
		ErrNothingVerified, stagePrefetch, stageSecurity, stageTests, supportedLanguages, strings.Join(notes, "; "))
}

// unsupportedLanguages lists the languages unsupportedMarkers finds at the repository root, each
// once and with the files that identified it, in the table's order.
func unsupportedLanguages(repoDir string) []string {
	hasLock := util.FileExists(filepath.Join(repoDir, CargoLockFile))
	order := make([]string, 0, len(unsupportedMarkers))
	files := make(map[string][]string, len(unsupportedMarkers))
	for i := 0; i < len(unsupportedMarkers); i++ {
		marker := unsupportedMarkers[i]
		if (hasLock && marker.file == "Cargo.toml") || !util.FileExists(filepath.Join(repoDir, marker.file)) {
			continue
		}
		if _, seen := files[marker.language]; !seen {
			order = append(order, marker.language)
		}
		files[marker.language] = append(files[marker.language], marker.file)
	}
	named := make([]string, 0, len(order))
	for i := 0; i < len(order); i++ {
		named = append(named, fmt.Sprintf("%s (%s)", order[i], strings.Join(files[order[i]], ", ")))
	}
	return named
}
