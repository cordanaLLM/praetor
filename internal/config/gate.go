// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const (
	// MaxDeclaredVerifyTimeout is the maximum duration gate.declared_verify_timeout may declare
	// without committed suite-length evidence (30 min). It also sets the ceiling for the
	// race-detector test stage timeout (gating.MaxTestStageTimeout).
	MaxDeclaredVerifyTimeout = 30 * time.Minute

	// MaxDeclaredVerifyTimeoutWithEvidence is the hard upper bound (60 min) on
	// gate.declared_verify_timeout when gate.declared_verify_evidence points to a committed
	// suite-length measurement (HISS-02: every stage execution carries a finite scalar upper bound).
	MaxDeclaredVerifyTimeoutWithEvidence = 60 * time.Minute

	// MinDeclaredVerifyEvidenceDuration is the shortest measured suite duration (20 min, two thirds
	// of MaxDeclaredVerifyTimeout) that justifies raising gate.declared_verify_timeout above the
	// 30-minute ceiling. A suite measured below 20 minutes still has more than 50% headroom under
	// 30 minutes, so normal load variance fits within MaxDeclaredVerifyTimeout without an exception.
	MinDeclaredVerifyEvidenceDuration = 20 * time.Minute

	// MaxDeclaredVerifyEvidenceBytes bounds the size of the gate.declared_verify_evidence file (HISS-02).
	MaxDeclaredVerifyEvidenceBytes = 4096

	// declaredVerifyEvidenceCommand is the verification command a declared_verify_evidence file must record.
	declaredVerifyEvidenceCommand = "make verify-all"

	// maxEvidenceTreeRecordBytes bounds the git ls-tree output for one evidence path (HISS-02).
	maxEvidenceTreeRecordBytes = 1024

	// maxEvidencePathSegments bounds the path-segment scan in validateDeclaredVerifyEvidencePath (HISS-02).
	maxEvidencePathSegments = 128
)

// GatePolicy is the manifest's gate section: settings of the anti-direct-merge gating pipeline.
// declared_verify_timeout sets the stage bound for the declared-verification stage.
// declared_verify_evidence names the committed suite-length measurement required when
// declared_verify_timeout exceeds MaxDeclaredVerifyTimeout (30m), up to
// MaxDeclaredVerifyTimeoutWithEvidence (60m).
// readme_presentation turns measured-not-enforced README presentation report lines into an
// enforced gate failure when no baseline entry covers the findings. It is repository-only,
// like Documentation and Security, and stays out of ResolvedPolicy.
type GatePolicy struct {
	DeclaredVerifyTimeout  string `yaml:"declared_verify_timeout,omitempty"`
	DeclaredVerifyEvidence string `yaml:"declared_verify_evidence,omitempty"`
	ReadmePresentation     bool   `yaml:"readme_presentation,omitempty"`
	measuredDuration       time.Duration
}

// DeclaredVerifyEvidence is the committed suite-length measurement that
// gate.declared_verify_evidence points to when gate.declared_verify_timeout exceeds
// MaxDeclaredVerifyTimeout (30m).
type DeclaredVerifyEvidence struct {
	Command          string `yaml:"command"`
	MeasuredDuration string `yaml:"measured_duration"`
	Date             string `yaml:"date"`
	Commit           string `yaml:"commit"`
}

// gateKeys is the closed key set of the gate section.
var gateKeys = []string{"declared_verify_timeout", "declared_verify_evidence", "readme_presentation"}

// EnforceReadmePresentation reports whether the manifest enforces the README presentation gate.
func (p *GatePolicy) EnforceReadmePresentation() bool {
	return p != nil && p.ReadmePresentation
}

