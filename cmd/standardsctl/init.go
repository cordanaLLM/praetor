package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// initFilePerm is the mode of the scaffolded, tracked configuration files.
const initFilePerm os.FileMode = 0o644

// initIdentityTimeout bounds the settings read and the origin-remote lookup that resolve the
// identity init writes (HISS-02).
const initIdentityTimeout = 30 * time.Second

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	profile := fs.String("profile", "framework", "Primary repository profile")
	facets := fs.String("facets", strings.Join(config.DefaultFacets(), ","), "Comma-separated list of facets")
	outputPath := fs.String("output", ".standards.yaml", "Path to write .standards.yaml; its directory receives the companion files")
	lockSource := fs.String("lock-source-root", "", devcontainer.SourceRootForms+" the profile and facet pins in .standards.lock come from; "+
		"without it init writes an unpinned placeholder lock that audit and devcontainer generate refuse"+devcontainer.SourceRootGitNote)
	settings := registerOperatorSettingsFlags(fs)

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("init accepts no positional arguments, got %q", fs.Args())
	}

	if err := ensureManifestAbsent(*outputPath); err != nil {
		return err
	}
	resolvedLockSource, err := resolveLockSourceRoot(*lockSource, "--lock-source-root")
	if err != nil {
		return err
	}

	// Every companion file lives next to the manifest, never in the process cwd.
	rootDir := filepath.Dir(*outputPath)
	if !util.DirExists(rootDir) {
		return fmt.Errorf("failed to write %s: directory %s does not exist", *outputPath, rootDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), initIdentityTimeout)
	defer cancel()
	identity, err := initRepositoryIdentity(ctx, rootDir, settings)
	if err != nil {
		return err
	}
	manifest := initialManifest(*profile, util.SplitCSV(*facets), identity)
	if err := writeInitialManifest(*outputPath, &manifest); err != nil {
		return err
	}
	if err := initBaselineAndLockfile(ctx, rootDir, resolvedLockSource, &manifest); err != nil {
		return err
	}
	if err := initAgentContext(rootDir); err != nil {
		return err
	}

	fmt.Println("\n" + onboardedMessage(identity))
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

// initRepositoryIdentity resolves the repository.owner and repository.name init writes. The
// manifest does not exist yet, so config.ResolveRepositoryIdentity reads the origin remote of
// rootDir; without one the owner is forge.default_owner and the name stays empty. Nothing is
// invented: every value left empty is printed as a field to set. A remote read git did not
// answer, or unreadable settings, fail init instead of writing a guessed identity.
//
// repository.default_branch is the origin HEAD only this checkout records, when it is not main
// (forge.DefaultBranchToDeclare), so a CI checkout without it resolves the same default branch
// for the ruleset. An origin HEAD git did not answer or that cannot be rendered fails init too.
func initRepositoryIdentity(ctx context.Context, rootDir string, settings *operatorSettingsFlags) (config.RepositoryMetadata, error) {
	forgeSettings, err := loadForgeSettings(ctx, settings)
	if err != nil {
		return config.RepositoryMetadata{}, fmt.Errorf("init: %w", err)
	}
	owner, name, err := config.ResolveRepositoryIdentity(ctx, rootDir, "", "")
	if err != nil && !errors.Is(err, config.ErrOwnerUnknown) && !errors.Is(err, config.ErrRepositoryNameUnknown) {
		return config.RepositoryMetadata{}, fmt.Errorf("init: %w", err)
	}
	if err != nil {
		owner, name = forgeSettings.DefaultOwner, ""
	}
	if owner == "" {
		fmt.Printf("[WARN] repository.owner not detected; set it in %s\n", config.ManifestFileName)
	}
	if name == "" {
		fmt.Printf("[WARN] repository.name not detected; set it in %s\n", config.ManifestFileName)
	}
	branch, err := forge.DefaultBranchToDeclare(ctx, rootDir)
	if err != nil {
		return config.RepositoryMetadata{}, fmt.Errorf("init: %w", err)
	}
	return config.RepositoryMetadata{Owner: owner, Name: name, Visibility: "public", DefaultBranch: branch}, nil
}

// onboardedMessage is the closing line of init. It names the repository the manifest records
// (owner/name) and nothing else: Praetor ships no repository name of its own to print.
func onboardedMessage(identity config.RepositoryMetadata) string {
	if identity.Owner != "" && identity.Name != "" {
		return fmt.Sprintf("Repository successfully onboarded: %s/%s", identity.Owner, identity.Name)
	}
	return "Repository successfully onboarded."
}

