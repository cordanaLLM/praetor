// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const maxReleaseWorkflowFiles = 64

var releasePleaseConfigPaths = [...]string{
	"release-please-config.json",
	".release-please-manifest.json",
	".github/release-please-config.json",
	".github/.release-please-manifest.json",
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
	if err != nil {
		s.warnReleasePolicy(fmt.Sprintf("no release.e2e section recorded: %v", err))
		return nil
	}
	if !hasRelease {
		return nil
	}
	since, err := util.ResolveGitCommit(ctx, s.repoPath, "HEAD")
	if err != nil {
		s.warnReleasePolicy(fmt.Sprintf("no release.e2e section recorded: %v", err))
		return nil
	}
	if since == "" {
		s.warnReleasePolicy("no release.e2e section recorded: resolve commit HEAD: repository has no commits")
		return nil
	}
	return &config.ReleasePolicy{
		E2E: &config.ReleaseE2EPolicy{
			Since: since,
		},
	}
}

func (s *adoptSession) warnReleasePolicy(msg string) {
	if s == nil || s.report == nil {
		return
	}
	if !slices.Contains(s.report.Warnings, msg) {
		s.report.addWarning("%s", msg)
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
		if workflowEntryTriggersTag(ctx, wfDir, entries[i]) {
			return true, nil
		}
	}
	return false, nil
}

func workflowEntryTriggersTag(ctx context.Context, wfDir string, entry os.DirEntry) bool {
	name := entry.Name()
	if entry.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
		return false
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(wfDir, name))
	if err != nil || !exists {
		return false
	}
	return workflowTriggersOnVersionTag(string(data))
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
