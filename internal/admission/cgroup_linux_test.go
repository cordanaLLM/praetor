// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cgroupFixture creates synthetic unified-cgroup membership and a three-level all-max memory.max tree.
// The test framework removes both temporary roots; callers may alter them without reading host
// cgroups.
func cgroupFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"", "parent", "parent/leaf"} {
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- private fixture needs owner search bit
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "memory.max"), []byte("max\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proc := filepath.Join(t.TempDir(), "cgroup")
	if err := os.WriteFile(proc, []byte("0::/parent/leaf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return proc, root
}

// TestAdmitCgroup_AncestorNoneAndUnreadable pins ancestor-limit reporting and the shared no-bound
// result for absent, unreadable, malformed, or escaping membership data. A planted numeric ancestor
// must be found, while a removed leaf file must stop traversal.
func TestAdmitCgroup_AncestorNoneAndUnreadable(t *testing.T) {
	for _, kind := range []string{"ancestor", "none", "unreadable", "malformed", "escape"} {
		t.Run(kind, func(t *testing.T) {
			ctx := admitContext(t)
			proc, root := cgroupFixture(t)
			plantCgroupCase(t, kind, proc, root)
			got := cgroupMemoryBound(ctx, proc, root)
			want := noMemoryBound
			if kind == "ancestor" {
				want = "memory.max=1048576 at " + filepath.Join(root, "memory.max")
			}
			if got != want {
				t.Fatalf("%s: got %q, want %q", kind, got, want)
			}
		})
	}
}

// TestAdmitCgroup_WalkBoundAndCompensation pins the 32-level walk cap, cancellation result, and
// concurrency text in unscoped diagnostics. A numeric limit planted beyond the walk cap must remain
// undiscovered.
func TestAdmitCgroup_WalkBoundAndCompensation(t *testing.T) {
	ctx := admitContext(t)
	proc, root := cgroupFixture(t)
	deep := strings.Repeat("a/", 33) + "leaf"
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- private fixture needs owner search bit
	if err := os.MkdirAll(filepath.Join(root, deep), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, deep)
	for level := 0; level < 35; level++ {
		if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte("max"), 0o600); err != nil {
			t.Fatal(err)
		}
		if dir == root {
			break
		}
		dir = filepath.Dir(dir)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proc, []byte("0::/"+deep), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cgroupMemoryBound(ctx, proc, root); got != noMemoryBound {
		t.Fatal(got)
	}
	text := UnscopedBounds(ctx, "test", "slot 2 of 3")
	if !strings.Contains(text, "concurrency: slot 2 of 3") {
		t.Fatal(text)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got := cgroupMemoryBound(canceled, proc, root); got != noMemoryBound {
		t.Fatal(got)
	}
}

// plantCgroupCase mutates the synthetic membership or memory.max files to select an ancestor limit,
// unreadable leaf, malformed membership, or escaping path. The caller supplies test-owned paths and
// relies on their temporary-directory cleanup.
func plantCgroupCase(t *testing.T, kind, proc, root string) {
	t.Helper()
	switch kind {
	case "ancestor":
		if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("1048576\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "unreadable":
		if err := os.Remove(filepath.Join(root, "parent/leaf/memory.max")); err != nil {
			t.Fatal(err)
		}
	case "malformed":
		if err := os.WriteFile(proc, []byte("not a cgroup"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "escape":
		if err := os.WriteFile(proc, []byte("0::/../../escape\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
