// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

var ownReleaseResolveCases = []struct {
	name       string
	config     string
	wantErrSub string
}{
	{
		name:   "top-level true",
		config: `{"skip-github-release":true,"packages":{".":{}}}`,
	},
	{
		name:       "package false overrides",
		config:     `{"skip-github-release":true,"packages":{".":{},"pkg":{"skip-github-release":false}}}`,
		wantErrSub: `package "pkg" does not resolve skip-github-release to true`,
	},
	{
		name:       "missing key",
		config:     `{"packages":{".":{}}}`,
		wantErrSub: `package "." does not resolve skip-github-release to true`,
	},
	{
		name:       "malformed JSON",
		config:     `{"skip-github-release":`,
		wantErrSub: "read release-please configuration:",
	},
	{
		name:       "empty config data",
		config:     "",
		wantErrSub: "release-please configuration missing; a version manifest cannot declare skip-github-release",
	},
	{
		name:   "package true overrides top-level false",
		config: `{"skip-github-release":false,"packages":{".":{"skip-github-release":true}}}`,
	},
	{
		name:   "legacy root without packages",
		config: `{"skip-github-release":true}`,
	},
	{
		name:       "non-boolean top level",
		config:     `{"skip-github-release":"true","packages":{".":{}}}`,
		wantErrSub: "skip-github-release must be a boolean",
	},
	{
		name:       "non-boolean package level",
		config:     `{"skip-github-release":true,"packages":{".":{"skip-github-release":"yes"}}}`,
		wantErrSub: "skip-github-release must be a boolean",
	},
	{
		name:   "null inherits top level",
		config: `{"skip-github-release":true,"packages":{".":{"skip-github-release":null}}}`,
	},
	{
		name:       "empty packages object",
		config:     `{"skip-github-release":true,"packages":{}}`,
		wantErrSub: "packages must be an object with 1..1024 entries",
	},
	{
		name:       "non-object package entry",
		config:     `{"skip-github-release":true,"packages":{".":123}}`,
		wantErrSub: `package "." must be an object`,
	},
}

// checkOwnReleaseResolve verifies whether releasePackagesSkipGitHub satisfies the expected error substring.
func checkOwnReleaseResolve(t *testing.T, config, wantErrSub string) {
	t.Helper()
	err := releasePackagesSkipGitHub([]byte(config))
	if wantErrSub != "" {
		if err == nil || !strings.Contains(err.Error(), wantErrSub) {
			t.Fatalf("releasePackagesSkipGitHub error = %v; want containing %q", err, wantErrSub)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOwnReleaseResolve covers skip-github-release resolution: top level, package overrides, missing keys and malformed JSON.
func TestOwnReleaseResolve(t *testing.T) {
	for i := 0; i < len(ownReleaseResolveCases); i++ {
		tc := ownReleaseResolveCases[i]
		t.Run(tc.name, func(t *testing.T) {
			checkOwnReleaseResolve(t, tc.config, tc.wantErrSub)
		})
	}
}
