package adopt

import (
	"context"
	"fmt"
)

func reconcileAsDecided(ctx context.Context, s *adoptSession) error {
	if s.opts.DecisionMCPReconcile == nil {
		s.report.recordNotApplicable("asdecided", "This adoption entrypoint has no project MCP merge adapter")
		return nil
	}
	actions, err := s.opts.DecisionMCPReconcile(ctx, s.repoPath, s.opts.DryRun)
	if err != nil {
		return fmt.Errorf("reconcile AsDecided project MCP servers: %w", err)
	}
	for i := 0; i < len(actions) && i < 16; i++ {
		action := actions[i]
		s.report.ActionDetails = append(s.report.ActionDetails, action)
		switch action.Action {
		case actionCreate:
			s.report.CreatedFiles = append(s.report.CreatedFiles, action.Path)
		case actionReconcile, actionMerge, actionAppend:
			s.report.ReconciledFiles = append(s.report.ReconciledFiles, action.Path)
		case actionSkip:
			s.report.Warnings = append(s.report.Warnings, action.Path+": "+action.Details)
		}
	}
	return nil
}
