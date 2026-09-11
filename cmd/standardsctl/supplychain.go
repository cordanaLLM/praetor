package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/standards/internal/supplychain"
)

func runSBOM(args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to repository to generate SBOM for")
	out := fs.String("out", "", "Output file path (default stdout)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bom, err := supplychain.GenerateCycloneDX(ctx, *path)
	if err != nil {
		return fmt.Errorf("failed generating CycloneDX SBOM: %w", err)
	}

	data, err := json.MarshalIndent(bom, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SBOM: %w", err)
	}

	if *out != "" {
		if err := os.WriteFile(*out, data, 0644); err != nil {
			return fmt.Errorf("failed writing SBOM to %s: %w", *out, err)
		}
		fmt.Printf("CycloneDX 1.5 SBOM written to %s (%d components)\n", *out, len(bom.Components))
		return nil
	}

	fmt.Println(string(data))
	return nil
}

func runProvenance(args []string) error {
	fs := flag.NewFlagSet("provenance", flag.ContinueOnError)
	artifact := fs.String("artifact", "praetorctl", "Name of the artifact")
	builder := fs.String("builder", "ghcr.io/cordanallm/builder", "Builder identifier")
	digest := fs.String("digest", "", "SHA256 hex digest of the artifact")
	out := fs.String("out", "", "Output file path (default stdout)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *digest == "" {
		return fmt.Errorf("flag -digest is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stmt, err := supplychain.GenerateSLSAProvenance(ctx, *artifact, *builder, *digest)
	if err != nil {
		return fmt.Errorf("failed generating SLSA provenance: %w", err)
	}

	data, err := json.MarshalIndent(stmt, "", "  ")
	if err != nil {
		return fmt.Errorf("failed formatting SLSA statement: %w", err)
	}

	if *out != "" {
		if err := os.WriteFile(*out, data, 0644); err != nil {
			return fmt.Errorf("failed writing provenance to %s: %w", *out, err)
		}
		fmt.Printf("SLSA v1.0 Provenance written to %s\n", *out)
		return nil
	}

	fmt.Println(string(data))
	return nil
}
