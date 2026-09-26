package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
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

	if err := fs.Parse(args); err != nil {
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

func runProvenance(args []string) error {
	fs := flag.NewFlagSet("provenance", flag.ContinueOnError)
	file := fs.String("file", "", "Artifact file whose bytes the subject digest is computed from (required)")
	artifact := fs.String("artifact", "", "Subject name (default: base name of -file)")
	builder := fs.String("builder", "ghcr.io/cordanallm/builder", "Builder identifier")
	digest := fs.String("digest", "", "Optional expected SHA-256 hex digest; the run fails unless -file hashes to it")
	out := fs.String("out", "", "Output file path (default stdout)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("flag -file is required: the subject digest is computed from the artifact bytes, and -digest is only a cross-check against them")
	}

	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDigestDuration+contextopt.MaxDuration)
	defer cancel()

	stmt, err := supplychain.GenerateSLSAProvenance(ctx, supplychain.ProvenanceRequest{
		ArtifactPath: *file, ArtifactName: *artifact, BuilderID: *builder, ExpectedSHA256: *digest,
	})
	if err != nil {
		return fmt.Errorf("failed generating SLSA provenance: %w", err)
	}

	data, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SLSA statement: %w", err)
	}
	fmt.Fprintln(os.Stderr, unsignedProvenanceWarning)

	if *out != "" {
		if err := writeCommandArtifact(ctx, *out, data, 0644); err != nil {
			return fmt.Errorf("failed writing provenance to %s: %w", *out, err)
		}
		fmt.Printf("Unsigned SLSA v1.0 provenance statement written to %s (subject %s sha256:%s)\n",
			*out, stmt.Subject[0].Name, stmt.Subject[0].Digest["sha256"])
		return nil
	}

	fmt.Println(string(data))
	return nil
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
