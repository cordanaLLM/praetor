// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	commitPolicyScriptName = "praetor-commit-policy.sh"
	commitPolicyScriptFile = ".lefthook/pre-push/" + commitPolicyScriptName
)

// buildCommitPolicyScript runs independently of Lefthook's changed-file estimate. Command
// jobs can be skipped when the final diff is empty, even with invalid commits in the range.
func buildCommitPolicyScript() string {
	return "#!/bin/sh\n# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>\n" +
		"# SPDX-License-Identifier: EUPL-1.2\n" +
		"set -eu\n" +
		"exec " + lefthookGovernedCommand("forge check-commits --pre-push") + "\n"
}

// reconcileCommitPolicyScript uses the same scaffold ownership rules as the engine launcher.
// A custom script is preserved and fails adoption rather than earning an unbacked stage.
func reconcileCommitPolicyScript(ctx context.Context, s *adoptSession) error {
	content := buildCommitPolicyScript()
	digest, _, err := util.CanonicalTextDigest([]byte(content))
	if err != nil {
		return fmt.Errorf("fingerprint commit policy script: %w", err)
	}
	state, err := s.scaffoldFile(ctx, scaffold{
		rel: commitPolicyScriptFile, perm: filePerm, content: []byte(content),
		created:  "Installed pre-push commit policy script; checks commits even with an empty file diff",
		verified: "Existing pre-push commit policy script verified present",
		prior:    map[string]string{digest: "pre-push commit policy"},
	})
	if err != nil {
		return err
	}
	if state != scaffoldWritten && state != scaffoldIdentical {
		return fmt.Errorf("cannot enforce pre-push commit policy: custom %s preserved", commitPolicyScriptFile)
	}
	return nil
}