// ReadmePresentationEnforced reports whether the manifest at root declares gate.readme_presentation: true.
// An absent manifest returns false, nil (report-only default); an unreadable or invalid manifest returns an error.
func ReadmePresentationEnforced(root string) (bool, error) {
	m, err := LoadManifest(filepath.Join(root, ".standards.yaml"))
	if errors.Is(err, os.ErrNotExist) || (err == nil && m == nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return m.Gate.EnforceReadmePresentation(), nil
}

// UnmarshalYAML checks that all keys are known before decoding GatePolicy.
func (p *GatePolicy) UnmarshalYAML(node *yaml.Node) error {
	if err := requireKnownKeys(node, "gate", gateKeys); err != nil {
		return err
	}
	type rawGatePolicy GatePolicy
	var decoded rawGatePolicy
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*p = GatePolicy(decoded)
	return nil
}

// DeclaredVerifyTimeoutDuration returns the declared verify-all timeout as a time.Duration,
// or 0 if unset. ValidateGate has already verified that if set, it is positive and within the
// applicable ceiling (MaxDeclaredVerifyTimeout, or MaxDeclaredVerifyTimeoutWithEvidence with evidence).
func (p *GatePolicy) DeclaredVerifyTimeoutDuration() time.Duration {
	if p == nil || p.DeclaredVerifyTimeout == "" {
		return 0
	}
	d, err := time.ParseDuration(p.DeclaredVerifyTimeout)
	if err != nil {
		return 0
	}
	return d
}

// DeclaredVerifyEvidencePath returns the repository-relative path declared in
// gate.declared_verify_evidence, or "" when unset.
func (p *GatePolicy) DeclaredVerifyEvidencePath() string {
	if p == nil {
		return ""
	}
	return p.DeclaredVerifyEvidence
}

// DeclaredVerifyMeasuredDuration returns the measured suite duration read from
// gate.declared_verify_evidence during ValidateGate, or 0 when no raised bound is in force.
func (p *GatePolicy) DeclaredVerifyMeasuredDuration() time.Duration {
	if p == nil {
		return 0
	}
	return p.measuredDuration
}

// validateManifestGate validates the gate section of the manifest against its repository root.
func validateManifestGate(m *Manifest) error {
	if m == nil {
		return nil
	}
	ctx := m.validateCtx
	if ctx == nil {
		ctx = defaultManifestContext
	}
	return ValidateGate(ctx, m.repoRoot, m.Gate)
}

// ValidateGate validates a decoded GatePolicy for the repository at repoRoot.
// A declared_verify_timeout at most MaxDeclaredVerifyTimeout (30m) is accepted without evidence
// (and refuses gate.declared_verify_evidence when present without need). A value above 30m and
// at most MaxDeclaredVerifyTimeoutWithEvidence (60m) requires a valid, committed evidence file
// at gate.declared_verify_evidence; any value above 60m is refused with or without evidence.
func ValidateGate(ctx context.Context, repoRoot string, p *GatePolicy) error {
	if ctx == nil {
		return errors.New("validating gate policy requires a context")
	}
	if p == nil {
		return nil
	}
	if p.DeclaredVerifyTimeout == "" {
		if p.DeclaredVerifyEvidence != "" {
			return fmt.Errorf("gate.declared_verify_evidence %q requires gate.declared_verify_timeout above %s",
				p.DeclaredVerifyEvidence, MaxDeclaredVerifyTimeout)
		}
		p.measuredDuration = 0
		return nil
	}
	parsed, err := time.ParseDuration(p.DeclaredVerifyTimeout)
	if err != nil {
		return fmt.Errorf("gate.declared_verify_timeout %q is not a duration (ceiling %s): %w",
			p.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout, err)
	}
	if parsed <= 0 {
		return fmt.Errorf("gate.declared_verify_timeout %s is not positive (ceiling %s)",
			p.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout)
	}
	if parsed <= MaxDeclaredVerifyTimeout {
		if p.DeclaredVerifyEvidence != "" {
			return fmt.Errorf("gate.declared_verify_evidence %q applies only when gate.declared_verify_timeout (%s) exceeds the %s ceiling",
				p.DeclaredVerifyEvidence, p.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout)
		}
		p.measuredDuration = 0
		return nil
	}
	return validateRaisedDeclaredVerifyTimeout(ctx, repoRoot, p, parsed)
}

// validateRaisedDeclaredVerifyTimeout enforces the 60-minute hard cap and the committed evidence
// requirement for a gate.declared_verify_timeout above MaxDeclaredVerifyTimeout (30m).
func validateRaisedDeclaredVerifyTimeout(ctx context.Context, repoRoot string, p *GatePolicy, parsed time.Duration) error {
	if parsed > MaxDeclaredVerifyTimeoutWithEvidence {
		return fmt.Errorf("gate.declared_verify_timeout %s exceeds the %s hard cap (ceiling %s without evidence)",
			p.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeoutWithEvidence, MaxDeclaredVerifyTimeout)
	}
	if err := requireDeclaredVerifyEvidenceKey(p); err != nil {
		return err
	}
	if strings.TrimSpace(p.DeclaredVerifyEvidence) == "" {
		return nil
	}
	_, measured, err := LoadDeclaredVerifyEvidence(ctx, repoRoot, p.DeclaredVerifyEvidence, parsed)
	if err != nil {
		return err
	}
	p.measuredDuration = measured
	return nil
}

// requireDeclaredVerifyEvidenceKey refuses a raised declared_verify_timeout when
// gate.declared_verify_evidence is omitted or blank.
func requireDeclaredVerifyEvidenceKey(p *GatePolicy) error {
	if strings.TrimSpace(p.DeclaredVerifyEvidence) == "" {
		return fmt.Errorf("gate.declared_verify_timeout %s exceeds the %s ceiling without gate.declared_verify_evidence (hard cap %s)",
			p.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout, MaxDeclaredVerifyTimeoutWithEvidence)
	}
	return nil
}

// LoadDeclaredVerifyEvidence confines evidencePath to repoRoot, reads the regular file through
// the repository's pinned os.Root handle within MaxDeclaredVerifyEvidenceBytes, verifies via git
// that the file is tracked as a regular blob at HEAD, and validates its YAML payload and
// measured-duration relation against declaredBound.
func LoadDeclaredVerifyEvidence(ctx context.Context, repoRoot, evidencePath string, declaredBound time.Duration) (DeclaredVerifyEvidence, time.Duration, error) {
	if ctx == nil {
		return DeclaredVerifyEvidence{}, 0, fmt.Errorf("gate.declared_verify_evidence %q: context is required", evidencePath)
	}
	if strings.TrimSpace(repoRoot) == "" {
		repoRoot = "."
	}
	if err := validateDeclaredVerifyEvidencePath(repoRoot, evidencePath); err != nil {
		return DeclaredVerifyEvidence{}, 0, err
	}
	cleanSlash := slashpath.Clean(filepath.ToSlash(evidencePath))
	data, err := readConfinedEvidenceFile(repoRoot, evidencePath, cleanSlash)
	if err != nil {
		return DeclaredVerifyEvidence{}, 0, err
	}
	if err := checkDeclaredVerifyEvidenceTracked(ctx, repoRoot, evidencePath, cleanSlash); err != nil {
		return DeclaredVerifyEvidence{}, 0, err
	}
	return parseDeclaredVerifyEvidence(data, evidencePath, declaredBound)
}

// validateDeclaredVerifyEvidencePath ensures evidencePath is a clean, relative, repository-local
// forward-slash path with no parent step or escaping symlink.
func validateDeclaredVerifyEvidencePath(repoRoot, evidencePath string) error {
	if strings.TrimSpace(evidencePath) == "" || strings.TrimSpace(evidencePath) != evidencePath {
		return fmt.Errorf("gate.declared_verify_evidence %q: path must be a non-empty relative path without leading or trailing whitespace", evidencePath)
	}
	if filepath.IsAbs(evidencePath) || strings.HasPrefix(evidencePath, "/") || strings.HasPrefix(evidencePath, "\\") || filepath.VolumeName(evidencePath) != "" {
		return fmt.Errorf("gate.declared_verify_evidence %q: path must be relative to the repository root, not absolute", evidencePath)
	}
	if hasParentOrCurrentSegment(evidencePath) || !ValidRepositoryPath(evidencePath) {
		return fmt.Errorf("gate.declared_verify_evidence %q: path must stay inside the repository without parent ('..') segments", evidencePath)
	}
	if _, err := util.ConfinePath(repoRoot, filepath.FromSlash(evidencePath)); err != nil {
		return fmt.Errorf("gate.declared_verify_evidence %q: path escapes the repository root: %w", evidencePath, err)
	}
	return nil
}

// hasParentOrCurrentSegment reports whether raw contains a "." or ".." path component.
func hasParentOrCurrentSegment(raw string) bool {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	parts := strings.Split(normalized, "/")
	for i := 0; i < len(parts) && i < maxEvidencePathSegments; i++ {
		if parts[i] == ".." || parts[i] == "." || parts[i] == "" {
			return true
		}
	}
	return len(parts) > maxEvidencePathSegments
}

// readConfinedEvidenceFile reads cleanSlash inside repoRoot through a pinned os.Root handle,
// bounded to MaxDeclaredVerifyEvidenceBytes and refusing non-regular files and escaping symlinks.
func readConfinedEvidenceFile(repoRoot, evidencePath, cleanSlash string) ([]byte, error) {
	var data []byte
	err := util.InConfinedDirectory(repoRoot, ".", func(dir *os.Root) error {
		var readErr error
		data, readErr = util.ReadLimitedIn(dir, filepath.FromSlash(cleanSlash), MaxDeclaredVerifyEvidenceBytes)
		return readErr
	})
	if err != nil {
		return nil, fmt.Errorf("gate.declared_verify_evidence %q: read confined regular file: %w", evidencePath, err)
	}
	return data, nil
}

// checkDeclaredVerifyEvidenceTracked verifies through `git --literal-pathspecs ls-tree -z HEAD -- <path>`
// under util.RunGitProbe (5s timeout, isolated git environment) that cleanSlash is committed at HEAD
// as a regular file blob.
func checkDeclaredVerifyEvidenceTracked(ctx context.Context, repoRoot, evidencePath, cleanSlash string) error {
	listing, err := util.RunGitProbe(ctx, repoRoot, maxEvidenceTreeRecordBytes,
		"--literal-pathspecs", "ls-tree", "-z", "HEAD", "--", cleanSlash)
	if err != nil {
		return fmt.Errorf("gate.declared_verify_evidence %q: check git HEAD tracking: %w",
			evidencePath, util.CommandDiagnostic(err, listing.Stderr))
	}
	entries, err := util.ParseGitTreeListing(listing.Stdout, false, 1)
	if err != nil {
		return fmt.Errorf("gate.declared_verify_evidence %q: parse git ls-tree output: %w", evidencePath, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("gate.declared_verify_evidence %q: file is not tracked at HEAD (commit the measurement before declaring a bound above %s)",
			evidencePath, MaxDeclaredVerifyTimeout)
	}
	if entries[0].Path != cleanSlash || !entries[0].RegularBlob() {
		return fmt.Errorf("gate.declared_verify_evidence %q: HEAD entry is not a regular file", evidencePath)
	}
	return nil
}

// parseDeclaredVerifyEvidence decodes a single YAML document into DeclaredVerifyEvidence with
// unknown fields rejected and checks every field and the measured-duration relation.
func parseDeclaredVerifyEvidence(data []byte, evidencePath string, declaredBound time.Duration) (DeclaredVerifyEvidence, time.Duration, error) {
	var ev DeclaredVerifyEvidence
	if err := util.DecodeYAMLDocument(data, &ev, util.YAMLDocumentOptions{KnownFields: true}); err != nil {
		return DeclaredVerifyEvidence{}, 0, fmt.Errorf("gate.declared_verify_evidence %q: malformed evidence YAML: %w", evidencePath, err)
	}
	if err := validateDeclaredVerifyEvidenceFields(ev, evidencePath); err != nil {
		return DeclaredVerifyEvidence{}, 0, err
	}
	measured, err := validateMeasuredDurationRelation(ev.MeasuredDuration, evidencePath, declaredBound)
	if err != nil {
		return DeclaredVerifyEvidence{}, 0, err
	}
	return ev, measured, nil
}

// validateDeclaredVerifyEvidenceFields checks the command, date, and commit SHA fields of ev.
func validateDeclaredVerifyEvidenceFields(ev DeclaredVerifyEvidence, evidencePath string) error {
	if strings.TrimSpace(ev.Command) != declaredVerifyEvidenceCommand {
		return fmt.Errorf("gate.declared_verify_evidence %q: command %q must be %q",
			evidencePath, ev.Command, declaredVerifyEvidenceCommand)
	}
	if _, err := time.Parse(ExceptionDateLayout, ev.Date); err != nil {
		return fmt.Errorf("gate.declared_verify_evidence %q: date %q must be a YYYY-MM-DD date: %w",
			evidencePath, ev.Date, err)
	}
	if !validEvidenceCommitSHA(ev.Commit) {
		return fmt.Errorf("gate.declared_verify_evidence %q: commit %q must be a %d..%d character hexadecimal commit SHA",
			evidencePath, ev.Commit, minReleaseCommitSHALen, maxReleaseCommitSHALen)
	}
	return nil
}

// validEvidenceCommitSHA reports whether commit is a 7..40 character hexadecimal commit SHA.
func validEvidenceCommitSHA(commit string) bool {
	if len(commit) < minReleaseCommitSHALen || len(commit) > maxReleaseCommitSHALen {
		return false
	}
	for i := 0; i < len(commit) && i < maxReleaseCommitSHALen; i++ {
		if !isHexByte(commit[i]) {
			return false
		}
	}
	return true
}

// validateMeasuredDurationRelation verifies that measured_duration parses as a positive Go
// duration in [MinDeclaredVerifyEvidenceDuration, declaredBound].
func validateMeasuredDurationRelation(raw, evidencePath string, declaredBound time.Duration) (time.Duration, error) {
	measured, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("gate.declared_verify_evidence %q: measured_duration %q is not a duration: %w",
			evidencePath, raw, err)
	}
	if measured <= 0 {
		return 0, fmt.Errorf("gate.declared_verify_evidence %q: measured_duration %s is not positive",
			evidencePath, raw)
	}
	if measured < MinDeclaredVerifyEvidenceDuration {
		return 0, fmt.Errorf("gate.declared_verify_evidence %q: measured_duration %s is below the %s minimum required to raise gate.declared_verify_timeout above %s",
			evidencePath, measured, MinDeclaredVerifyEvidenceDuration, MaxDeclaredVerifyTimeout)
	}
	if measured > declaredBound {
		return 0, fmt.Errorf("gate.declared_verify_evidence %q: measured_duration %s exceeds declared bound %s (the declared bound must cover the measurement)",
			evidencePath, measured, declaredBound)
	}
	return measured, nil
}
