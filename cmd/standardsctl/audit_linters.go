// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// auditDeclaredLinters verifies that every linter declared by the resolved policy's
// profiles or facets is run by at least one CI workflow under .github/workflows (#1289).
func auditDeclaredLinters(ctx context.Context, manifest *config.Manifest, rootDir string, effective *config.EffectivePolicy) error {
	var exceptions []config.Exception
	if manifest != nil {
		exceptions = manifest.Exceptions
	}
	line, err := adopt.AuditDeclaredLinters(ctx, adopt.DeclaredLintersOptions{
		RootDir:    rootDir,
		Policy:     effective,
		Exceptions: exceptions,
		Today:      time.Now(),
	})
	if line != "" {
		fmt.Println(line)
	}
	return err
}
