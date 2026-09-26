package supplychain

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// sha256HexPattern matches a SHA-256 digest as exactly 64 lowercase hex characters, the
// only form an in-toto subject digest may take. Anything else -- wrong length, uppercase,
// a non-hex character -- names an artifact that cannot be verified against, so it is
// refused rather than compared against the digest computed from the artifact bytes.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MaxArtifactBytes bounds the artifact bytes one provenance statement digests. The
// digest is streamed, so the bound caps read time, not memory: 2 GiB covers every
// release archive and binary this repository builds with room to spare.
const MaxArtifactBytes int64 = 2 << 30

// emissionNotice is the human-readable half of the UnsignedEmission extension field.
const emissionNotice = "praetorctl emitted this in-toto statement unsigned. It is not an attestation on its own: " +
	"trust it only inside a signed DSSE envelope whose signature and signer identity you verify."

// SLSAStatement represents an in-toto v1 statement embedding SLSA v1.0 provenance.
type SLSAStatement struct {
	Type          string        `json:"_type"`
	Subject       []Subject     `json:"subject"`
	Emission      Emission      `json:"praetorEmission"`
	PredicateType string        `json:"predicateType"`
	Predicate     SLSAPredicate `json:"predicate"`
}

// Emission is an in-toto extension field that records how praetorctl produced the
// statement. The in-toto v1 parsing rules require consumers to ignore unrecognized fields
// (https://github.com/in-toto/attestation/blob/main/spec/v1/README.md#parsing-rules), so it
// never changes the meaning of the subject or predicate; it exists so that a reader of the
// bare JSON cannot mistake it for a verified attestation.
type Emission struct {
	// Signed is false: praetorctl has no signing step.
	Signed bool `json:"signed"`
	// SubjectDigest names where the subject digest came from.
	SubjectDigest string `json:"subjectDigest"`
	// Notice states what a consumer must do before trusting the statement.
	Notice string `json:"notice"`
}

// UnsignedEmission is the Emission every statement from GenerateSLSAProvenance carries.
func UnsignedEmission() Emission {
	return Emission{Signed: false, SubjectDigest: "computed-from-artifact-bytes", Notice: emissionNotice}
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

// ProvenanceRequest names the artifact a provenance statement describes. The subject
// digest is always computed from the bytes at ArtifactPath; a caller-supplied digest is
// only ever a cross-check, never the attested value.
type ProvenanceRequest struct {
	// ArtifactPath is the artifact file whose bytes are digested. Required.
	ArtifactPath string
	// ArtifactName is the subject name; empty uses the base name of ArtifactPath.
	ArtifactName string
	// BuilderID identifies the builder recorded in the predicate.
	BuilderID string
	// ExpectedSHA256, when set, must equal the digest computed from the artifact bytes;
	// a mismatch refuses the statement instead of attesting either value.
	ExpectedSHA256 string
	// MaxBytes bounds the artifact size; zero means MaxArtifactBytes, and a value above
	// MaxArtifactBytes is refused.
	MaxBytes int64
}

// GenerateSLSAProvenance constructs an unsigned in-toto SLSA v1.0 provenance statement
// whose subject digest is the SHA-256 of the artifact file's bytes. It does not sign the
// statement: the returned value carries the UnsignedEmission extension field, and it is not
// an attestation until a signer wraps it in a DSSE envelope that a verifier checks.
func GenerateSLSAProvenance(ctx context.Context, req ProvenanceRequest) (*SLSAStatement, error) {
	if ctx == nil {
		return nil, fmt.Errorf("slsa: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("slsa: context cancelled: %w", err)
	}
	subject, err := artifactSubject(ctx, req)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return &SLSAStatement{
		Type:          "https://in-toto.io/Statement/v1",
		Subject:       []Subject{subject},
		Emission:      UnsignedEmission(),
		PredicateType: "https://slsa.dev/provenance/v1",
		Predicate: SLSAPredicate{
			BuildDefinition: BuildDefinition{
				BuildType: "https://cordana.ai/slsa/build/v1",
				External: map[string]string{
					"builder": req.BuilderID,
				},
			},
			RunDetails: RunDetails{
				Builder: BuilderMetadata{
					ID: req.BuilderID,
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

// artifactSubject digests the artifact file and returns the subject that names it. An
// empty file, a file over the byte bound, and a file whose digest differs from a supplied
// expected digest are all refused.
func artifactSubject(ctx context.Context, req ProvenanceRequest) (Subject, error) {
	limit, err := validateProvenanceRequest(req)
	if err != nil {
		return Subject{}, err
	}
	digest, size, err := contextopt.DigestBinarySnapshot(ctx, req.ArtifactPath, limit)
	if err != nil {
		return Subject{}, fmt.Errorf("slsa: digest artifact %s: %w", req.ArtifactPath, err)
	}
	if size == 0 {
		return Subject{}, fmt.Errorf("slsa: artifact %s is empty; a zero-byte file is not a build output to attest", req.ArtifactPath)
	}
	if req.ExpectedSHA256 != "" && req.ExpectedSHA256 != digest {
		return Subject{}, fmt.Errorf("slsa: artifact %s hashes to sha256 %s, not the expected %s", req.ArtifactPath, digest, req.ExpectedSHA256)
	}
	name := req.ArtifactName
	if name == "" {
		name = filepath.Base(req.ArtifactPath)
	}
	return Subject{Name: name, Digest: map[string]string{"sha256": digest}}, nil
}

// validateProvenanceRequest checks the request before any file is read and returns the
// effective artifact byte bound.
func validateProvenanceRequest(req ProvenanceRequest) (int64, error) {
	if req.ArtifactPath == "" {
		return 0, fmt.Errorf("slsa: artifact path is required: the subject digest is computed from the artifact bytes, never taken on trust")
	}
	if req.ExpectedSHA256 != "" && !sha256HexPattern.MatchString(req.ExpectedSHA256) {
		return 0, fmt.Errorf("slsa: expected sha256 hex digest must be exactly 64 lowercase hex characters, got %q", req.ExpectedSHA256)
	}
	switch {
	case req.MaxBytes == 0:
		return MaxArtifactBytes, nil
	case req.MaxBytes < 0 || req.MaxBytes > MaxArtifactBytes:
		return 0, fmt.Errorf("slsa: artifact byte bound must be between 1 and %d, got %d", MaxArtifactBytes, req.MaxBytes)
	default:
		return req.MaxBytes, nil
	}
}
