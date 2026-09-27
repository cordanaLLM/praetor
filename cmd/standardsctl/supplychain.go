package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

func runSBOM(args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to repository to generate SBOM for")
	out := fs.String("out", "", "Output file path (default stdout)")
	moduleVersion := fs.String("module-version", "",
		"Version of the scanned module to record (default: its release tag on HEAD, omitted when HEAD has none)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bom, err := supplychain.GenerateCycloneDX(ctx, *path, supplychain.SBOMOptions{ModuleVersion: *moduleVersion})
	if err != nil {
		return fmt.Errorf("failed generating CycloneDX SBOM: %w", err)
	}

	data, err := json.MarshalIndent(bom, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SBOM: %w", err)
	}

	if *out != "" {
		if err := writeCommandArtifact(ctx, *out, data, 0644); err != nil {
			return fmt.Errorf("failed writing SBOM to %s: %w", *out, err)
		}
		fmt.Printf("CycloneDX 1.5 SBOM written to %s (%d components)\n", *out, len(bom.Components))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

// unsignedProvenanceWarning goes to stderr on every provenance run, so the JSON on stdout
// stays parseable while nobody mistakes the statement for a signed attestation.
const unsignedProvenanceWarning = "warning: the SLSA provenance statement is UNSIGNED; it is not an attestation " +
	"until it is wrapped in a signed DSSE envelope whose signature and signer identity are verified"

// provenanceFlags are the provenance command's subject sources, builder and output.
type provenanceFlags struct {
	file, artifact, builder, digest, checksums, out string
}

func runProvenance(args []string) error {
	flags, err := parseProvenanceFlags(args)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDigestDuration+contextopt.MaxDuration)
	defer cancel()

	stmt, err := provenanceStatement(ctx, flags)
	if err != nil {
		return fmt.Errorf("failed generating SLSA provenance: %w", err)
	}

	data, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SLSA statement: %w", err)
	}
	if err := supplychain.CheckInTotoStatement(data); err != nil {
		return fmt.Errorf("refusing a statement cosign verify-blob-attestation would reject: %w", err)
	}
	fmt.Fprintln(os.Stderr, unsignedProvenanceWarning)

	if flags.out != "" {
		if err := writeCommandArtifact(ctx, flags.out, data, 0644); err != nil {
			return fmt.Errorf("failed writing provenance to %s: %w", flags.out, err)
		}
		fmt.Printf("Unsigned SLSA v1.0 provenance statement written to %s (%s)\n", flags.out, subjectSummary(stmt.Subject))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

// parseProvenanceFlags reads the provenance flags, written in any order, and refuses a run
// with a positional argument, with no subject source, or with -checksums beside the
// single-artifact flags it replaces.
func parseProvenanceFlags(args []string) (provenanceFlags, error) {
	var f provenanceFlags
	fs := flag.NewFlagSet("provenance", flag.ContinueOnError)
	fs.StringVar(&f.file, "file", "", "Artifact file whose bytes the subject digest is computed from (required unless -checksums)")
	fs.StringVar(&f.artifact, "artifact", "", "Subject name (default: base name of -file)")
	fs.StringVar(&f.builder, "builder", "", "Builder identifier (default in GitHub Actions: $GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF; required elsewhere)")
	fs.StringVar(&f.digest, "digest", "", "Optional expected SHA-256 hex digest; the run fails unless -file hashes to it")
	fs.StringVar(&f.checksums, "checksums", "",
		"sha256sum manifest, such as GoReleaser's checksums.txt: every listed file beside it becomes a subject, "+
			"digested from its bytes and cross-checked against its line (excludes -file, -artifact and -digest)")
	fs.StringVar(&f.out, "out", "", "Output file path (default stdout)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return f, err
	}
	switch {
	case len(positional) > 0:
		return f, fmt.Errorf("provenance accepts no positional arguments, got %q", positional)
	case f.checksums != "" && (f.file != "" || f.artifact != "" || f.digest != ""):
		return f, fmt.Errorf("flag -checksums names every subject itself and excludes -file, -artifact and -digest")
	case f.checksums == "" && f.file == "":
		return f, fmt.Errorf("flag -file is required (or -checksums for every file a sha256sum manifest lists): " +
			"the subject digest is computed from the artifact bytes, and -digest is only a cross-check against them")
	}
	builder, err := resolveBuilder(f.builder)
	if err != nil {
		return f, err
	}
	f.builder = builder
	return f, nil
}

// resolveBuilder returns the -builder value, or in GitHub Actions the running workflow's
// identity ($GITHUB_SERVER_URL/$GITHUB_WORKFLOW_REF). Outside GitHub Actions there is no
// identity to derive, and no default names someone else's builder, so the flag is required.
func resolveBuilder(flagValue string) (string, error) {
	if flagValue != "" {
		trimmed := strings.TrimSpace(flagValue)
		if trimmed == "" {
			return "", fmt.Errorf("flag -builder cannot be blank")
		}
		return trimmed, nil
	}
	serverURL := strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL"))
	workflowRef := strings.TrimSpace(os.Getenv("GITHUB_WORKFLOW_REF"))
	if serverURL != "" && workflowRef != "" {
		return strings.TrimRight(serverURL, "/") + "/" + strings.TrimLeft(workflowRef, "/"), nil
	}
	return "", fmt.Errorf("flag -builder is required outside GitHub Actions (GITHUB_SERVER_URL and GITHUB_WORKFLOW_REF are unset)")
}

// provenanceStatement builds the statement from the one subject source f names.
func provenanceStatement(ctx context.Context, f provenanceFlags) (*supplychain.SLSAStatement, error) {
	if f.checksums != "" {
		return supplychain.GenerateSLSAProvenanceFromChecksums(ctx, supplychain.ChecksumsRequest{
			ManifestPath: f.checksums, BuilderID: f.builder,
		})
	}
	return supplychain.GenerateSLSAProvenance(ctx, supplychain.ProvenanceRequest{
		ArtifactPath: f.file, ArtifactName: f.artifact, BuilderID: f.builder, ExpectedSHA256: f.digest,
	})
}

// subjectSummary names the one subject of a single-artifact statement, or counts the
// subjects of a manifest statement.
func subjectSummary(subjects []supplychain.Subject) string {
	if len(subjects) == 1 {
		return fmt.Sprintf("subject %s sha256:%s", subjects[0].Name, subjects[0].Digest["sha256"])
	}
	return fmt.Sprintf("%d subjects", len(subjects))
}

// writeCommandArtifact preserves explicit public/private output modes and rejects
// stale observations, symlinks, and partial writes through the shared snapshot publisher.
func writeCommandArtifact(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	before, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return contextopt.ReplaceSnapshot(ctx, path, data, contextopt.ReplaceOptions{Expected: before, Exists: err == nil, Mode: mode})
}
