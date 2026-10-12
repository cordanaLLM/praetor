// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// declaredLintersGate is the title used on every audit verdict line.
const declaredLintersGate = "Declared linters"

// AuditDeclaredLinters verifies that every linter declared by the profiles or facets in policy
// is run by at least one CI workflow under .github/workflows in rootDir (#1289). Each missing
// linter is reported as one finding naming the linter and where it was declared.
func AuditDeclaredLinters(ctx context.Context, rootDir string, policy *config.EffectivePolicy) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + declaredLintersGate + ": the audit requires a context")
	}
	if policy == nil {
		return "", errors.New("[FAIL] " + declaredLintersGate + ": policy is required")
	}
	declared, err := policy.DeclaredLinters(ctx)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	if len(declared) == 0 {
		return "[PASS] " + declaredLintersGate + ": policy declares no linters; no workflow is required.", nil
	}

	names := make([]string, 0, len(declared))
	for _, l := range declared {
		names = append(names, l.Name)
	}

	present, err := forge.MeasureWorkflowLinters(ctx, rootDir, names)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot inspect workflows: %w", declaredLintersGate, err)
	}

	var missing []config.DeclaredLinter
	for _, l := range declared {
		if !present[l.Name] {
			missing = append(missing, l)
		}
	}

	if len(missing) == 0 {
		return fmt.Sprintf("[PASS] %s: every declared linter is run by a CI workflow (%d verified).", declaredLintersGate, len(declared)), nil
	}

	lines := make([]string, 0, len(missing)+1)
	lines = append(lines, fmt.Sprintf("[FAIL] %s: %d declared linter(s) not run by any workflow:", declaredLintersGate, len(missing)))
	for _, m := range missing {
		lines = append(lines, fmt.Sprintf("  - %s: declared by %s, not run by any workflow", m.Name, m.Source))
	}
	return "", errors.New(strings.Join(lines, "\n"))
}
