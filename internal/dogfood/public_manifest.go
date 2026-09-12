package dogfood

import (
	"context"
	"errors"
	"os"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// publicManifest is also used to validate the explicitly selected source bundle
// before cloning. Applied checkout verification uses the effective-policy loader.
func publicManifest(ctx context.Context, dir string) (result *config.Manifest, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err := readPublicFile(root, ".standards.yaml", 1<<20)
	if err != nil {
		return nil, err
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}
