// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeCheckedFixture writes one repository file below root.
func writeCheckedFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

const registerTaskRouting = `version: 1
tiers:
  only:
    description: "single tier"
    target_tasks: [deploy_prod]
    models:
      - {id: "m", family: "local", rpm_limit: 1, tpm_limit: 1, cost_per_m_in: 0, cost_per_m_out: 0}
    fallback_tier: ""
governance:
  max_concurrent_same_model: 1
  exhaustion_threshold_percent: 90
  orthogonal_audit_required: false
`

func TestLoadRegisterTaskAuthorityPositive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	authority, labels, err := LoadRegisterTaskAuthority(ctx, root)
	if err != nil || !slices.Contains(labels, "feature_implementation") {
		t.Fatalf("defaults: labels=%v err=%v", labels, err)
	}
	resolution, err := authority.Resolve(SurfaceAgent, "feature_implementation")
	if err != nil || resolution.ManifestSHA256 != authority.ManifestSHA256() || resolution.ManifestSHA256 == "" {
		t.Fatalf("resolution must carry the snapshot digest: %+v, %v", resolution, err)
	}

	writeCheckedFixture(t, root, ".config/models/routing.yaml", registerTaskRouting)
	writeCheckedFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: social\n")
	authority, labels, err = LoadRegisterTaskAuthority(ctx, root)
	if err != nil || !slices.Equal(labels, []string{"deploy_prod"}) {
		t.Fatalf("declared vocabulary: labels=%v err=%v", labels, err)
	}
	if got, err := authority.Resolve(SurfaceAgent, "deploy_prod"); err != nil || got.Register != TextRegisterSocial {
		t.Fatalf("task row resolution = %+v, %v", got, err)
	}
}

func TestLoadRegisterTaskAuthorityNegative(t *testing.T) {
	ctx := context.Background()
	var nilContext context.Context
	if _, _, err := LoadRegisterTaskAuthority(nilContext, t.TempDir()); err == nil {
		t.Fatal("a nil context must be an error")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := LoadRegisterTaskAuthority(canceled, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	root := t.TempDir()
	writeCheckedFixture(t, root, ".config/models/routing.yaml", registerTaskRouting)
	writeCheckedFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n")
	if _, _, err := LoadRegisterTaskAuthority(ctx, root); err == nil || !strings.Contains(err.Error(), `"ci_debugging"`) {
		t.Fatalf("manifest row outside the vocabulary: %v", err)
	}
}

// Without declared manifest rows the plain loader never reads routing, while the task
// loader must, because its caller checks a label against the vocabulary it returns.
func TestLoadRegisterTaskAuthorityBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeCheckedFixture(t, root, ".config/models/routing.yaml", "tiers: [\n")
	if _, err := LoadCheckedRegisterAuthority(ctx, root); err != nil {
		t.Fatalf("plain loader read routing without declared rows: %v", err)
	}
	if _, labels, err := LoadRegisterTaskAuthority(ctx, root); err == nil || labels != nil {
		t.Fatalf("task loader accepted broken routing: labels=%v err=%v", labels, err)
	}
}
