// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"

	"github.com/cordanaLLM/praetor/internal/router"
)

// publishReplayOutcomes appends every replay row as one validated batch: an invalid row leaves the
// existing outcome log untouched, and the log keeps its own record bound, not the 1 MiB snapshot one.
func publishReplayOutcomes(ctx context.Context, path string, rows []router.Outcome) error {
	ctx, cancel := context.WithTimeout(ctx, DefaultArmTimeout)
	defer cancel()
	return router.AppendOutcomes(ctx, path, rows)
}
