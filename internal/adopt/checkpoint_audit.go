// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// currentCheckpointDigests are the digests (util.CanonicalTextDigest) of the checkpoint bundle
// this release ships, one per bundle path. Each is also in priorCheckpointDigests, which holds
// every text a release shipped; this map tells the current one apart, so audit can name a copy
// an earlier release left behind. TestCurrentCheckpointDigests_Boundary_MatchShippedBundle
// fails until a changed script is recorded in both.
var currentCheckpointDigests = map[string]string{
	checkpointScript:   "80a0a2da3dc2222e12b5c70ab7bc442fae586f1d06d5bdcb0516df3c20a144ca",
	checkpointCommon:   "dfe0975c01017ee575fdd57ecd28cb8963e253e4ccc698f83e63f3b1b3375a07",
	checkpointLauncher: "577b85ce8fa4eb90aae8fa84b251ed828315baf5a20f5f249e346b74f49df622",
}

// AuditCheckpointBundle returns one warning line per checkpoint bundle file of the repository
// at rootDir that holds an earlier Praetor text of it, naming the release that shipped it
// (#239). Such a copy can fail where the current one works: before #149 the evaluator called
// os.O_DIRECTORY, which Python on Windows lacks. A plain adopt run refreshes it, beside a kept
// lefthook.yml too, unless another bundle file holds an edited copy: adoption keeps the bundle
// as one unit then, so the line names that file instead of adopt.
//
// A missing file, the current text and an edited copy are not reported: adoption installs
// the first and keeps the other two. A repository whose lefthook.yml extends the canonical
// policy vendors its scripts from one reviewed commit and keeps them on purpose (BUG-858), so
// nothing is reported there either.
func AuditCheckpointBundle(ctx context.Context, rootDir string) ([]string, error) {
	return AuditCheckpointBundleWithSource(ctx, rootDir, "")
}

// AuditCheckpointBundleWithSource compares with the bundle adoption copies from sourceRoot,
// the explicitly selected catalog. Empty sourceRoot retains the running release as reference.
func AuditCheckpointBundleWithSource(ctx context.Context, rootDir, sourceRoot string) ([]string, error) {
	vendored, err := extendsCanonicalLefthook(ctx, rootDir)
	if err != nil || vendored {
		return nil, err
	}
	digests, err := checkpointBundleDigests(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	if len(digests) == 0 {
		return nil, nil
	}
	current, err := checkpointReferenceDigests(ctx, sourceRoot)
	if err != nil {
		return nil, err
	}
	remedy := "run praetorctl adopt (no --force needed) to refresh it."
	if edited := editedCheckpointFile(digests, current); edited != "" {
		remedy = "adoption keeps the bundle while " + edited + " holds an edited copy; restore that file " +
			"or replace the bundle with this release's text, then run praetorctl adopt."
	}
	return checkpointWarningLines(digests, current, sourceRoot, remedy), nil
}

func checkpointWarningLines(digests, current map[string]string, sourceRoot, remedy string) []string {
	var lines []string
	reference := "this release's"
	if sourceRoot != "" {
		reference = "the selected source bundle's"
	}
	for i := 0; i < len(checkpointBundle) && i < maxCheckpointSources; i++ {
		rel := checkpointBundle[i]
		if release, ok := priorCheckpointDigests[rel][digests[rel]]; ok && digests[rel] != current[rel] {
			lines = append(lines, fmt.Sprintf("[WARN] %s holds the checkpoint evaluator Praetor shipped with %s, not %s; %s",
				rel, release, reference, remedy))
		}
	}
	return lines
}

func checkpointReferenceDigests(ctx context.Context, root string) (map[string]string, error) {
	if root == "" {
		return currentCheckpointDigests, nil
	}
	sources, err := readCheckpointSources(ctx, root)
	if err != nil {
		return nil, err
	}
	current := make(map[string]string, len(sources))
	for i := 0; i < len(sources) && i < maxCheckpointSources; i++ {
		digest, _, err := util.CanonicalTextDigest(sources[i].data)
		if err != nil {
			return nil, fmt.Errorf("checkpoint source %s: %w", sources[i].path, err)
		}
		current[sources[i].path] = digest
	}
	return current, nil
}

// checkpointBundleDigests returns the digest (util.CanonicalTextDigest) of every checkpoint
// bundle file of the repository at rootDir that exists and has one; a missing file has no entry.
func checkpointBundleDigests(ctx context.Context, rootDir string) (map[string]string, error) {
	digests := make(map[string]string, len(checkpointBundle))
	for i := 0; i < len(checkpointBundle) && i < maxCheckpointSources; i++ {
		rel := checkpointBundle[i]
		full, err := repoFile(rootDir, rel)
		if err != nil {
			return nil, err
		}
		data, exists, err := contextopt.ObserveSnapshot(ctx, full)
		if err != nil {
			return nil, fmt.Errorf("read checkpoint bundle file %s: %w", rel, err)
		}
		if !exists {
			continue
		}
		if digest, _, err := util.CanonicalTextDigest(data); err == nil {
			digests[rel] = digest
		}
	}
	return digests, nil
}

// editedCheckpointFile returns the first bundle path whose digest is neither this release's nor
// an earlier Praetor text of it, or "" when every existing file is one of those.
func editedCheckpointFile(digests, current map[string]string) string {
	for i := 0; i < len(checkpointBundle) && i < maxCheckpointSources; i++ {
		rel := checkpointBundle[i]
		digest, exists := digests[rel]
		if !exists || digest == current[rel] {
			continue
		}
		if _, prior := priorCheckpointDigests[rel][digest]; !prior {
			return rel
		}
	}
	return ""
}

// extendsCanonicalLefthook reports whether the repository's lefthook.yml extends the canonical
// policy (extendsCanonicalPolicy). A missing or unparsable file does not.
func extendsCanonicalLefthook(ctx context.Context, rootDir string) (bool, error) {
	full, err := repoFile(rootDir, lefthookFile)
	if err != nil {
		return false, err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", lefthookFile, err)
	}
	if !exists {
		return false, nil
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return false, nil //nolint:nilerr // a lefthook.yml that does not parse extends no canonical policy
	}
	return extendsCanonicalPolicy(parsed), nil
}
