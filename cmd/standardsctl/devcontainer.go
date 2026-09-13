package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

type devContainerOptions struct {
	configPath, outputPath, sourceRoot, builderImage, baseImage string
	verify, force                                               bool
}

func parseDevContainerOptions(args []string) (devContainerOptions, error) {
	var opts devContainerOptions
	fs := flag.NewFlagSet("devcontainer", flag.ContinueOnError)
	fs.StringVar(&opts.configPath, "config", ".standards.yaml", "Path to .standards.yaml")
	fs.StringVar(&opts.outputPath, "output", ".devcontainer/devcontainer.json", "Target path for devcontainer.json")
	fs.BoolVar(&opts.verify, "verify", false, "Verify configuration against declared standards and recorded bootstrap inputs")
	fs.StringVar(&opts.sourceRoot, "source-root", "", "Explicit complete Praetor source checkout for a portable bootstrap bundle")
	fs.StringVar(&opts.builderImage, "builder-image", "", "Digest-pinned Go builder image (defaults to the reviewed bootstrap image)")
	fs.StringVar(&opts.baseImage, "base-image", "", "Digest-pinned DevContainer base image (defaults to the reviewed base)")
	fs.BoolVar(&opts.force, "force", false, "Replace only the reviewed generated DevContainer bundle files")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return opts, err
	}
	action := positionalAt(positional, 0, "generate")
	if action != "generate" && action != "verify" {
		return opts, fmt.Errorf("unknown devcontainer action: %s (supported: generate, verify)", action)
	}
	opts.verify = opts.verify || action == "verify"
	if opts.verify && (opts.sourceRoot != "" || opts.builderImage != "" || opts.baseImage != "" || opts.force) {
		return opts, fmt.Errorf("verify uses the recorded bootstrap specification; generation-only options are not accepted")
	}
	return opts, nil
}

func runDevContainer(args []string) error {
	opts, err := parseDevContainerOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	policy, policyErr := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: filepath.Dir(opts.configPath), ManifestPath: opts.configPath})
	if policyErr != nil {
		return fmt.Errorf("failed to resolve pinned catalog: %w", policyErr)
	}
	manifest := policy.Manifest
	features, err := config.ResolveDevContainerFeatures(ctx, policy)
	if err != nil {
		return fmt.Errorf("failed to resolve selected DevContainer features: %w", err)
	}
	dc, err := devcontainer.SynthesizeWithFeatures(manifest, features)
	if err != nil {
		return fmt.Errorf("failed to synthesize devcontainer: %w", err)
	}
	if opts.verify {
		fmt.Printf("Verifying %s against %s...\n", opts.outputPath, opts.configPath)
		if err := devcontainer.Verify(ctx, opts.outputPath, dc); err != nil {
			return fmt.Errorf("devcontainer verification failed: %w", err)
		}
		fmt.Println("[PASS] DevContainer configuration and recorded bootstrap inputs match; runtime execution remains a separate check.")
		return nil
	}
	return generateDevContainerBundle(ctx, manifest, dc, features, opts)
}

func generateDevContainerBundle(ctx context.Context, manifest *config.Manifest, dc *devcontainer.DevContainer, features []config.DevContainerFeature, opts devContainerOptions) error {
	bundle, err := devcontainer.PrepareBundle(ctx, dc.Name, manifest.Profiles, manifest.Facets, devcontainer.BootstrapOptions{SourceRoot: opts.sourceRoot, BuilderImage: opts.builderImage, BaseImage: opts.baseImage, Features: features})
	if err != nil {
		return fmt.Errorf("prepare devcontainer bootstrap: %w", err)
	}
	if err := devcontainer.WriteBundle(ctx, opts.outputPath, bundle, opts.force); err != nil {
		return fmt.Errorf("write devcontainer bundle: %w", err)
	}
	if bundle.Spec().State == devcontainer.BootstrapUnavailable {
		return fmt.Errorf("%w: %s; select --source-root before rebuilding the container", devcontainer.ErrBootstrapUnavailable, bundle.Spec().Reason)
	}
	fmt.Printf("[PREPARED, NOT EXECUTED] %s for %s; source %s (%d companions)\n", opts.outputPath, bundle.Config.Name, bundle.Spec().SourceSHA256, len(bundle.Artifacts))
	return nil
}
