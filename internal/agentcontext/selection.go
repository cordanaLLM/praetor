// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TrackedExcluded returns tracked context files outside clients, in registry order.
// Order: engine-fr-rule-checks-20261027; an untracked local copy is outside the gate.
func TrackedExcluded(ctx context.Context, root string, clients []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	excluded, err := excludedVendorPaths(clients)
	if err != nil {
		return nil, err
	}
	if len(excluded) == 0 {
		return nil, ctx.Err()
	}
	args := append([]string{"ls-files", "--cached", "-z", "--"}, excluded...)
	result, err := util.RunGitProbe(ctx, root, 64*1024, args...)
	if err != nil {
		return nil, fmt.Errorf("read tracked agent context: %w", err)
	}
	tracked := map[string]bool{}
	names := strings.Split(string(result.Stdout), "\x00")
	if len(names) > len(vendorTargets)+1 {
		return nil, fmt.Errorf("tracked context listing exceeds %d files", len(vendorTargets))
	}
	for i := 0; i < len(names) && i <= len(vendorTargets); i++ {
		tracked[names[i]] = true
	}
	var found []string
	for i := 0; i < len(excluded) && i < len(vendorTargets); i++ {
		if tracked[excluded[i]] {
			found = append(found, excluded[i])
		}
	}
	return found, nil
}

func excludedVendorPaths(clients []string) ([]string, error) {
	selected, err := VendorTargets(clients)
	if err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(selected))
	for i := 0; i < len(selected) && i < len(vendorTargets); i++ {
		keep[selected[i].Path] = true
	}
	var excluded []string
	for i := 0; i < len(vendorTargets); i++ {
		if !keep[vendorTargets[i].path] {
			excluded = append(excluded, vendorTargets[i].path)
		}
	}
	return excluded, nil
}
