// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

func TestHostCappedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats sentinel.HostStats
		ram   uint64
		swap  uint64
	}{
		{"small", sentinel.HostStats{RAMTotalBytes: 8 * gib, SwapMeasured: true, SwapTotalBytes: 4 * gib}, 2 * gib, 2 * gib},
		{"large", sentinel.HostStats{RAMTotalBytes: 128 * gib, SwapMeasured: true, SwapTotalBytes: 96 * gib}, 12 * gib, 15 * gib},
		{"at caps", sentinel.HostStats{RAMTotalBytes: 48 * gib, SwapMeasured: true, SwapTotalBytes: 30 * gib}, 12 * gib, 15 * gib},
		{"no swap", sentinel.HostStats{RAMTotalBytes: 16 * gib, SwapMeasured: true}, 4 * gib, 0},
		{"unmeasured", sentinel.HostStats{}, 12 * gib, 15 * gib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HostCappedPolicy(tc.stats)
			if got.MinAvailableBytes != tc.ram || got.MinSwapFreeBytes != tc.swap || got.MaxLoad1 != DefaultPolicy().MaxLoad1 {
				t.Fatalf("policy %+v; want memory %d swap %d and default load", got, tc.ram, tc.swap)
			}
		})
	}
}
