// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"fmt"
	"io"

	"github.com/cordanaLLM/praetor/internal/govuln"
)

// retryGoCacheMiss keeps retries visible in the stage log, using the shared policy from
// order engine-gate-private-gocache-20261025. Wrap after selecting the host or container runner:
// the policy must see the Go executable before a container translates it into runtime arguments.
func retryGoCacheMiss(run commandRunner, log io.Writer) commandRunner {
	return commandRunner(govuln.RetryCacheMiss(govuln.Runner(run), func(dir string) error {
		if _, err := fmt.Fprintf(log, "gate: note: %s: %s\n", govuln.CacheMissNote, dir); err != nil {
			return fmt.Errorf("write Go cache retry note: %w", err)
		}
		return nil
	}))
}
