package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

type devContainerOptions struct {
	configPath, outputPath, sourceRoot, builderImage, baseImage string
	verify, force, freshness, bump                              bool
}

// devContainerFreshnessBound bounds one freshness check: it recaptures the build source, as
// preparation does, and reads three git answers.
const devContainerFreshnessBound = 5 * time.Minute

// devContainerBumpBound bounds one bump command: resolving the declared policy, then
// devcontainer.Bump, which edits the pin source, the prior list and the Dockerfiles, captures
// the source once for the regenerated bundle, writes it and verifies it, and holds its own
// part to the same ten minutes.
const devContainerBumpBound = 10 * time.Minute

func parseDevContainerOptions(args []string) (devContainerOptions, error) {
	var opts devContainerOptions
	fs := flag.NewFlagSet("devcontainer", flag.ContinueOnError)
	fs.StringVar(&opts.configPath, "config", ".standards.yaml", "Path to .standards.yaml")
	fs.StringVar(&opts.outputPath, "output", ".devcontainer/devcontainer.json", "Target path for devcontainer.json")
	fs.BoolVar(&opts.verify, "verify", false, "Verify configuration against declared standards and recorded bootstrap inputs")
	fs.StringVar(&opts.sourceRoot, "source-root", "", "Explicit complete Praetor source checkout for a portable bootstrap bundle (bump: the checkout whose pins move, default the directory of --config)")
	fs.StringVar(&opts.builderImage, "builder-image", "", "Digest-pinned Go builder image, repository[:tag]@sha256:<digest> (default: the recorded image unless it names a reviewed default's repository and digest, else the reviewed bootstrap image; bump: the new reviewed pin, repository:tag@sha256:<digest>)")
	fs.StringVar(&opts.baseImage, "base-image", "", "Digest-pinned DevContainer base image, repository[:tag]@sha256:<digest> (default: the recorded image unless it names a reviewed default's repository and digest, else the reviewed base; bump: the new reviewed pin, repository:tag@sha256:<digest>)")
	fs.BoolVar(&opts.force, "force", false, "Replace only the reviewed generated DevContainer bundle files")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return opts, err
	}
	return opts, applyDevContainerAction(&opts, positionalAt(positional, 0, "generate"))
}

// applyDevContainerAction selects verify, freshness or bump from the positional action and
// refuses options the selected action would ignore.
func applyDevContainerAction(opts *devContainerOptions, action string) error {
	selected := *opts
	switch action {
	case "freshness":
		selected.freshness = true
	case "bump":
		selected.bump = true
	case "generate", "verify":
		selected.verify = opts.verify || action == "verify"
	default:
		return fmt.Errorf("unknown devcontainer action: %s (supported: generate, verify, freshness, bump)", action)
	}
	if err := selected.refuseIgnoredOptions(); err != nil {
		return err
	}
	*opts = selected
	return nil
}

// refuseIgnoredOptions names the options the selected action would ignore: freshness and bump
// take no --verify, freshness no generation option, bump no --force, and verify no
// generation-only option.
func (o devContainerOptions) refuseIgnoredOptions() error {
	switch {
	case o.freshness && (o.verify || o.generationSelected()):
		return errors.New("freshness measures the committed bundle; generation and verify options are not accepted")
	case o.bump && (o.verify || o.force):
		return errors.New("bump regenerates and verifies the bundle itself; --verify and --force are not accepted")
	case o.verify && o.generationSelected():
		return errors.New("verify uses the recorded bootstrap specification; generation-only options are not accepted")
	}
	return nil
}

// generationSelected reports whether any generation-only option was given.
func (o devContainerOptions) generationSelected() bool {
	return o.sourceRoot != "" || o.builderImage != "" || o.baseImage != "" || o.force
}

func runDevContainer(args []string) error {
	opts, err := parseDevContainerOptions(args)
	if err != nil {
		return err
	}
	if opts.freshness {
		return runDevContainerFreshness(opts)
	}
	if opts.bump {
		return runDevContainerBump(opts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manifest, features, dc, err := declaredDevContainer(ctx, opts.configPath)
	if err != nil {
		return err
	}
	if opts.verify {
		fmt.Printf("Verifying %s against %s...\n", opts.outputPath, opts.configPath)
		if err := devcontainer.Verify(ctx, opts.outputPath, dc); err != nil {
			return fmt.Errorf("devcontainer verification failed: %w", devContainerCheckoutRemedy(ctx, filepath.Dir(opts.configPath), err))
		}
		fmt.Println("[PASS] DevContainer configuration and recorded bootstrap inputs match; runtime execution remains a separate check.")
		return nil
	}
	return generateDevContainerBundle(ctx, manifest, dc, features, opts)
}

// declaredDevContainer resolves the pinned catalog of the manifest at configPath and returns
// the manifest, its selected features and the configuration they synthesize.
func declaredDevContainer(ctx context.Context, configPath string) (*config.Manifest, []config.DevContainerFeature, *devcontainer.DevContainer, error) {
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: filepath.Dir(configPath), ManifestPath: configPath})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to resolve pinned catalog: %w", err)
	}
	features, err := config.ResolveDevContainerFeatures(ctx, policy)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to resolve selected DevContainer features: %w", err)
	}
	dc, err := devcontainer.SynthesizeWithFeatures(policy.Manifest, features)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to synthesize devcontainer: %w", err)
	}
	return policy.Manifest, features, dc, nil
}

