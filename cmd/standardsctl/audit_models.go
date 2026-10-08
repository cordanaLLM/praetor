package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// auditModelCatalog fails a repository whose model routing catalog is stale: an entry marked
// preview, or one whose as_of date is older than the catalog's freshness window
// (docs/standards/model-routing-and-fanout.md). A repository without a catalog skips the check,
// saying so; a catalog that does not load fails it, since a check that did not run is no pass.
func auditModelCatalog(ctx context.Context, rootDir string, now time.Time) error {
	path := filepath.Join(rootDir, filepath.FromSlash(router.DefaultConfigPath))
	cfg, err := router.LoadRoutingConfigContext(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("[SKIP] model catalog freshness not checked: the repository has no %s.\n", router.DefaultConfigPath)
		return nil
	}
	if err != nil {
		return fmt.Errorf("[FAIL] model catalog not checked: %w", err)
	}
	findings := router.CatalogFindings(cfg, now)
	if len(findings) == 0 {
		fmt.Printf("[PASS] model catalog %s has no preview entry and none older than %d days.\n",
			router.DefaultConfigPath, int(router.CatalogMaxAge(cfg)/(24*time.Hour)))
		return nil
	}
	for _, finding := range findings {
		fmt.Printf("  - %s\n", finding)
	}
	return fmt.Errorf("[FAIL] model catalog %s is stale: %d finding(s); run praetorctl models sync, or replace the entries it reports",
		router.DefaultConfigPath, len(findings))
}
