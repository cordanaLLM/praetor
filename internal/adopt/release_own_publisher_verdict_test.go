// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

// TestOwnReleaseVerdict checks that a pass names every publisher and a failure names the configuration to fix.
func TestOwnReleaseVerdict(t *testing.T) {
	cases := []struct {
		name         string
		configs      map[string][]byte
		publishers   []string
		wantPassSubs []string
		wantFailSubs []string
	}{
		{
			name: "pass line names every publisher",
			configs: map[string][]byte{
				"release-please-config.json": []byte(`{"skip-github-release":true,"packages":{".":{}}}`),
			},
			publishers: []string{
				".github/workflows/publish.yml",
				".github/workflows/dist.yml",
			},
			wantPassSubs: []string{
				"[PASS] release-own-publisher:",
				"release-please-config.json: every package resolves skip-github-release to true",
				"publishing workflow(s): .github/workflows/publish.yml, .github/workflows/dist.yml.",
			},
		},
		{
			name: "multiple configs pass naming all publishers",
			configs: map[string][]byte{
				"release-please-config.json":         []byte(`{"skip-github-release":true,"packages":{".":{}}}`),
				".github/release-please-config.json": []byte(`{"skip-github-release":true,"packages":{".":{}}}`),
			},
			publishers: []string{
				".github/workflows/release.yml",
			},
			wantPassSubs: []string{
				"release-please-config.json:",
				".github/release-please-config.json:",
				"publishing workflow(s): .github/workflows/release.yml.",
			},
		},
		{
			name: "fail line names the fix path for root manifest",
			configs: map[string][]byte{
				".release-please-manifest.json": nil,
			},
			publishers: []string{
				".github/workflows/publish.yml",
			},
			wantFailSubs: []string{
				"[FAIL] release-own-publisher:",
				"publishing workflow(s): .github/workflows/publish.yml",
				"set \"skip-github-release\": true at the top level or on every package in release-please-config.json",
			},
		},
		{
			name: "fail line names the fix path for github manifest",
			configs: map[string][]byte{
				".github/.release-please-manifest.json": nil,
			},
			publishers: []string{
				".github/workflows/publish.yml",
			},
			wantFailSubs: []string{
				"[FAIL] release-own-publisher:",
				"in .github/release-please-config.json",
			},
		},
		{
			name: "fail line names fix path for config without skip",
			configs: map[string][]byte{
				"release-please-config.json": []byte(`{"packages":{".":{}}}`),
			},
			publishers: []string{
				".github/workflows/deploy.yml",
			},
			wantFailSubs: []string{
				"[FAIL] release-own-publisher:",
				"publishing workflow(s): .github/workflows/deploy.yml",
				"in release-please-config.json",
			},
		},
	}

	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			out, err := judgeOwnReleaseConfigs(tc.configs, tc.publishers)
			if len(tc.wantFailSubs) > 0 {
				if err == nil {
					t.Fatalf("judgeOwnReleaseConfigs passed; want error with %v", tc.wantFailSubs)
				}
				for j := 0; j < len(tc.wantFailSubs); j++ {
					sub := tc.wantFailSubs[j]
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error missing %q: %v", sub, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for j := 0; j < len(tc.wantPassSubs); j++ {
				sub := tc.wantPassSubs[j]
				if !strings.Contains(out, sub) {
					t.Errorf("output missing %q: %s", sub, out)
				}
			}
		})
	}
}
