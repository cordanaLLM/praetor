package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Preserve the CLI error identities while the shared validator owns their meaning.
var (
	ErrLockDigestPlaceholder = config.ErrLockDigestPlaceholder
	ErrLockDigestMalformed   = config.ErrLockDigestMalformed
	ErrLockEntryMissing      = config.ErrLockEntryMissing
	ErrLockDigestMismatch    = config.ErrLockDigestMismatch
)

func auditLockDigests(manifest *config.Manifest, rootDir string) error {
	return auditLockDigestsContext(context.Background(), manifest, rootDir)
}

func auditLockDigestsContext(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	result, err := config.ValidateLockfile(ctx, rootDir, manifest)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	fmt.Println("[PASS] SemVer lockfile .standards.lock verified.")
	fmt.Printf("[PASS] Lockfile digests verified (%d profiles, %d facets).\n", result.Profiles, result.Facets)
	return nil
}
