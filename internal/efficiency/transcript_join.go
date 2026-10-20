// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

// MaxResponseToolBytes caps distinct tool inputs scanned across all blocks of a response.
// Overflow fails the read; a partial scan must never turn an ambiguous response into a join.
const MaxResponseToolBytes = 64 * 1024
const maxTranscriptResponses = 1000000
const maxTranscriptRoots = 50000

// TranscriptShare reports usage left without a branch, including all token classes.
// Per-unit shares overlap when sessions mention several units; the report total is unique.
type TranscriptShare struct {
	Responses             int   `json:"responses"`
	Tokens                int64 `json:"tokens"`
	UnattributedResponses int   `json:"unattributed_responses"`
	UnattributedTokens    int64 `json:"unattributed_tokens"`
}

// String displays unresolved counts over the observed response and token totals.
func (s TranscriptShare) String() string {
	return fmt.Sprintf("%d/%d responses, %d/%d tokens", s.UnattributedResponses, s.Responses, s.UnattributedTokens, s.Tokens)
}

func (s *TranscriptShare) add(stats *BranchTranscriptStats, unattributed bool) {
	tokens := stats.InputTokens + stats.CacheCreationTokens + stats.CacheReadTokens + stats.OutputTokens
	s.Responses += stats.TotalRequests
	s.Tokens += tokens
	if unattributed {
		s.UnattributedResponses += stats.TotalRequests
		s.UnattributedTokens += tokens
	}
}

type transcriptResponse struct {
	stats           BranchTranscriptStats
	session, branch string
	branchConflict  bool
	paths           map[string]bool
	tools           map[string]bool
	bytes           int
}

type transcriptJoin struct {
	roots         map[string]string
	live          map[string]bool
	defaultBranch string
	responses     map[string]*transcriptResponse
	unresolved    map[string]bool
	recorded      int
	conflicts     map[string]bool
}

func newTranscriptJoin(roots map[string]string, defaultBranch string) *transcriptJoin {
	j := &transcriptJoin{roots: make(map[string]string), live: make(map[string]bool), defaultBranch: defaultBranch,
		responses: make(map[string]*transcriptResponse), unresolved: make(map[string]bool), conflicts: make(map[string]bool)}
	for root, branch := range roots {
		root = filepath.ToSlash(filepath.Clean(root))
		j.roots[root], j.live[root] = branch, branch != ""
	}
	return j
}

func (r *transcriptReader) branchStats(branch string) *BranchTranscriptStats {
	if s := r.byBranch[branch]; s != nil {
		return s
	}
	s := &BranchTranscriptStats{seenResponses: make(map[string]usageSnapshot)}
	r.byBranch[branch] = s
	return s
}

func (j *transcriptJoin) response(rec *harvester.LedgerRecord, source string, line int) (*transcriptResponse, error) {
	key := responseKey(rec)
	if key == "" {
		key = fmt.Sprintf("%s:%d", source, line)
	}
	if response := j.responses[key]; response != nil {
		return response, nil
	}
	if len(j.responses) >= maxTranscriptResponses {
		return nil, fmt.Errorf("transcript responses exceed %d bound", maxTranscriptResponses)
	}
	session := rec.SessionID
	if session == "" {
		session = source
	}
	response := &transcriptResponse{session: session, paths: make(map[string]bool), tools: make(map[string]bool),
		stats: BranchTranscriptStats{seenResponses: make(map[string]usageSnapshot)}}
	j.responses[key] = response
	return response, nil
}

func (j *transcriptJoin) collect(rec *harvester.LedgerRecord, source string, line int, r *transcriptReader) error {
	branch := strings.TrimSpace(rec.GitBranch)
	if branch != "" {
		stats := r.branchStats(branch)
		if isOperatorTouch(rec) {
			stats.OperatorTouches++
		}
	}
	if rec.Message == nil || rec.Type != "assistant" {
		return nil
	}
	response, err := j.response(rec, source, line)
	if err != nil {
		return err
	}
	if branch != "" && branch != j.defaultBranch {
		response.setBranch(branch)
	}
	if rec.Message.Usage != nil {
		response.stats.applyUsage(rec, r.classifier)
	}
	if err := j.collectTools(rec, response); err != nil {
		return fmt.Errorf("%s:%d: %w", source, line, err)
	}
	return nil
}

func (j *transcriptJoin) resolve(paths map[string]bool) map[string]bool {
	branches := make(map[string]bool)
	for path := range paths {
		root := ""
		for candidate := range j.roots {
			if len(candidate) > len(root) && pathWithin(path, candidate) {
				root = candidate
			}
		}
		if branch := j.roots[root]; branch != "" {
			branches[branch] = true
		} else {
			j.unresolved[path] = true
		}
	}
	return branches
}

func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func addTranscriptStats(dst, src *BranchTranscriptStats) {
	dst.TotalRequests += src.TotalRequests
	dst.LocalRequests += src.LocalRequests
	dst.FrontierTokens += src.FrontierTokens
	dst.InputTokens += src.InputTokens
	dst.CacheCreationTokens += src.CacheCreationTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.OutputTokens += src.OutputTokens
}

type sessionAttribution struct {
	branches map[string]bool
	missing  BranchTranscriptStats
}

func singleResponseBranch(explicit string, branches map[string]bool) string {
	if explicit != "" {
		return explicit
	}
	if len(branches) == 1 {
		for branch := range branches {
			return branch
		}
	}
	return ""
}

func (r *transcriptReader) attribute(response *transcriptResponse, session *sessionAttribution) {
	branches := r.join.resolve(response.paths)
	branch := singleResponseBranch(response.branch, branches)
	if response.branchConflict {
		branch = ""
	}
	for candidate := range branches {
		session.branches[candidate] = true
		if branch == "" && len(branches) > 1 {
			r.branchStats(candidate).AmbiguousResponses += response.stats.TotalRequests
		}
	}
	r.notes.Coverage.add(&response.stats, branch == "")
	if branch == "" {
		addTranscriptStats(&session.missing, &response.stats)
		return
	}
	session.branches[branch] = true
	addTranscriptStats(r.branchStats(branch), &response.stats)
}

func (r *transcriptReader) finish() {
	if r.join == nil {
		return
	}
	j := r.join
	for _, branch := range j.roots {
		if branch != "" {
			r.branchStats(branch).WorktreeResolved = true
		}
	}
	sessions := make(map[string]*sessionAttribution)
	for _, response := range j.responses {
		session := sessions[response.session]
		if session == nil {
			session = &sessionAttribution{branches: make(map[string]bool)}
			sessions[response.session] = session
		}
		r.attribute(response, session)
	}
	for _, session := range sessions {
		for branch := range session.branches {
			r.branchStats(branch).Unattributed.add(&session.missing, true)
		}
	}
	r.notes.JoinNotes = append(r.notes.JoinNotes, j.note())
}

func (j *transcriptJoin) note() string {
	unresolvedRoots := 0
	for _, branch := range j.roots {
		if branch == "" {
			unresolvedRoots++
		}
	}
	return fmt.Sprintf("transcript joins: gitBranch (non-default); tool inputs; live worktrees=%d; transcript worktree creations=%d; unresolved roots=%d; unresolved paths=%d; default branch=%s", len(j.live), j.recorded, unresolvedRoots, len(j.unresolved), j.defaultBranch)
}

func (r *transcriptResponse) setBranch(branch string) {
	if r.branch != "" && r.branch != branch {
		r.branchConflict = true
	}
	r.branch = branch
}
