package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cordanaLLM/standards/internal/baseline"
	"github.com/cordanaLLM/standards/internal/compiler"
	"github.com/cordanaLLM/standards/internal/config"
	"gopkg.in/yaml.v3"
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

	manifest := config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner:      "cordanaLLM",
			Name:       "new-service",
			Visibility: "public",
		},
		Profiles: []string{*profile},
		Facets:   facetList,
	}

	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(*outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", *outputPath, err)
	}
	fmt.Printf("[CREATED] %s (Profile: %s, Facets: %v)\n", *outputPath, *profile, facetList)

	// Initialize baseline if missing
	if _, err := os.Stat(".standards-baseline.json"); os.IsNotExist(err) {
		base := &baseline.Baseline{
			Version:          1,
			TotalInfractions: 0,
			Infractions:      []baseline.Infraction{},
		}
		_ = baseline.SaveBaseline(".standards-baseline.json", base)
		fmt.Println("[CREATED] .standards-baseline.json (0 legacy infractions)")
	}

	// Initialize lockfile if missing
	if _, err := os.Stat(".standards.lock"); os.IsNotExist(err) {
		_ = os.WriteFile(".standards.lock", []byte("# SemVer lockfile\nversion: 1\npinned_version: \"v1.0.0\"\n"), 0644)
		fmt.Println("[CREATED] .standards.lock")
	}

	// Transpile agent context
	tr := compiler.NewTranspiler()
	if res, err := tr.Compile("AGENTS.md"); err == nil {
		_ = tr.WriteOutputs(res, ".")
		fmt.Println("[TRANSPILED] Cross-agent context targets initialized from AGENTS.md.")
	}

	fmt.Println("\nRepository successfully onboarded into cordanaLLM/standards!")
	fmt.Println("Next steps: run 'standardsctl audit' and 'make verify-all'.")
	return nil
}
