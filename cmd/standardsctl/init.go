package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// initFilePerm is the mode of the scaffolded, tracked configuration files.
const initFilePerm os.FileMode = 0o644

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	profile := fs.String("profile", "framework", "Primary repository profile")
	facets := fs.String("facets", "security:high,api:public-contract,docs:seo-portal", "Comma-separated list of facets")
	outputPath := fs.String("output", ".standards.yaml", "Path to write .standards.yaml; its directory receives the companion files")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("init accepts no positional arguments, got %q", fs.Args())
	}

	if err := ensureManifestAbsent(*outputPath); err != nil {
		return err
	}

	// Every companion file lives next to the manifest, never in the process cwd.
	rootDir := filepath.Dir(*outputPath)
	if err := createInitialManifest(*outputPath, *profile, splitCSV(*facets)); err != nil {
		return err
	}
	if err := initBaselineAndLockfile(rootDir); err != nil {
		return err
	}
	if err := initAgentContext(rootDir); err != nil {
		return err
	}

	fmt.Println("\nRepository successfully onboarded into cordanaLLM/praetor!")
	fmt.Println("Next steps: run 'praetorctl audit' and 'make verify-all'.")
	return nil
}

// ensureManifestAbsent refuses to overwrite an existing manifest and surfaces any stat
// error other than "not found" instead of proceeding blindly.
func ensureManifestAbsent(path string) error {
	missing, err := fileMissing(path)
	if err != nil {
		return err
	}
	if !missing {
		return fmt.Errorf("%s already exists; use 'praetorctl plan' or 'praetorctl sync' instead", path)
	}
	return nil
}

// fileMissing reports whether path does not exist; any other stat error is returned so
// that an unreadable file is never mistaken for an absent one.
func fileMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, fmt.Errorf("cannot inspect %s: %w", path, err)
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

	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := util.WriteFileSecure(outputPath, data, initFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	fmt.Printf("[CREATED] %s (Profile: %s, Facets: %v)\n", outputPath, profile, facets)
	return nil
}

func initBaselineAndLockfile(rootDir string) error {
	baselinePath := filepath.Join(rootDir, ".standards-baseline.json")
	missing, err := fileMissing(baselinePath)
	if err != nil {
		return err
	}
	if missing {
		base := &baseline.Baseline{
			Version:          1,
			TotalInfractions: 0,
			Infractions:      []baseline.Infraction{},
		}
		if err := baseline.SaveBaseline(baselinePath, base); err != nil {
			return fmt.Errorf("failed to create baseline: %w", err)
		}
		fmt.Printf("[CREATED] %s (0 legacy infractions)\n", baselinePath)
	}

	lockPath := filepath.Join(rootDir, ".standards.lock")
	missing, err = fileMissing(lockPath)
	if err != nil {
		return err
	}
	if missing {
		pinned, identified := lockVersion()
		if !identified {
			fmt.Printf("[WARN] %s records pinned_version %q: this build carries no release "+
				"version and no VCS stamp, so the lock cannot say which praetor governed this "+
				"repository. Re-run init from a released binary or a VCS-stamped build.\n",
				lockPath, pinned)
		}
		content := fmt.Appendf(nil, "# SemVer lockfile\nversion: 1\npinned_version: %q\n", pinned)
		if err := util.WriteFileSecure(lockPath, content, initFilePerm); err != nil {
			return fmt.Errorf("failed to create lockfile: %w", err)
		}
		fmt.Printf("[CREATED] %s\n", lockPath)
	}
	return nil
}

func initAgentContext(rootDir string) error {
	agentsPath := filepath.Join(rootDir, "AGENTS.md")
	missing, err := fileMissing(agentsPath)
	if err != nil {
		return err
	}
	if missing {
		return nil
	}
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(agentsPath)
	if err != nil {
		return fmt.Errorf("failed to compile AGENTS.md: %w", err)
	}
	if err := tr.WriteOutputs(res, rootDir); err != nil {
		return fmt.Errorf("failed to write agent outputs: %w", err)
	}
	fmt.Println("[TRANSPILED] Cross-agent context targets initialized from AGENTS.md.")
	return nil
}
