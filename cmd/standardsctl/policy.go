package main

import (
	"path/filepath"

	"github.com/cordanallm/praetor/internal/config"
	"github.com/cordanallm/praetor/internal/hiss"
)

// scanOptionsFor derives HISS scan bounds from the repository's resolved
// policy so audit, baseline, and adopt all gate on the same function-length
// cap the manifest declares (overrides.complexity.max_func_loc). Without a
// readable manifest the scanner defaults apply.
func scanOptionsFor(repoDir string) hiss.ScanOptions {
	opts := hiss.ScanOptions{}
	manifest, err := config.LoadManifest(filepath.Join(repoDir, ".standards.yaml"))
	if err != nil {
		return opts
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	if policy.Complexity.MaxFuncLOC > 0 {
		opts.MaxFuncLOC = policy.Complexity.MaxFuncLOC
	}
	return opts
}
