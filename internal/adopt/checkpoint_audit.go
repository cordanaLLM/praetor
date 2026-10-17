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
	checkpointScript:   "2c08cd616a4a2d53bcbdc2a4e3bb8a4fc235a718dd0913c53d95f9943b53eb6c",
	checkpointCommon:   "f5e11ef5e868e8799d47d9cf845dbcd5ad7f36f51d2dc34f759993a1dc0c07b2",
	checkpointLauncher: "28f19d46625ce26a3eeafd16d88abbc7fda68f46c677bcb6566cf8dc6c70f8fd",
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
	vendored, err := extendsCanonicalLefthook(ctx, rootDir)
	if err != nil || vendored {
		return nil, err
	}
	digests, err := checkpointBundleDigests(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	remedy := "run praetorctl adopt (no --force needed) to refresh it."
	if edited := editedCheckpointFile(digests); edited != "" {
		remedy = "adoption keeps the bundle while " + edited + " holds an edited copy; restore that file " +
			"or replace the bundle with this release's text, then run praetorctl adopt."
	}
	var lines []string
	for i := 0; i < len(checkpointBundle) && i < maxCheckpointSources; i++ {
		rel := checkpointBundle[i]
		if release, ok := priorCheckpointDigests[rel][digests[rel]]; ok && digests[rel] != currentCheckpointDigests[rel] {
			lines = append(lines, fmt.Sprintf("[WARN] %s holds the checkpoint evaluator Praetor shipped with %s, not this release's; %s",
				rel, release, remedy))
		}
	}
	return lines, nil
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
func editedCheckpointFile(digests map[string]string) string {
	for i := 0; i < len(checkpointBundle) && i < maxCheckpointSources; i++ {
		rel := checkpointBundle[i]
		digest, exists := digests[rel]
		if !exists || digest == currentCheckpointDigests[rel] {
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
