// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
)

// AuditEditorSelection refuses tracked projections of unselected agent clients.
// Order: engine-fr-rule-checks-20261027. The absent selection keeps every client.
func AuditEditorSelection(ctx context.Context, manifest *config.Manifest, root string) (string, error) {
	files, err := agentcontext.TrackedExcluded(ctx, root, manifest.AgentClients)
	if err != nil {
		return "", fmt.Errorf("[FAIL] editor-selection: %w", err)
	}
	if len(files) > 0 {
		return "", fmt.Errorf("[FAIL] editor-selection: tracked unselected agent-context files: %s; delete the files, or add their editor to agent_clients in %s", strings.Join(files, ", "), manifestFile)
	}
	return "[PASS] editor-selection: no tracked agent-context files belong to unselected editors.", nil
}

func reportExcludedContext(ctx context.Context, s *adoptSession, clients []string, paths []string) error {
	tracked, err := agentcontext.TrackedExcluded(ctx, s.repoPath, clients)
	if err != nil {
		return err
	}
	for i := 0; i < len(paths) && i < maxTranspileTargets; i++ {
		if slices.Contains(tracked, paths[i]) {
			detail := "Action required: delete tracked " + paths[i] + ", or add its editor to agent_clients in " + manifestFile + "; file left for review"
			s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{Path: paths[i], Action: actionRemove, Details: detail})
			s.report.Warnings = append(s.report.Warnings, paths[i]+": "+detail)
			continue
		}
		s.report.recordNotApplicable(paths[i], "Not selected by agent_clients in "+manifestFile)
	}
	return nil
}
