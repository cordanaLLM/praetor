// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// nodeVersionPreserved is the report line of a version file adoption found in place.
const nodeVersionPreserved = "Existing Node version file preserved; the repository owns the value"

// reconcileNodeVersionFile seeds markdownassets.NodeVersionFile, the file the documentation workflow
// takes its Node release from, once. The seed is the release the gate's DevContainer CLI runs on,
// a pin the engine's Renovate already moves. A file already there keeps its bytes, --force
// included. It stays out of scaffoldFile, which would warn that a value differing from the seed
// has drifted: the repository owns the value, and audit checks it.
func reconcileNodeVersionFile(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, markdownassets.NodeVersionFile)
	if err != nil {
		return err
	}
	_, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	if exists {
		s.report.recordReconciled(markdownassets.NodeVersionFile, nodeVersionPreserved)
		return nil
	}
	pins, err := devcontainer.LoadCLIPins()
	if err != nil {
		return fmt.Errorf("seed %s: %w", markdownassets.NodeVersionFile, err)
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:      markdownassets.NodeVersionFile,
		perm:     filePerm,
		content:  []byte(pins.Node.Version + "\n"),
		created:  "Seeded the Node release of the documentation gate; the repository owns the value",
		verified: nodeVersionPreserved,
	})
	return err
}

// AuditNodeVersionFile returns the release the repository's documentation workflow runs on, read
// from markdownassets.NodeVersionFile, and fails while the file is missing or names no release the
// gate can run. It returns "" without a failure when the workflow copy reads no version file: only
// the text of an earlier engine the repository is locked to gets past the byte lock that way, and
// that text names its release itself.
func AuditNodeVersionFile(ctx context.Context, repoPath string) (string, error) {
	workflow, _, err := contextopt.ObserveSnapshotIn(ctx, repoPath, DocumentationWorkflowFile)
	if err != nil {
		return "", fmt.Errorf("[FAIL] cannot read %s: %w", DocumentationWorkflowFile, err)
	}
	if !markdownassets.ReadsNodeVersionFile(workflow) {
		return "", nil
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, markdownassets.NodeVersionFile)
	if err != nil {
		return "", fmt.Errorf("[FAIL] cannot read the documentation gate's Node version file %s: %w", markdownassets.NodeVersionFile, err)
	}
	if !exists {
		return "", fmt.Errorf("[FAIL] The documentation gate's Node version file %s is missing; run 'praetorctl adopt'", markdownassets.NodeVersionFile)
	}
	version, err := markdownassets.CheckNodeVersion(data)
	if err != nil {
		return "", fmt.Errorf("[FAIL] The documentation gate's Node version file %s: %w; set a release the gate can run", markdownassets.NodeVersionFile, err)
	}
	return version, nil
}
