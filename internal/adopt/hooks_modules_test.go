// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const nestedGoModuleWarningPrefix = "go.mod found in "

// adoptTrackedModules runs adoption over a fixture whose supplied modules git tracks.
func adoptTrackedModules(t *testing.T, modules ...string) *AdoptReport {
	t.Helper()
	root := newTestRepo(t, "nested-go-modules")
	for index := 0; index < len(modules) && index <= maxNestedGoModuleWarnings; index++ {
		trackGoModule(t, root, modules[index])
	}
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// nestedGoModuleWarnings keeps only the nested-module lines from an adoption report.
func nestedGoModuleWarnings(report *AdoptReport) []string {
	return slices.DeleteFunc(slices.Clone(report.Warnings), func(warning string) bool {
		return !strings.HasPrefix(warning, nestedGoModuleWarningPrefix)
	})
}

func TestAdoptGitHooksWarnsNestedGoModules(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		modules    []string
	}{
		{"only nested", "tooling", []string{"tooling/go.mod"}},
		{"root and nested", "examples/a", []string{"go.mod", "examples/a/go.mod"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings := nestedGoModuleWarnings(adoptTrackedModules(t, tc.modules...))
			want := []string{nestedGoModuleWarning(tc.want)}
			if !slices.Equal(warnings, want) {
				t.Fatalf("nested module warnings = %q, want %q", warnings, want)
			}
		})
	}
}

func TestAdoptGitHooksSkipsRootAndIgnoredGoModules(t *testing.T) {
	report := adoptTrackedModules(t, "go.mod", "testdata/fixture/go.mod", "vendor/widget/go.mod")
	if warnings := nestedGoModuleWarnings(report); len(warnings) != 0 {
		t.Fatalf("nested module warnings = %q, want none", warnings)
	}
}

func TestAdoptGitHooksBoundsNestedGoModuleWarnings(t *testing.T) {
	modules := make([]string, 0, maxNestedGoModuleWarnings+1)
	for index := 0; index <= maxNestedGoModuleWarnings; index++ {
		modules = append(modules, filepath.ToSlash(filepath.Join("modules", fmt.Sprintf("%02d", index), "go.mod")))
	}
	report := adoptTrackedModules(t, modules...)
	warnings := nestedGoModuleWarnings(report)
	if len(warnings) != maxNestedGoModuleWarnings {
		t.Fatalf("nested module warnings = %d, want %d", len(warnings), maxNestedGoModuleWarnings)
	}
	for index := 0; index < len(warnings); index++ {
		want := nestedGoModuleWarning(fmt.Sprintf("modules/%02d", index))
		if warnings[index] != want {
			t.Fatalf("warning %d = %q, want %q", index, warnings[index], want)
		}
	}
	if !slices.Contains(report.Warnings, "1 more modules outside the root") {
		t.Fatalf("report lacks the bounded warning: %q", report.Warnings)
	}
}

func TestWarnNestedGoModulesReportsDiscoveryError(t *testing.T) {
	report := &AdoptReport{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	warnNestedGoModules(ctx, &adoptSession{repoPath: t.TempDir(), report: report})
	if !slices.ContainsFunc(report.Warnings, func(warning string) bool {
		return strings.Contains(warning, "context canceled")
	}) {
		t.Fatalf("discovery warning = %q", report.Warnings)
	}
}

func nestedGoModuleWarning(dir string) string {
	return "go.mod found in " + dir + ", not at the root: the Go hooks (go vet, govulncheck) and the gate's toolchain stages skip it; its declared verification command is what checks it (#1353)"
}
