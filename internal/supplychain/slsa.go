package supplychain

import (
	"context"
	"fmt"
	"time"
)

// SLSAStatement represents an in-toto v1 statement embedding SLSA v1.0 provenance.
type SLSAStatement struct {
	Type          string        `json:"_type"`
	Subject       []Subject     `json:"subject"`
	PredicateType string        `json:"predicateType"`
	Predicate     SLSAPredicate `json:"predicate"`
}

// Subject describes the artifact being attested.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// SLSAPredicate represents the SLSA v1.0 Provenance predicate.
type SLSAPredicate struct {
	BuildDefinition BuildDefinition `json:"buildDefinition"`
	RunDetails      RunDetails      `json:"runDetails"`
}

// BuildDefinition defines the build environment and entry point.
type BuildDefinition struct {
	BuildType string            `json:"buildType"`
	External  map[string]string `json:"externalParameters"`
}

// RunDetails holds execution details including builder identity.
type RunDetails struct {
	Builder  BuilderMetadata `json:"builder"`
	Metadata BuildMetadata   `json:"metadata"`
}

// BuilderMetadata identifies the trusted builder.
type BuilderMetadata struct {
	ID string `json:"id"`
}

// BuildMetadata records execution timestamps.
type BuildMetadata struct {
	InvocationID string `json:"invocationId"`
	StartedOn    string `json:"startedOn"`
	FinishedOn   string `json:"finishedOn"`
}

// GenerateSLSAProvenance constructs an in-toto SLSA v1.0 provenance statement.
func GenerateSLSAProvenance(ctx context.Context, artifactName, builderID, sha256Hex string) (*SLSAStatement, error) {
	if ctx == nil {
		return nil, fmt.Errorf("slsa: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("slsa: context cancelled: %w", err)
	}
	if sha256Hex == "" {
		return nil, fmt.Errorf("slsa: sha256 hex digest cannot be empty")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return &SLSAStatement{
		Type: "https://in-toto.io/Statement/v1",
		Subject: []Subject{
			{
				Name: artifactName,
				Digest: map[string]string{
					"sha256": sha256Hex,
				},
			},
		},
		PredicateType: "https://slsa.dev/provenance/v1",
		Predicate: SLSAPredicate{
			BuildDefinition: BuildDefinition{
				BuildType: "https://cordana.ai/slsa/build/v1",
				External: map[string]string{
					"builder": builderID,
				},
			},
			RunDetails: RunDetails{
				Builder: BuilderMetadata{
					ID: builderID,
				},
				Metadata: BuildMetadata{
					InvocationID: fmt.Sprintf("run-%d", time.Now().UnixNano()),
					StartedOn:    now,
					FinishedOn:   now,
				},
			},
		},
	}, nil
}
