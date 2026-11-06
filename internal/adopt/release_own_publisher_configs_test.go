// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestOwnReleaseConfigs covers the configuration inventory: none, manifest only, manifest with a configuration, and refused reads.
func TestOwnReleaseConfigs(t *testing.T) {
	cases := []struct {
		name          string
		files         map[string]string
		setupSymlink  func(t *testing.T, root string) bool
		wantKeys      []string
		wantNilValues []string
		wantErr       bool
	}{
		{
			name:     "none",
			files:    map[string]string{},
			wantKeys: []string{},
		},
		{
			name: "manifest only",
			files: map[string]string{
				".release-please-manifest.json": `{".":"1.0.0"}`,
			},
			wantKeys:      []string{".release-please-manifest.json"},
			wantNilValues: []string{".release-please-manifest.json"},
		},
		{
			name: "manifest plus config",
			files: map[string]string{
				"release-please-config.json":    `{"skip-github-release":true,"packages":{".":{}}}`,
				".release-please-manifest.json": `{".":"1.0.0"}`,
			},
			wantKeys: []string{"release-please-config.json"},
		},
		{
			name: "github manifest only",
			files: map[string]string{
				".github/.release-please-manifest.json": `{".":"1.0.0"}`,
			},
			wantKeys:      []string{".github/.release-please-manifest.json"},
			wantNilValues: []string{".github/.release-please-manifest.json"},
		},
		{
			name: "symlink refused if the reader refuses it",
			setupSymlink: func(t *testing.T, root string) bool {
				t.Helper()
				target := filepath.Join(root, "actual-config.json")
				mustWrite(t, target, `{"packages":{".":{}}}`)
				link := filepath.Join(root, "release-please-config.json")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlinks not supported on this platform: %v", err)
					return false
				}
				return true
			},
			wantErr: true,
		},
	}

	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			root := supplyChainRepo(t, tc.files)
			if tc.setupSymlink != nil {
				if !tc.setupSymlink(t, root) {
					return
				}
			}
			configs, err := ownReleaseConfigs(t.Context(), root)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ownReleaseConfigs error = %v; wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(configs) != len(tc.wantKeys) {
				t.Fatalf("len(configs) = %d, want %d (%v)", len(configs), len(tc.wantKeys), configs)
			}
			for j := 0; j < len(tc.wantKeys); j++ {
				key := tc.wantKeys[j]
				val, exists := configs[key]
				if !exists {
					t.Errorf("missing expected config key %q", key)
				}
				if slices.Contains(tc.wantNilValues, key) && val != nil {
					t.Errorf("config %q = %q, want nil", key, string(val))
				}
			}
		})
	}
}
