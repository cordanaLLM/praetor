// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// auditDeclaredLinters verifies that every linter declared by the resolved policy's
// profiles or facets is run by at least one CI workflow under .github/workflows (#1289).
func auditDeclaredLinters(ctx context.Context, rootDir string, effective *config.EffectivePolicy) error {
	line, err := adopt.AuditDeclaredLinters(ctx, rootDir, effective)
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
