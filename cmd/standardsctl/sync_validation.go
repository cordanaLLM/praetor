package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// syncCompanions is what verifySyncCompanions found: how many companion checks are missing
// or unverified, and whether the lockfile is present but unverifiable against the catalog.
type syncCompanions struct {
	incomplete     int
	lockUnverified bool
}

// verifySyncCompanions reports the companion checks. A verify function returns a non-empty
// reason for a valid file it could not verify.
func verifySyncCompanions(ctx context.Context, root, catalogRoot string, manifest *config.Manifest) (syncCompanions, error) {
	checks := []struct {
		name   string
		label  string
		verify func() (string, error)
	}{
		{".standards.lock", "Lockfile", func() (string, error) {
			return verifySyncLockfile(ctx, root, catalogRoot, manifest)
		}},
		{"AGENTS.md", "Context harness and six compiled projections", func() (string, error) {
			return "", compiler.NewTranspiler().VerifyContext(ctx, filepath.Join(root, "AGENTS.md"), root)
		}},
	}
	var report syncCompanions
	for _, check := range checks {
		_, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(root, check.name))
		if err != nil {
			return report, fmt.Errorf("%s observation failed: %w", check.name, err)
		}
		if !exists {
			fmt.Printf("  [MISSING] %s %s missing; verification incomplete.\n", check.label, check.name)
			report.incomplete++
			continue
		}
		reason, err := check.verify()
		if err != nil {
			return report, fmt.Errorf("%s validation failed: %w", check.name, err)
		}
		if reason != "" {
			fmt.Printf("  [UNVERIFIED] %s %s: %s; verification incomplete.\n", check.label, check.name, reason)
			report.incomplete++
			report.lockUnverified = report.lockUnverified || check.name == ".standards.lock"
			continue
		}
		fmt.Printf("  [OK] %s %s verified\n", check.label, check.name)
	}
	return report, nil
}

// verifySyncLockfile reports a well-formed lock whose content digests cannot be hashed
// as unverified instead of verified.
func verifySyncLockfile(ctx context.Context, root, catalogRoot string, manifest *config.Manifest) (string, error) {
	result, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{Root: root, CatalogRoot: catalogRoot}, manifest)
	if err != nil || result.Verified() {
		return "", err
	}
	return fmt.Sprintf("pins and aggregate digest are valid, but %v; materialize it or pass --catalog-root", config.ErrLockUnverifiable), nil
}
