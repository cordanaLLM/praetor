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
