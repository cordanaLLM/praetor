package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

// Use the same preserved or planned manifest as the policy resolver and audit.
// Inferred repository identity and runtime markers must not override that input.
// A forced re-adoption keeps the images the bundle at path records, by the rule
// devcontainer generate applies (#536), and reports each one it keeps or refreshes.
func prepareAdoptDevContainer(ctx context.Context, s *adoptSession, path string) (*devcontainer.Bundle, error) {
	data, err := plannedManifestBytes(ctx, s)
	if err != nil {
		return nil, err
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return nil, err
	}
	baseline, err := devcontainer.Synthesize(manifest)
	if err != nil {
		return nil, err
	}
	var features []config.DevContainerFeature
	if s.policy != nil {
		features, err = config.ResolveDevContainerFeatures(ctx, s.policy)
		if err != nil {
			return nil, fmt.Errorf("resolve selected DevContainer features: %w", err)
		}
	}
	options, notes, err := devcontainer.InheritRecordedImages(ctx, path, devcontainer.BootstrapOptions{SourceRoot: s.opts.LockSourceRoot, Features: features})
	if err != nil {
		return nil, err
	}
	for _, note := range notes {
		s.report.addWarning("%s", note)
	}
	return devcontainer.PrepareBundle(ctx, baseline.Name, manifest.Profiles, manifest.Facets, options)
}

// devContainerPreserved returns the DevContainer configuration path and whether the
// dev-container step keeps the file there as it is: it exists and the run is not forced. A
// preserved file is never regenerated, so the step captures no bootstrap source.
func (s *adoptSession) devContainerPreserved() (string, bool, error) {
	full, err := repoFile(s.repoPath, devcontainerFile)
	if err != nil {
		return "", false, err
	}
	return full, fileExists(full) && !s.opts.Force, nil
}

// preflightDevContainerSource refuses, before the first step writes anything, a lock source the
// dev-container step would refuse to capture (devcontainer.CheckSource), such as one holding
// go.mod outside any Git checkout. Checked only in that step, the refusal came after the
// manifest, the ignore rules, the lock, the policy catalog, the baseline and the agent harness
// were written, and left a half-adopted repository (#839). A declined step, a run without a lock
// source and a DevContainer the step preserves capture nothing and are not checked.
func preflightDevContainerSource(ctx context.Context, s *adoptSession, declined map[string]bool) error {
	if declined[devContainerStep] || s.opts.LockSourceRoot == "" {
		return nil
	}
	_, preserved, err := s.devContainerPreserved()
	if err != nil || preserved {
		return err
	}
	if err := devcontainer.CheckSource(ctx, s.opts.LockSourceRoot); err != nil {
		return fmt.Errorf("prepare devcontainer bootstrap: %w", err)
	}
	return nil
}
