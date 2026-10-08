// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/efficiency"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
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

func loadEfficiencyPolicy(root, manifestPath string) (*config.EfficiencyPolicy, *config.Manifest, error) {
	effectiveManifestPath := filepath.Join(root, config.ManifestFileName)
	if manifestPath != "" {
		effectiveManifestPath = manifestPath
	}
	m, err := config.LoadManifest(effectiveManifestPath)
	if err != nil {
		if manifestPath == "" && errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load manifest %s: %w", effectiveManifestPath, err)
	}
	if m == nil {
		return nil, nil, nil
	}
	return m.Efficiency, m, nil
}

// resolveLiveForge builds the forge driver of the manifest repository from the shared token
// resolver. A missing repository or token leaves the forge unmeasured with the reason in
// note; an unsupported forge kind is an error.
func resolveLiveForge(ctx context.Context, manifest *config.Manifest) (driver forge.Forge, note string, err error) {
	if manifest == nil || manifest.Repository.Owner == "" || manifest.Repository.Name == "" {
		return nil, "forge not measured: the manifest declares no repository owner and name", nil
	}
	token := util.ResolveAuthTokenContext(ctx, "")
	if token == "" {
		return nil, "forge not measured: no forge token available (set GITHUB_TOKEN/GH_TOKEN or sign in with gh)", nil
	}
	provider := string(manifest.Repository.Forge)
	if provider == "" {
		provider = "github"
	}
	driver, err = forge.NewForge(provider, token, "")
	if err != nil {
		return nil, "", fmt.Errorf("create %s forge driver: %w", provider, err)
	}
	if gh, ok := driver.(*forge.GitHubDriver); ok {
		gh.SetRepository(manifest.Repository.Owner, manifest.Repository.Name)
	}
	return driver, "", nil
}

func runEfficiency(args []string) error {
	return runEfficiencyTo(args, os.Stdout)
}

func runEfficiencyTo(args []string, out io.Writer) error {
	fl, err := parseEfficiencyFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(efficiencyTimeout)
	defer cancel()

	policy, manifest, err := loadEfficiencyPolicy(fl.root, fl.manifestPath)
	if err != nil {
		return err
	}
	var forgeDriver forge.Forge
	var notes []string
	if fl.forgeFile == "" && (policy == nil || policy.Sources.Forge.Path == "") {
		driver, note, liveErr := resolveLiveForge(ctx, manifest)
		if liveErr != nil {
			return liveErr
		}
		forgeDriver = driver
		if note != "" {
			notes = append(notes, note)
		}
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
		Notes:          notes,
	})

	report, err := collector.Collect(ctx)
	if err != nil {
		return fmt.Errorf("collect efficiency metrics: %w", err)
	}

	if fl.format == "json" {
		return efficiency.RenderJSON(report, out)
	}
	return efficiency.RenderTable(report, out)
}
