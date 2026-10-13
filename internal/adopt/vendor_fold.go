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
// next then carries it, and it stays in the working tree for the next commit.

// foldNoticeFormat opens a folded block; %s is the vendor file it came from.
const foldNoticeFormat = "<!-- Folded by praetorctl adopt from %s: text that file held and AGENTS.md lacked. -->"

// foldAuthoredVendorContext folds into AGENTS.md the text of every vendor context file clients
// selects that is not a projection of AGENTS.md as the run found it (prior), before the
// agent-harness step rewrites them. It writes AGENTS.md only when something is folded, and
// warns with the files folded. A dry run plans the write (planDryRunWrite), so the harness
// merge that follows reads the folded text either way (agentsSource).
func foldAuthoredVendorContext(ctx context.Context, s *adoptSession, clients []string, prior priorVendorProjections) error {
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
		if block := vendorOnlyText(string(before), text); block != "" {
			text = appendFold(text, paths[i], block)
			folded = append(folded, paths[i])
		}
	}
	if len(folded) == 0 {
		return nil
	}
	if err := s.writeFoldedAgents(ctx, agents, exists, text); err != nil {
		return err
	}
	s.report.addWarning("%s held text %s lacked; it is folded into %s below its instructions, so the projection "+
		"compile-context writes next carries it and it stays in the working tree. Review it there and commit it.",
		strings.Join(folded, ", "), agentsFile, agentsFile)
	return nil
}

// agentsSource returns AGENTS.md as this run leaves it so far: the text a dry run planned for it,
// else the bytes on disk, and whether it exists.
func (s *adoptSession) agentsSource(ctx context.Context) ([]byte, bool, error) {
	if planned, ok := s.dryRunWrites[agentsFile]; ok && planned != nil {
		return planned, true, nil
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, agentsFile)
	if err != nil {
		return nil, false, fmt.Errorf("read %s before folding vendor context into it: %w", agentsFile, err)
	}
	return data, exists, nil
}

// writeFoldedAgents writes text, AGENTS.md with folded blocks appended, over before: created
// when AGENTS.md did not exist, else replaced only while it still holds before. The change only
// appends, so nothing of before is lost and no backup is taken.
func (s *adoptSession) writeFoldedAgents(ctx context.Context, before []byte, exists bool, text string) error {
	s.planDryRunWrite(agentsFile, []byte(text))
	if s.opts.DryRun {
		return nil
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: filePerm}
	if err := contextopt.ReplaceSnapshotIn(ctx, s.repoPath, agentsFile, []byte(text), options); err != nil {
		return fmt.Errorf("fold vendor context into %s: %w", agentsFile, err)
	}
	return nil
}

// vendorOnlyText returns the text of vendor, a vendor context file, that agents lacks. A file a
// person wrote counts whole, unless every line of it already stands in agents. A compiled
// projection someone edited counts only for its lines agents lacks, without the comment lines
// compile-context writes, so the projection's own frame is never folded.
func vendorOnlyText(vendor, agents string) string {
	vendor = strings.ReplaceAll(vendor, "\r\n", "\n")
	have := lineSet(agents)
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
