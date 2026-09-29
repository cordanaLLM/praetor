package supplychain

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
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

// SLSAStatement represents an in-toto v1 statement embedding SLSA v1.0 provenance. It holds
// exactly the four fields the in-toto v1 Statement defines
// (https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md) and nothing else:
// sigstore-go, which cosign verify-blob-attestation uses, decodes a DSSE payload with
// protojson and rejects any field the Statement message does not declare, so a single extra
// top-level field makes a signed statement unverifiable. CheckInTotoStatement enforces the
// shape on the encoded JSON.
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
	// ContentCheck decides what happens when the artifact's bytes are not the file type its
	// subject name declares (a .deb, an .efi image, a Unified Kernel Image): the zero value
	// and ContentCheckEnforce refuse the statement, ContentCheckReport attests the artifact
	// and returns its verdict as ContentUnverified.
	ContentCheck ContentCheck
}

// GenerateSLSAProvenance constructs an unsigned in-toto SLSA v1.0 provenance statement
// whose subject digest is the SHA-256 of the artifact file's bytes, and returns the verdict
// of the content check that ran over the same bytes. It does not sign the statement: a bare
// statement is never an attestation, and it becomes one only when a signer wraps it in a
// DSSE envelope that a verifier checks.
func GenerateSLSAProvenance(ctx context.Context, req ProvenanceRequest) (*SLSAStatement, []ContentVerdict, error) {
	if err := checkProvenanceContext(ctx); err != nil {
		return nil, nil, err
	}
	subject, verdict, err := artifactSubject(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	return newStatement([]Subject{subject}, req.BuilderID), []ContentVerdict{verdict}, nil
}

// ChecksumsRequest names a sha256sum manifest, such as the checksums.txt GoReleaser writes,
// whose every line becomes one subject of a single statement.
type ChecksumsRequest struct {
	// ManifestPath is the manifest file. Required. Every name it lists is resolved against
	// the manifest's own directory and must stay inside it.
	ManifestPath string
	// BuilderID identifies the builder recorded in the predicate.
	BuilderID string
	// MaxBytes bounds each listed artifact as ProvenanceRequest.MaxBytes does.
	MaxBytes int64
	// ContentCheck applies to each listed artifact as ProvenanceRequest.ContentCheck does;
	// under the zero value one mismatching file refuses the whole statement.
	ContentCheck ContentCheck
}

// GenerateSLSAProvenanceFromChecksums constructs one unsigned in-toto SLSA v1.0 provenance
// statement naming every file a sha256sum manifest lists. The manifest supplies the names;
// each digest is computed from the listed file's bytes, and the manifest's digest is only
// the cross-check ProvenanceRequest.ExpectedSHA256 is, so a manifest line that no longer
// matches its file refuses the whole statement instead of attesting either value. Each
// listed file's content check verdict is returned in manifest order. The release workflow
// signs the statement with `cosign attest-blob --statement`
// (.github/workflows/release-binaries.yml).
func GenerateSLSAProvenanceFromChecksums(ctx context.Context, req ChecksumsRequest) (*SLSAStatement, []ContentVerdict, error) {
	if err := checkProvenanceContext(ctx); err != nil {
		return nil, nil, err
	}
	if req.ManifestPath == "" {
		return nil, nil, fmt.Errorf("slsa: checksum manifest path is required")
	}
	if err := req.ContentCheck.validate(); err != nil {
		return nil, nil, err
	}
	data, err := contextopt.ReadSnapshot(ctx, req.ManifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("slsa: read checksum manifest %s: %w", req.ManifestPath, err)
	}
	listed, err := ParseChecksums(data)
	if err != nil {
		return nil, nil, err
	}
	subjects, verdicts, err := checksummedSubjects(ctx, filepath.Dir(req.ManifestPath), listed, req)
	if err != nil {
		return nil, nil, err
	}
	return newStatement(subjects, req.BuilderID), verdicts, nil
}

// checksummedSubjects digests every listed file under dir, cross-checking each against the
// digest its manifest line records and checking its content against its name.
func checksummedSubjects(ctx context.Context, dir string, listed []Subject, req ChecksumsRequest) ([]Subject, []ContentVerdict, error) {
	subjects := make([]Subject, 0, len(listed))
	verdicts := make([]ContentVerdict, 0, len(listed))
	for i := 0; i < len(listed) && i < maxProvenanceSubjects; i++ {
		rel := filepath.FromSlash(listed[i].Name)
		if !filepath.IsLocal(rel) {
			return nil, nil, fmt.Errorf("slsa: checksum line %d names %q, which is not a path inside %s", i+1, listed[i].Name, dir)
		}
		subject, verdict, err := artifactSubject(ctx, ProvenanceRequest{
			ArtifactPath: filepath.Join(dir, rel), ArtifactName: listed[i].Name,
			ExpectedSHA256: listed[i].Digest["sha256"], MaxBytes: req.MaxBytes, ContentCheck: req.ContentCheck,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("slsa: checksum line %d: %w", i+1, err)
		}
		subjects = append(subjects, subject)
		verdicts = append(verdicts, verdict)
	}
	return subjects, verdicts, nil
}

// checkProvenanceContext refuses a nil or already cancelled context before any file is read.
func checkProvenanceContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("slsa: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("slsa: context cancelled: %w", err)
	}
	return nil
}

// DefaultBuildType is the SLSA v1.0 build definition type every praetor provenance statement
// records: the project's own URI, not an operator's domain.
const DefaultBuildType = "https://cordanallm.github.io/praetor/slsa/build/v1"

// newStatement builds the unsigned statement naming subjects, whose digests the caller has
// already computed from the artifact bytes.
func newStatement(subjects []Subject, builderID string) *SLSAStatement {
	now := time.Now().UTC().Format(time.RFC3339)
	return &SLSAStatement{
		Type:          inTotoStatementType,
		Subject:       subjects,
		PredicateType: "https://slsa.dev/provenance/v1",
		Predicate: SLSAPredicate{
			BuildDefinition: BuildDefinition{
				BuildType: DefaultBuildType,
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
	}
}

// artifactSubject digests the artifact file, checks the same bytes against the file type
// the subject name declares, and returns the subject that names it with the content
// verdict. An empty file, a file over the byte bound, a file whose digest differs from a
// supplied expected digest, and under an enforced content check a file that is not what
// its name says are all refused.
func artifactSubject(ctx context.Context, req ProvenanceRequest) (Subject, ContentVerdict, error) {
	limit, err := validateProvenanceRequest(req)
	if err != nil {
		return Subject{}, ContentVerdict{}, err
	}
	name := req.ArtifactName
	if name == "" {
		name = filepath.Base(req.ArtifactPath)
	}
	// A nil probe converts to a nil observer: a name no content rule covers is only digested.
	format, probe := contentProbeFor(name)
	digest, size, err := contextopt.DigestBinarySnapshotTo(ctx, req.ArtifactPath, limit, probe)
	if err != nil {
		return Subject{}, ContentVerdict{}, fmt.Errorf("slsa: digest artifact %s: %w", req.ArtifactPath, err)
	}
	if size == 0 {
		return Subject{}, ContentVerdict{}, fmt.Errorf("slsa: artifact %s is empty; a zero-byte file is not a build output to attest", req.ArtifactPath)
	}
	if req.ExpectedSHA256 != "" && req.ExpectedSHA256 != digest {
		return Subject{}, ContentVerdict{}, fmt.Errorf("slsa: artifact %s hashes to sha256 %s, not the expected %s", req.ArtifactPath, digest, req.ExpectedSHA256)
	}
	verdict, err := judgeContent(req, name, format, probe, size)
	if err != nil {
		return Subject{}, ContentVerdict{}, err
	}
	return Subject{Name: name, Digest: map[string]string{"sha256": digest}}, verdict, nil
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
	if err := req.ContentCheck.validate(); err != nil {
		return 0, err
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

// maxProvenanceSubjects bounds the subjects one statement names and the lines one checksum
// manifest may hold (HISS-02). A release of four binaries on six platforms is 24 archives
// plus two SBOMs each, far below it.
const maxProvenanceSubjects = 1024

// validateSubjects refuses a subject list no verifier could match an artifact against: no
// subject, too many, a subject without a name, a name listed twice, or a digest that is not
// a SHA-256 hex string.
func validateSubjects(subjects []Subject) error {
	if len(subjects) == 0 {
		return fmt.Errorf("slsa: at least one subject is required")
	}
	if len(subjects) > maxProvenanceSubjects {
		return fmt.Errorf("slsa: %d subjects exceed the limit of %d", len(subjects), maxProvenanceSubjects)
	}
	seen := make(map[string]bool, len(subjects))
	for i := 0; i < len(subjects) && i < maxProvenanceSubjects; i++ {
		name := subjects[i].Name
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("slsa: subject %d has no name", i+1)
		}
		if seen[name] {
			return fmt.Errorf("slsa: subject %q is listed twice", name)
		}
		seen[name] = true
		if digest := subjects[i].Digest["sha256"]; !sha256HexPattern.MatchString(digest) {
			return fmt.Errorf("slsa: sha256 hex digest must be exactly 64 lowercase hex characters, got %q", digest)
		}
	}
	return nil
}

// ParseChecksums reads a sha256sum manifest, such as the checksums.txt GoReleaser writes,
// into one name and expected digest per line. A line is "<64 lowercase hex>  <name>" (text
// mode) or "<64 lowercase hex> *<name>" (binary mode); a trailing newline and CRLF line
// endings are accepted. Anything else fails the whole manifest rather than dropping the
// line, because a subject silently left out is an archive the provenance silently does not
// cover. The digests are what the manifest claims, not yet what the files hold:
// GenerateSLSAProvenanceFromChecksums recomputes each one from the file's bytes.
func ParseChecksums(data []byte) ([]Subject, error) {
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("slsa: checksum manifest holds no lines")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxProvenanceSubjects {
		return nil, fmt.Errorf("slsa: checksum manifest has %d lines, more than the limit of %d", len(lines), maxProvenanceSubjects)
	}
	subjects := make([]Subject, 0, len(lines))
	for i := 0; i < len(lines) && i < maxProvenanceSubjects; i++ {
		subject, err := parseChecksumLine(lines[i])
		if err != nil {
			return nil, fmt.Errorf("slsa: checksum line %d: %w", i+1, err)
		}
		subjects = append(subjects, subject)
	}
	if err := validateSubjects(subjects); err != nil {
		return nil, err
	}
	return subjects, nil
}

// checksumDigestLength is the length of a SHA-256 digest written as hex.
const checksumDigestLength = 64

// parseChecksumLine splits one sha256sum line into its digest and file name.
func parseChecksumLine(line string) (Subject, error) {
	if len(line) <= checksumDigestLength+2 {
		return Subject{}, fmt.Errorf("%q is not \"<sha256>  <name>\"", line)
	}
	digest, separator, name := line[:checksumDigestLength], line[checksumDigestLength:checksumDigestLength+2], line[checksumDigestLength+2:]
	if separator != "  " && separator != " *" {
		return Subject{}, fmt.Errorf("%q is not \"<sha256>  <name>\"", line)
	}
	if !sha256HexPattern.MatchString(digest) {
		return Subject{}, fmt.Errorf("digest %q is not 64 lowercase hex characters", digest)
	}
	return Subject{Name: name, Digest: map[string]string{"sha256": digest}}, nil
}
