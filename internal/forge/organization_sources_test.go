// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestOrganizationSourcesInvalidManifest pins untrusted source exclusion when the repository manifest is invalid.
func TestOrganizationSourcesInvalidManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	data := "version: 1\nforge:\n  own_organizations: [untrusted]\nexceptions:\n  - rule: HISS-11\n    path: .github/workflows/release.yml\n    reason: migration\n    expires: " + testToday.AddDate(1, 0, 0).Format(time.DateOnly) + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	getenv := func(key string) string {
		if key == "GITHUB_REPOSITORY" {
			return "owner/project"
		}
		return ""
	}
	resolved, err := ResolveOwnOrganizationSources(ctx, "", path, []string{"operator"}, getenv)
	if err != nil || resolved.ManifestProblem == nil {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if !slices.Equal(resolved.Organizations, []string{"operator", "owner"}) || !slices.Equal(resolved.Sources, []string{"operator layer", "GITHUB_REPOSITORY"}) {
		t.Fatalf("untrusted or missing source: %+v", resolved)
	}
	if _, err := ResolveOwnOrganizations(ctx, "", path, nil, getenv); err == nil {
		t.Fatal("strict API accepted invalid manifest")
	}
}

// TestOrganizationSourcesMissingAndInvalidIdentity pins behavior when the manifest is missing or the identity string is invalid.
func TestOrganizationSourcesMissingAndInvalidIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	resolved, err := ResolveOwnOrganizationSources(ctx, "", path, nil, nil)
	if err != nil || resolved.ManifestProblem == nil || len(resolved.Organizations) != 0 {
		t.Fatalf("missing manifest: %+v %v", resolved, err)
	}
	if _, err := ResolveOwnOrganizations(ctx, "", path, nil, nil); err != nil {
		t.Fatalf("strict API changed missing-file contract: %v", err)
	}
	if _, err := ResolveOwnOrganizationSources(ctx, "", path, nil, func(string) string { return "bad identity" }); err == nil {
		t.Fatal("invalid identity accepted")
	}
}