func initialManifest(profile string, facets []string, identity config.RepositoryMetadata) config.Manifest {
	return config.Manifest{
		Version:    1,
		Repository: identity,
		Profiles:   []string{profile},
		Facets:     facets,
	}
}

func writeInitialManifest(outputPath string, manifest *config.Manifest) error {
	profile, facets, identity := manifest.Profiles[0], manifest.Facets, manifest.Repository
	data, err := config.RenderManifest(manifest)
	if err != nil {
		return fmt.Errorf("failed to render manifest: %w", err)
	}

	// Anchored at the manifest's directory, the repository root every companion file shares:
	// a link planted at the manifest path is refused instead of written through (BUG-826).
	if err := util.WriteFileAt(outputPath, data, initFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	fmt.Printf("[CREATED] %s (Profile: %s, Facets: %v)\n", outputPath, profile, facets)
	if identity.DefaultBranch != "" {
		fmt.Printf("[INFO] repository.default_branch: %s recorded from the origin remote's HEAD\n", identity.DefaultBranch)
	}
	return nil
}

func initBaselineAndLockfile(ctx context.Context, rootDir, lockSource string, manifest *config.Manifest) error {
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

	return initLockfile(ctx, rootDir, lockSource, manifest)
}

// initLockfile writes .standards.lock unless one exists. With a lock source it writes the lock
// adoption writes (config.BuildLockfile: every selected profile and facet pinned to its verified
// content digest), so devcontainer generate and audit accept a fresh repository. Without one it
// writes the unpinned placeholder and says so, because no digest can be computed without a source.
func initLockfile(ctx context.Context, rootDir, lockSource string, manifest *config.Manifest) error {
	lockPath := filepath.Join(rootDir, config.LockFileName)
	missing, err := fileMissing(lockPath)
	if err != nil || !missing {
		return err
	}
	var content []byte
	if lockSource != "" {
		content, err = config.BuildLockfile(ctx, lockSource, manifest)
		if err != nil {
			return fmt.Errorf("failed to pin %s to %s: %w", config.LockFileName, lockSource, err)
		}
	} else {
		content = placeholderLock(lockPath)
	}
	if err := util.WriteFileConfined(rootDir, config.LockFileName, content, initFilePerm); err != nil {
		return fmt.Errorf("failed to create lockfile: %w", err)
	}
	fmt.Printf("[CREATED] %s\n", lockPath)
	if lockSource == "" {
		return nil
	}
	// The lock pins digests of the catalog; audit and devcontainer generate read that catalog
	// from the repository, so it is materialized next to the lock exactly as adoption does.
	if err := adopt.MaterializePinnedCatalog(ctx, rootDir, lockSource); err != nil {
		return fmt.Errorf("failed to materialize the pinned catalog: %w", err)
	}
	fmt.Printf("[CREATED] %s (pinned catalog)\n", filepath.Join(rootDir, ".config", "archetypes"))
	return nil
}

// placeholderLock is the lock init writes without a lock source: the running build's version and
// no digests. It prints what that costs and the command that pins it.
func placeholderLock(lockPath string) []byte {
	pinned, identified := lockVersion()
	if !identified {
		fmt.Printf("[WARN] %s records pinned_version %q: this build carries no release "+
			"version, no VCS stamp and no module version, so the lock cannot say which "+
			"praetor governed this repository. Re-run init from a released binary, a "+
			"VCS-stamped build or a go install module@version build.\n",
			lockPath, pinned)
	}
	fmt.Printf("[WARN] %s carries no content digests: audit and devcontainer generate refuse it. "+
		"Pin it with 'praetorctl profile set --lock-source-root=%s' or re-run init with --lock-source-root.\n",
		lockPath, adopt.LockSourcePlaceholder)
	return fmt.Appendf(nil, "# SemVer lockfile\nversion: 1\npinned_version: %q\n", pinned)
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
	// compile-context's write: Git ignores the evidence directory the text register block names,
	// every agent context surface is written and the caveman lint runs over it, so a freshly
	// initialised repository passes compile-context --verify or init fails saying why (BUG-604).
	ctx, cancel := context.WithTimeout(context.Background(), compileContextTimeout)
	defer cancel()
	if err := adopt.CompileAgentContext(ctx, os.Stdout, compiler.NewTranspiler(), agentsPath, rootDir); err != nil {
		return fmt.Errorf("failed to compile AGENTS.md; the manifest is already written, so fix the cause and run "+
			"'praetorctl compile-context': %w", err)
	}
	fmt.Println("[TRANSPILED] Cross-agent context targets initialized from AGENTS.md.")
	return nil
}
