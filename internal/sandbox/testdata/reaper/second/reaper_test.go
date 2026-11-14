// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package second

import (
	"testing"

	"github.com/testcontainers/testcontainers-go/praetorfixture/reaper"
)

// TestReaper starts this package's reaper against the fixture engine.
func TestReaper(t *testing.T) { reaper.Start(t) }