// runDevContainerBump moves the reviewed default images of the Praetor checkout at the
// source root (the directory of --config unless given) to the selected pins, records the
// replaced ones, regenerates the bundle at the output path and verifies it (#323).
func runDevContainerBump(opts devContainerOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), devContainerBumpBound)
	defer cancel()
	manifest, features, dc, err := declaredDevContainer(ctx, opts.configPath)
	if err != nil {
		return err
	}
	sourceRoot := opts.sourceRoot
	if sourceRoot == "" {
		sourceRoot = filepath.Dir(opts.configPath)
	}
	changes, err := devcontainer.Bump(ctx, devcontainer.BumpOptions{
		SourceRoot: sourceRoot, Output: opts.outputPath, Name: dc.Name, Profiles: manifest.Profiles, Facets: manifest.Facets,
		Features: features, Expected: dc, BuilderImage: opts.builderImage, BaseImage: opts.baseImage,
	})
	if err != nil {
		return fmt.Errorf("devcontainer bump failed: %w", err)
	}
	for _, change := range changes {
		fmt.Println(change)
	}
	fmt.Printf("[PASS] %s regenerated from %s and verified; commit the pins, %s and the bundle together.\n", opts.outputPath, sourceRoot, devcontainer.PriorImagesFile)
	return nil
}

func generateDevContainerBundle(ctx context.Context, manifest *config.Manifest, dc *devcontainer.DevContainer, features []config.DevContainerFeature, opts devContainerOptions) error {
	// A flag left unset keeps the image the replaced bundle records (#536).
	selected := devcontainer.BootstrapOptions{SourceRoot: opts.sourceRoot, BuilderImage: opts.builderImage, BaseImage: opts.baseImage, Features: features}
	selected, notes, err := devcontainer.InheritRecordedImages(ctx, opts.outputPath, selected)
	if err != nil {
		return err
	}
	bundle, err := devcontainer.PrepareBundle(ctx, dc.Name, manifest.Profiles, manifest.Facets, selected)
	if err != nil {
		return fmt.Errorf("prepare devcontainer bootstrap: %w", err)
	}
	if err := devcontainer.WriteBundle(ctx, opts.outputPath, bundle, opts.force); err != nil {
		return fmt.Errorf("write devcontainer bundle: %w", err)
	}
	for _, note := range notes {
		fmt.Println(note)
	}
	if bundle.Spec().State == devcontainer.BootstrapUnavailable {
		return fmt.Errorf("%w: %s; rerun generate with --source-root <complete Praetor checkout> before rebuilding the container "+
			"(it replaces this unedited placeholder without --force)", devcontainer.ErrBootstrapUnavailable, bundle.Spec().Reason)
	}
	fmt.Printf("[PREPARED, NOT EXECUTED] %s for %s; source %s (%d companions)\n", opts.outputPath, bundle.Config.Name, bundle.Spec().SourceSHA256, len(bundle.Artifacts))
	return nil
}

// runDevContainerFreshness reports how far the committed bundle at the output path is behind
// the working tree beside the manifest, and fails past the manifest's devcontainer.freshness
// bounds (#338).
func runDevContainerFreshness(opts devContainerOptions) error {
	manifest, err := config.LoadManifest(opts.configPath)
	if err != nil {
		return err
	}
	maxCommits, maxAgeDays := manifest.DevContainer.FreshnessBounds()
	bounds := devcontainer.FreshnessBounds{MaxCommits: maxCommits, MaxAge: time.Duration(maxAgeDays) * 24 * time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), devContainerFreshnessBound)
	defer cancel()
	report, err := devcontainer.CheckFreshness(ctx, filepath.Dir(opts.configPath), opts.outputPath, bounds, time.Now())
	if err != nil {
		return fmt.Errorf("devcontainer freshness check failed: %w", err)
	}
	if err := report.Err(); err != nil {
		return err
	}
	label := "[FRESH]"
	if !report.Fresh() {
		label = "[DRIFT WITHIN BOUNDS]"
	}
	fmt.Println(label, report.String())
	return nil
}
