package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cordanallm/praetor/internal/baseline"
	"github.com/cordanallm/praetor/internal/compiler"
	"github.com/cordanallm/praetor/internal/config"
	"github.com/golusoris/golusoris/core/codec/yaml"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	profile := fs.String("profile", "framework", "Primary repository profile")
	facets := fs.String("facets", "security:high,api:public-contract,docs:seo-portal", "Comma-separated list of facets")
	outputPath := fs.String("output", ".standards.yaml", "Path to write .standards.yaml")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*outputPath); err == nil {
		return fmt.Errorf("%s already exists; use 'standardsctl plan' or 'standardsctl sync' instead", *outputPath)
	}

	var facetList []string
	for _, f := range strings.Split(*facets, ",") {
		trimmed := strings.TrimSpace(f)
		if trimmed != "" {
			facetList = append(facetList, trimmed)
		}
	}

	if err := createInitialManifest(*outputPath, *profile, facetList); err != nil {
		return err
	}
	if err := initBaselineAndLockfile(); err != nil {
		return err
	}
	if err := initAgentContext(); err != nil {
		return err
	}

	fmt.Println("\nRepository successfully onboarded into cordanaLLM/standards!")
	fmt.Println("Next steps: run 'standardsctl audit' and 'make verify-all'.")
	return nil
}

func createInitialManifest(outputPath, profile string, facets []string) error {
	manifest := config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner:      "cordanaLLM",
			Name:       "new-service",
			Visibility: "public",
		},
		Profiles: []string{profile},
		Facets:   facets,
	}

	if err := yaml.WriteFile(outputPath, &manifest, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	fmt.Printf("[CREATED] %s (Profile: %s, Facets: %v)\n", outputPath, profile, facets)
	return nil
}

func initBaselineAndLockfile() error {
	if _, err := os.Stat(".standards-baseline.json"); os.IsNotExist(err) {
		base := &baseline.Baseline{
			Version:          1,
			TotalInfractions: 0,
			Infractions:      []baseline.Infraction{},
		}
		if err := baseline.SaveBaseline(".standards-baseline.json", base); err != nil {
			return fmt.Errorf("failed to create baseline: %w", err)
		}
		fmt.Println("[CREATED] .standards-baseline.json (0 legacy infractions)")
	}

	if _, err := os.Stat(".standards.lock"); os.IsNotExist(err) {
		content := []byte("# SemVer lockfile\nversion: 1\npinned_version: \"v1.0.0\"\n")
		if err := os.WriteFile(".standards.lock", content, 0644); err != nil {
			return fmt.Errorf("failed to create lockfile: %w", err)
		}
		fmt.Println("[CREATED] .standards.lock")
	}
	return nil
}

func initAgentContext() error {
	if _, err := os.Stat("AGENTS.md"); os.IsNotExist(err) {
		return nil
	}
	tr := compiler.NewTranspiler().WithRepoName(compiler.RepoNameFromDir("."))
	res, err := tr.Compile("AGENTS.md")
	if err != nil {
		return fmt.Errorf("failed to compile AGENTS.md: %w", err)
	}
	if err := tr.WriteOutputs(res, "."); err != nil {
		return fmt.Errorf("failed to write agent outputs: %w", err)
	}
	fmt.Println("[TRANSPILED] Cross-agent context targets initialized from AGENTS.md.")
	return nil
}
