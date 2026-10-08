// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/efficiency"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// efficiencyTimeout bounds one efficiency ledger run (HISS-02).
const efficiencyTimeout = 2 * time.Minute

type efficiencyFlags struct {
	format         string
	milestone      string
	limit          int
	root           string
	manifestPath   string
	forgeFile      string
	transcriptsDir string
	spendLogFile   string
}

func parseEfficiencyFlags(args []string) (*efficiencyFlags, error) {
	fs := flag.NewFlagSet("efficiency", flag.ContinueOnError)
	format := fs.String("format", "table", "Output format: table or json")
	jsonFlag := fs.Bool("json", false, "Alias for --format=json")
	milestone := fs.String("milestone", "", "Filter pull requests by milestone title")
	limit := fs.Int("limit", 20, "Maximum number of pull requests to report")
	path := fs.String("path", ".", "Repository root directory")
	manifestPath := fs.String("manifest", "", "Path to .standards.yaml manifest")
	forgeFile := fs.String("forge-records", "", "Path to merged pull requests JSON fixture")
	transcriptsDir := fs.String("transcripts-dir", "", "Path to agent session transcripts directory")
	spendLogFile := fs.String("spend-log", "", "Path to gateway spend-log export file")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	if len(positional) > 0 {
		return nil, fmt.Errorf("efficiency accepts no positional arguments, got %q", positional)
	}

	outFormat := strings.ToLower(strings.TrimSpace(*format))
	if *jsonFlag {
		outFormat = "json"
	}
	if outFormat != "table" && outFormat != "json" {
		return nil, fmt.Errorf("invalid format %q: must be 'table' or 'json'", outFormat)
	}

	root := *path
	if root == "" {
		root = "."
	}

	return &efficiencyFlags{
		format:         outFormat,
		milestone:      *milestone,
		limit:          *limit,
		root:           root,
		manifestPath:   *manifestPath,
		forgeFile:      *forgeFile,
		transcriptsDir: *transcriptsDir,
		spendLogFile:   *spendLogFile,
	}, nil
}

func loadEfficiencyPolicy(root, manifestPath string) (*config.EfficiencyPolicy, *config.Manifest) {
	effectiveManifestPath := filepath.Join(root, config.ManifestFileName)
	if manifestPath != "" {
		effectiveManifestPath = manifestPath
	}
	m, err := config.LoadManifest(effectiveManifestPath)
	if err != nil || m == nil {
		return nil, nil
	}
	return m.Efficiency, m
}

func resolveLiveForge(manifest *config.Manifest) forge.Forge {
	if manifest == nil || manifest.Repository.Owner == "" || manifest.Repository.Name == "" {
		return nil
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil
	}
	gh := forge.NewGitHubDriver(token, "")
	gh.SetRepository(manifest.Repository.Owner, manifest.Repository.Name)
	return gh
}

func runEfficiency(args []string) error {
	fl, err := parseEfficiencyFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(efficiencyTimeout)
	defer cancel()

	policy, manifest := loadEfficiencyPolicy(fl.root, fl.manifestPath)
	var forgeDriver forge.Forge
	if fl.forgeFile == "" && (policy == nil || policy.Sources.Forge.Path == "") {
		forgeDriver = resolveLiveForge(manifest)
	}

	collector := efficiency.NewCollector(efficiency.CollectorOptions{
		Root:           fl.root,
		Milestone:      fl.milestone,
		Limit:          fl.limit,
		Policy:         policy,
		ForgeDriver:    forgeDriver,
		ForgeJSONPath:  fl.forgeFile,
		TranscriptsDir: fl.transcriptsDir,
		SpendLogPath:   fl.spendLogFile,
	})

	report, err := collector.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect efficiency metrics: %w", err)
	}

	if fl.format == "json" {
		return efficiency.RenderJSON(report, os.Stdout)
	}
	return efficiency.RenderTable(report, os.Stdout)
}
