// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/release"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
	"go.yaml.in/yaml/v3"
)

// AuditReleaseE2E runs the release end-to-end evidence gate over root for manifest
// (order engine-release-feature-e2e-gate-20261027).
func AuditReleaseE2E(ctx context.Context, root string, manifest *config.Manifest) (string, error) {
	var policy *config.ReleaseE2EPolicy
	if manifest != nil && manifest.Release != nil {
		policy = manifest.Release.E2E
	}
	return release.AuditE2E(ctx, root, policy, release.IsReleaseContext(false))
}

const maxReleaseWorkflowFiles = 64

var releasePleaseConfigPaths = [...]string{
	"release-please-config.json",
	".release-please-manifest.json",
	".github/release-please-config.json",
	".github/.release-please-manifest.json",
}

// priorHostedGateReleasePRDigests records earlier hosted-gate renderings prior to
// PRAETOR_RELEASE_PR context propagation (order engine-release-feature-e2e-gate-20261027).
var priorHostedGateReleasePRDigests = map[string]string{
	"e43a041f8665588a41e872f0522fa574a98fc982b1e93310dc7882f9d9c08862": "hosted gate without PRAETOR_RELEASE_PR env block",
}

// PriorHostedGateReleasePRDigests returns a copy of the prior hosted-gate release PR digest ledger.
func PriorHostedGateReleasePRDigests() map[string]string {
	return maps.Clone(priorHostedGateReleasePRDigests)
}

// RenderHostedGateReleasePREnv injects the PRAETOR_RELEASE_PR environment block into a hosted
// gate workflow after its concurrency block when not already present
// (order engine-release-feature-e2e-gate-20261027).
func RenderHostedGateReleasePREnv(workflow string) string {
	if strings.Contains(workflow, "PRAETOR_RELEASE_PR:") {
		return workflow
	}
	target := ghworkflow.HostedGateConcurrency + "\n"
	if strings.Contains(workflow, target) {
		return strings.Replace(workflow, target, target+templates.HostedGateReleasePREnv+"\n", 1)
	}
	return templates.HostedGateReleasePREnv + "\n" + workflow
}

// releasePolicy returns the release.e2e section adoption writes for a repository that has a
// release workflow (release-please config or tag release workflow), baselining Since to HEAD at
// adoption time; repositories without a release workflow receive nil
// (order engine-release-feature-e2e-gate-20261027).
func (s *adoptSession) releasePolicy(ctx context.Context) *config.ReleasePolicy {
	if s == nil || s.repoPath == "" {
		return nil
	}
	hasRelease, err := HasReleaseWorkflow(ctx, s.repoPath)
	if err != nil || !hasRelease {
		return nil
	}
	since, err := util.ResolveGitCommit(ctx, s.repoPath, "HEAD")
	if err != nil {
		return &config.ReleasePolicy{E2E: &config.ReleaseE2EPolicy{}}
	}
	return &config.ReleasePolicy{
		E2E: &config.ReleaseE2EPolicy{
			Since: since,
		},
	}
}

// HasReleaseWorkflow reports whether repoPath declares a release-please configuration or a
// tag/release workflow under .github/workflows (order engine-release-feature-e2e-gate-20261027).
func HasReleaseWorkflow(ctx context.Context, repoPath string) (bool, error) {
	if hasReleasePleaseConfig(repoPath) {
		return true, nil
	}
	_, steps, err := forge.MeasureReleaseTokens(ctx, repoPath)
	if err == nil && steps > 0 {
		return true, nil
	}
	return hasTagReleaseWorkflow(ctx, repoPath)
}

func hasReleasePleaseConfig(repoPath string) bool {
	for i := 0; i < len(releasePleaseConfigPaths); i++ {
		if fileExists(filepath.Join(repoPath, filepath.FromSlash(releasePleaseConfigPaths[i]))) {
			return true
		}
	}
	return false
}

func hasTagReleaseWorkflow(ctx context.Context, repoPath string) (bool, error) {
	wfDir := filepath.Join(repoPath, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for i := 0; i < len(entries) && i < maxReleaseWorkflowFiles; i++ {
		name := entries[i].Name()
		if entries[i].IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		data, exists, readErr := contextopt.ObserveSnapshot(ctx, filepath.Join(wfDir, name))
		if readErr != nil || !exists {
			continue
		}
		if workflowTriggersOnVersionTag(string(data)) {
			return true, nil
		}
	}
	return false, nil
}

func workflowTriggersOnVersionTag(content string) bool {
	if !strings.Contains(content, "tags:") {
		return false
	}
	return strings.Contains(content, "v*") || strings.Contains(content, "v[0-9]*")
}

func (s *adoptSession) reconcileManifestReleaseSection(
	ctx context.Context,
	manifest *config.Manifest,
	data []byte,
) ([]byte, bool, error) {
	if manifest == nil || manifest.Release != nil {
		return data, false, nil
	}
	policy := s.releasePolicy(ctx)
	if policy == nil {
		return data, false, nil
	}
	return setManifestRelease(ctx, data, policy)
}

func setManifestRelease(ctx context.Context, data []byte, release *config.ReleasePolicy) ([]byte, bool, error) {
	if release == nil {
		return data, false, nil
	}
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, false, err
	}
	root := document.Content[0]
	if _, found, err := adoptYAMLMappingValue(root, "release"); err != nil || found {
		return data, false, err
	}
	var relNode yaml.Node
	if err := relNode.Encode(release); err != nil {
		return nil, false, fmt.Errorf("encode release policy: %w", err)
	}
	valNode := &relNode
	if relNode.Kind == yaml.DocumentNode && len(relNode.Content) == 1 {
		valNode = relNode.Content[0]
	}
	appendAdoptYAMLMapping(root, "release", valNode)
	out, err := encodeAdoptManifest(document)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
