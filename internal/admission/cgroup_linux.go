// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const noMemoryBound = "no memory bound applies to this step"

// memoryBound reads /proc/self/cgroup and the corresponding /sys/fs/cgroup memory.max ancestry for an
// unscoped diagnostic. Missing, unreadable, or unbounded data produces the same no-memory-bound
// message.
func memoryBound(ctx context.Context) string {
	return cgroupMemoryBound(ctx, "/proc/self/cgroup", "/sys/fs/cgroup")
}

// cgroupMemoryBound reads a bounded procPath and accepts only a local unified-cgroup path beneath root
// before inspecting memory.max. Cancellation, unreadable or malformed membership, and absent unified
// membership all report no memory bound.
func cgroupMemoryBound(ctx context.Context, procPath, root string) string {
	if err := ctx.Err(); err != nil {
		return noMemoryBound
	}
	data, err := util.ReadFileLimited(procPath, 65536)
	if err != nil {
		return noMemoryBound
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 64 {
		return noMemoryBound
	}
	for _, line := range lines {
		path, ok := strings.CutPrefix(line, "0::/")
		if !ok {
			continue
		}
		if path == "" {
			path = "."
		}
		if !filepath.IsLocal(path) {
			return noMemoryBound
		}
		return walkMemoryBound(ctx, root, filepath.Join(root, path))
	}
	return noMemoryBound
}

// walkMemoryBound reads memory.max at dir and at most 31 ancestors, stopping at the first numeric
// value. All-max ancestry, unreadable or malformed files, cancellation, and traversal exhaustion all
// report no memory bound.
func walkMemoryBound(ctx context.Context, root, dir string) string {
	for level := 0; level < 32; level++ {
		if err := ctx.Err(); err != nil {
			return noMemoryBound
		}
		path := filepath.Join(dir, "memory.max")
		data, err := util.ReadFileLimited(path, 64)
		if err != nil {
			return noMemoryBound
		}
		value := strings.TrimSpace(string(data))
		if value != "max" {
			limit, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return noMemoryBound
			}
			return fmt.Sprintf("memory.max=%d at %s", limit, path)
		}
		if dir == root {
			return noMemoryBound
		}
		dir = filepath.Dir(dir)
	}
	return noMemoryBound
}
