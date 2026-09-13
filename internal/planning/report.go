package planning

import "sort"

// Report is the shared CLI/MCP result envelope. ArtifactsWritten is caller
// supplied because the pure compiler never writes its in-memory files.
type Report struct {
	SchemaVersion    int      `json:"schema_version"`
	DraftID          string   `json:"draft_id"`
	Status           string   `json:"status"`
	ReviewRequired   bool     `json:"review_required"`
	ProvenanceStatus string   `json:"provenance_status"`
	Digest           string   `json:"digest"`
	MilestoneOrder   []string `json:"milestone_order"`
	StepOrder        []string `json:"step_order"`
	Unsupported      []string `json:"unsupported"`
	Artifacts        []string `json:"artifacts"`
	ArtifactsWritten bool     `json:"artifacts_written"`
}

// Report returns deterministic public metadata for a compiled result.
func (result *Result) Report(written bool) Report {
	artifacts := make([]string, 0, len(result.Files))
	for name := range result.Files {
		artifacts = append(artifacts, name)
	}
	sort.Strings(artifacts)
	return Report{SchemaVersion: result.Draft.SchemaVersion, DraftID: result.Draft.ID,
		Status: result.Status, ReviewRequired: result.ReviewRequired, ProvenanceStatus: result.ProvenanceStatus,
		Digest: result.Digest, MilestoneOrder: append([]string(nil), result.MilestoneOrder...),
		StepOrder: append([]string(nil), result.StepOrder...), Unsupported: append([]string(nil), result.Unsupported...),
		Artifacts: artifacts, ArtifactsWritten: written}
}
