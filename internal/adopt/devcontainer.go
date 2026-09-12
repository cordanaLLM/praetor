package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"gopkg.in/yaml.v3"
)

// Use the same preserved or planned manifest as the policy resolver and audit.
// Inferred repository identity and runtime markers must not override that input.
func prepareAdoptDevContainer(ctx context.Context, s *adoptSession) (*devcontainer.Bundle, error) {
	data, err := plannedManifestBytes(ctx, s)
	if err != nil {
		return nil, err
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	baseline, err := devcontainer.Synthesize(&manifest)
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
	return devcontainer.PrepareBundle(ctx, baseline.Name, manifest.Profiles, manifest.Facets, devcontainer.BootstrapOptions{SourceRoot: s.opts.LockSourceRoot, Features: features})
}
