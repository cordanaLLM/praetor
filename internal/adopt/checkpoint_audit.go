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
	checkpointScript:   "39138445b6927bde92db48182781070d48fed84daa8417e555bec744015606e9",
	checkpointCommon:   "f5e11ef5e868e8799d47d9cf845dbcd5ad7f36f51d2dc34f759993a1dc0c07b2",
	checkpointLauncher: "28f19d46625ce26a3eeafd16d88abbc7fda68f46c677bcb6566cf8dc6c70f8fd",
}

// AuditCheckpointBundle returns one warning line per checkpoint bundle file of the repository
// at rootDir that holds an earlier Praetor text of it, naming the release that shipped it
// (#239). Such a copy can fail where the current one works: before #149 the evaluator called
// os.O_DIRECTORY, which Python on Windows lacks. A plain adopt run refreshes it.
//
// A missing file, the current text and an edited copy are not reported: adoption installs
// the first and keeps the other two. A repository whose lefthook.yml extends the canonical
// policy vendors its scripts from one reviewed commit and keeps them on purpose (BUG-858), so
// nothing is reported there either.
func AuditCheckpointBundle(ctx context.Context, rootDir string) ([]string, error) {
	vendored, err := extendsCanonicalLefthook(ctx, rootDir)
	if err != nil || vendored {
		return nil, err
	}
	var lines []string
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
		digest, _, err := util.CanonicalTextDigest(data)
		if err != nil || digest == currentCheckpointDigests[rel] {
			continue
		}
		if release, ok := priorCheckpointDigests[rel][digest]; ok {
			lines = append(lines, fmt.Sprintf("[WARN] %s holds the checkpoint evaluator Praetor shipped with %s, not this release's; "+
				"run praetorctl adopt (no --force needed) to refresh it.", rel, release))
		}
	}
	return lines, nil
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
	if yaml.Unmarshal(data, &parsed) != nil {
		return false, nil
	}
	return extendsCanonicalPolicy(parsed), nil
}
