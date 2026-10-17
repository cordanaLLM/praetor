package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Adoption's fold of authored vendor context (#1073). A vendor context file (CLAUDE.md and the
// other compile-context projections) is always rewritten from AGENTS.md, so text it holds that
// AGENTS.md lacks used to survive only in a backup under .workingdir, which git ignores. Before
// the rewrite, that text moves into AGENTS.md below its instructions; the projection written
// next then carries it, and it stays in the working tree for the next commit. The fold writes
// nothing itself: the agent-harness step writes AGENTS.md once, after it validated the result,
// with the backup and the write guard taken from the bytes the run found, so a run that fails
// leaves AGENTS.md as it was.

// agentsFold is AGENTS.md with the folded vendor text, held for the agent-harness step.
type agentsFold struct {
	// found is AGENTS.md as the run found it, and exists whether it existed.
	found  []byte
	exists bool
	// text is found with the folded blocks appended, and from the vendor files they came from.
	text string
	from []string
}

// foldNoticeFormat opens a folded block; %s is the vendor file it came from.
const foldNoticeFormat = "<!-- Folded by praetorctl adopt from %s: text that file held and AGENTS.md lacked. -->"

// foldAuthoredVendorContext folds into AGENTS.md the text of every vendor context file clients
// selects that is not a projection of AGENTS.md as the run found it (prior), before the
// agent-harness step rewrites them. A line harness, the harness this run renders, holds is not
// folded: a projection compiled from an earlier AGENTS.md, or one compile-context spliced a
// fresh register block into, carries praetor's text, which the harness step writes anyway. The
// folded AGENTS.md is held in s.fold, which agentsSource returns, and written by the harness
// step (resolveAgentsContent); warnFolded reports it once that write is done.
func foldAuthoredVendorContext(ctx context.Context, s *adoptSession, clients []string, prior priorVendorProjections, harness string) error {
	paths, _, err := agentcontext.TargetPaths(clients)
	if err != nil {
		return fmt.Errorf("agent_clients in %s: %w", manifestFile, err)
	}
	agents, exists, err := s.agentsSource(ctx)
	if err != nil {
		return err
	}
	text, folded := string(agents), []string{}
	for i := 0; i < len(paths) && i < maxTranspileTargets; i++ {
		before, found, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(paths[i]))
		if err != nil {
			return fmt.Errorf("vendor context target %s: %w", paths[i], err)
		}
		if !found || prior.isPriorProjection(paths[i], before) {
			continue
		}
		if block := vendorOnlyText(string(before), text+"\n"+harness, agentcontext.ProjectionFrame(paths[i])); block != "" {
			text = appendFold(text, paths[i], block)
			folded = append(folded, paths[i])
		}
	}
	if len(folded) > 0 {
		s.fold = &agentsFold{found: agents, exists: exists, text: text, from: folded}
	}
	return nil
}

// warnFolded warns, once the harness step wrote AGENTS.md, which vendor files were folded into it.
func (s *adoptSession) warnFolded() {
	if s.fold == nil {
		return
	}
	s.report.addWarning("%s held text %s lacked; it is folded into %s below its instructions, so the projection "+
		"compile-context writes next carries it and it stays in the working tree. Review it there and commit it.",
		strings.Join(s.fold.from, ", "), agentsFile, agentsFile)
}

// agentsSource returns AGENTS.md as this run leaves it so far: the folded text (s.fold), else
// the text a dry run planned for it, else the bytes on disk, and whether it exists.
func (s *adoptSession) agentsSource(ctx context.Context) ([]byte, bool, error) {
	if s.fold != nil {
		return []byte(s.fold.text), true, nil
	}
	if planned, ok := s.dryRunWrites[agentsFile]; ok && planned != nil {
		return planned, true, nil
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, agentsFile)
	if err != nil {
		return nil, false, fmt.Errorf("read %s before folding vendor context into it: %w", agentsFile, err)
	}
	return data, exists, nil
}

// vendorOnlyText returns the text of vendor, a vendor context file, that agents lacks. A file a
// person wrote counts whole, unless every line of it already stands in agents. A compiled
// projection someone edited counts only for its lines agents lacks, without the comment lines
// and the lines of frame (agentcontext.ProjectionFrame) compile-context writes, so the
// projection's own frame, the Cursor frontmatter included, is never folded.
func vendorOnlyText(vendor, agents, frame string) string {
	vendor = strings.ReplaceAll(vendor, "\r\n", "\n")
	have := lineSet(agents)
	if agentcontext.IsCompiledProjection(vendor) {
		for line := range lineSet(frame) {
			have[line] = true
		}
	}
	lines := strings.Split(vendor, "\n")
	missing := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || have[trimmed] || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		missing = append(missing, line)
	}
	switch {
	case len(missing) == 0:
		return ""
	case agentcontext.IsCompiledProjection(vendor):
		return strings.Join(missing, "\n")
	}
	return strings.TrimSpace(vendor)
}

// lineSet returns the trimmed, non-empty lines of text.
func lineSet(text string) map[string]bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	set := make(map[string]bool, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

// appendFold returns text with block, folded from the vendor file at rel, appended under its
// notice, in text's line-ending style.
func appendFold(text, rel, block string) string {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	head := strings.TrimRight(text, "\r\n")
	if head != "" {
		head += newline + newline
	}
	added := fmt.Sprintf(foldNoticeFormat, rel) + "\n\n" + block + "\n"
	return head + strings.ReplaceAll(added, "\n", newline)
}
