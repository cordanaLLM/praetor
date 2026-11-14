// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"fmt"
	"time"
)

// scoreSample scores one tree without conflating metadata from different samples.
func scoreSample(p preparedSample, forward, reverse ArmVerdict, normForward, normReverse string) SampleScore {
	return SampleScore{
		Cutoff: p.sample.Cutoff.UTC(), CutoffCommit: p.cutoffCommit, MergeCommit: p.gt.MergeCommit,
		ForwardVerdict: normForward, ReverseVerdict: normReverse,
		RetainedDefects: countRetainedDefects(p.gt.Hunks, forward.Findings, reverse.Findings), TotalDefects: len(p.gt.Hunks),
		AddedVerifiedFindings: countAnnotations(p.sample.AddedVerified, forward.Findings, reverse.Findings, true),
		Fabrication:           countAnnotations(p.sample.Fabrications, forward.Findings, reverse.Findings, false),
	}
}

// countAnnotations counts verified additions in both runs and disproven locations in either run.
func countAnnotations(annotations, forward, reverse []Finding, both bool) *int {
	if annotations == nil {
		return nil
	}
	count := 0
	for i := 0; i < len(annotations) && i < MaxFindings; i++ {
		file, start, end, ok := resolveFindingSpan(annotations[i])
		if !ok {
			continue
		}
		h := FixHunk{File: file, StartLine: start, EndLine: end}
		a, b := findingsHitHunk(h, forward), findingsHitHunk(h, reverse)
		hit := a || b
		if both {
			hit = a && b
		}
		if hit {
			count++
		}
	}
	return &count
}

// aggregateAnnotations reports unmeasured if any sample lacks the requested annotation dimension.
func aggregateAnnotations(samples []SampleScore, verified bool) *int {
	total := 0
	for i := 0; i < len(samples) && i < MaxCommitScan; i++ {
		value := samples[i].Fabrication
		if verified {
			value = samples[i].AddedVerifiedFindings
		}
		if value == nil {
			return nil
		}
		total += *value
	}
	return &total
}

// optionalCutoff omits the singular cutoff on a multi-sample aggregate outcome.
func optionalCutoff(cutoff time.Time) *time.Time {
	if cutoff.IsZero() {
		return nil
	}
	return &cutoff
}

// annotationNote retains measured replay dimensions within the existing outcome schema.
func annotationNote(score ArmScore) string {
	return fmt.Sprintf("added_verified_findings=%s fabrication=%s", annotationValue(score.AddedVerifiedFindings), annotationValue(score.Fabrication))
}

// annotationValue distinguishes absent annotations from a measured zero.
func annotationValue(value *int) string {
	if value == nil {
		return "unmeasured"
	}
	return fmt.Sprint(*value)
}

// hasFabrication reports a measured disproven finding without conflating absence with zero.
func hasFabrication(value *int) bool {
	return value != nil && *value > 0
}

// validateSampleAnnotations refuses invalid or over-limit finding oracles before executing arms.
func validateSampleAnnotations(sample Sample) error {
	for _, annotations := range [2][]Finding{sample.AddedVerified, sample.Fabrications} {
		if len(annotations) > MaxFindings {
			return fmt.Errorf("annotation count exceeds %d", MaxFindings)
		}
		for i := 0; i < len(annotations) && i < MaxFindings; i++ {
			if _, _, _, ok := resolveFindingSpan(annotations[i]); !ok {
				return fmt.Errorf("annotation %d has no valid location", i)
			}
		}
	}
	return nil
}
