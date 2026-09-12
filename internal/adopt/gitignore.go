package adopt

import (
	"context"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// reconcileGitIgnore appends the private-directory rule after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, gitIgnoreFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	text := string(data)
	if strings.HasSuffix(strings.TrimSpace(text), "\n/.workingdir/") || strings.TrimSpace(text) == "/.workingdir/" {
		s.report.recordReconciled(gitIgnoreFile, "Private working directory ignore rule preserved")
		return nil
	}
	if !exists {
		text = "bin/\n*.test\n*.out\n.DS_Store\n"
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "/.workingdir/\n"
	if !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, full, []byte(text), contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm}); err != nil {
			return err
		}
	}
	if exists {
		s.report.recordReconciledAs(gitIgnoreFile, actionAppend, "Preserved existing rules and excluded private working directory")
	} else {
		s.report.recordCreated(gitIgnoreFile, "Created ignore rules for build artifacts and private working directory")
	}
	return nil
}
