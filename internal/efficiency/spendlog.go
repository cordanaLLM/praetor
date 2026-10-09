// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// SpendReport records gateway usage attributed per pull request, plus the spend that no
// pull request claims. Zero-spend entries (local models) count as requests.
type SpendReport struct {
	SpendByPRNumber          map[int]float64
	RequestsByPRNumber       map[int]int
	LocalRequestsByPRNumber  map[int]int
	FrontierTokensByPRNumber map[int]int64
	Unattributed             float64
	TotalSpend               float64
	Entries                  int
	DuplicateRequests        int
	seenRequestIDs           map[string]struct{}
}

func newSpendReport() *SpendReport {
	return &SpendReport{
		SpendByPRNumber:          make(map[int]float64),
		RequestsByPRNumber:       make(map[int]int),
		LocalRequestsByPRNumber:  make(map[int]int),
		FrontierTokensByPRNumber: make(map[int]int64),
		seenRequestIDs:           make(map[string]struct{}),
	}
}

// rawSpendEntry is one row of a LiteLLM_SpendLogs export. The accepted fields are the
// columns of that table (request_id, model, model_group, spend, total_tokens, prompt_tokens,
// completion_tokens, startTime, request_tags, metadata); branch, pull_request, pr and tags
// are accepted as flat attribution columns.
type rawSpendEntry struct {
	RequestID        string          `json:"request_id"`
	Model            string          `json:"model"`
	ModelGroup       string          `json:"model_group"`
	Spend            float64         `json:"spend"`
	TotalTokens      int64           `json:"total_tokens"`
	PromptTokens     int64           `json:"prompt_tokens"`
	CompletionTokens int64           `json:"completion_tokens"`
	StartTime        string          `json:"startTime"`
	RequestTags      json.RawMessage `json:"request_tags"`
	Metadata         json.RawMessage `json:"metadata"`
	Branch           string          `json:"branch"`
	PullRequest      json.RawMessage `json:"pull_request"`
	PR               json.RawMessage `json:"pr"`
	Tags             json.RawMessage `json:"tags"`
}

type attribution struct {
	branchToPR map[string]int
	validPRs   map[int]bool
}

// decodeStringList reads a JSON array of strings, a JSON-encoded array in a string, or a
// comma separated string (CSV exports).
func decodeStringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[") {
		if err := json.Unmarshal([]byte(text), &list); err == nil {
			return list
		}
	}
	if text == "" {
		return nil
	}
	return strings.Split(text, ",")
}

// spendMetadata is the attribution part of the metadata column.
type spendMetadata struct {
	Branch      string          `json:"branch"`
	PullRequest json.RawMessage `json:"pull_request"`
	PR          json.RawMessage `json:"pr"`
	Tags        json.RawMessage `json:"tags"`
}

// decodeMetadata reads the metadata column: a JSON object or a JSON-encoded object in a string.
// Metadata of any other shape carries no attribution.
func decodeMetadata(raw json.RawMessage) spendMetadata {
	var meta spendMetadata
	if len(raw) == 0 {
		return meta
	}
	if err := json.Unmarshal(raw, &meta); err == nil {
		return meta
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return spendMetadata{}
	}
	if err := json.Unmarshal([]byte(text), &meta); err != nil {
		return spendMetadata{}
	}
	return meta
}

func parseRawPR(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var num int
	if err := json.Unmarshal(raw, &num); err == nil && num > 0 {
		return num
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(str), "#")); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (a attribution) matchTag(tag string) (int, bool) {
	tag = strings.TrimSpace(tag)
	if prNum, ok := a.branchToPR[tag]; ok {
		return prNum, true
	}
	if b, found := strings.CutPrefix(tag, "branch:"); found {
		prNum, ok := a.branchToPR[strings.TrimSpace(b)]
		return prNum, ok
	}
	for _, prefix := range []string{"pr:", "pull_request:"} {
		if val, found := strings.CutPrefix(tag, prefix); found {
			num, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(val), "#"))
			return num, err == nil && a.validPRs[num]
		}
	}
	return 0, false
}

func (a attribution) matchTags(tags []string) (int, bool) {
	for i := 0; i < len(tags) && i < 50; i++ {
		if prNum, ok := a.matchTag(tags[i]); ok {
			return prNum, true
		}
	}
	return 0, false
}

func (a attribution) matchFields(branch string, pr ...json.RawMessage) (int, bool) {
	if prNum, ok := a.branchToPR[strings.TrimSpace(branch)]; ok && branch != "" {
		return prNum, true
	}
	for _, raw := range pr {
		if num := parseRawPR(raw); num > 0 && a.validPRs[num] {
			return num, true
		}
	}
	return 0, false
}

// attribute assigns an entry to a pull request by branch or pull-request tag in the flat
// columns, request_tags or metadata (branch, pull_request, pr, tags).
func (a attribution) attribute(entry *rawSpendEntry) (int, bool) {
	if prNum, ok := a.matchFields(entry.Branch, entry.PullRequest, entry.PR); ok {
		return prNum, true
	}
	if prNum, ok := a.matchTags(decodeStringList(entry.Tags)); ok {
		return prNum, true
	}
	if prNum, ok := a.matchTags(decodeStringList(entry.RequestTags)); ok {
		return prNum, true
	}
	meta := decodeMetadata(entry.Metadata)
	if prNum, ok := a.matchFields(meta.Branch, meta.PullRequest, meta.PR); ok {
		return prNum, true
	}
	return a.matchTags(decodeStringList(meta.Tags))
}

// entryTokens counts input, cache and output tokens like transcripts do: total_tokens
// (prompt including cached input, plus completion), else prompt plus completion.
func entryTokens(e *rawSpendEntry) int64 {
	if e.TotalTokens > 0 {
		return e.TotalTokens
	}
	return e.PromptTokens + e.CompletionTokens
}

// duplicate reports a request_id seen before; an export that overlaps itself counts a
// request once.
func (r *SpendReport) duplicate(entry *rawSpendEntry) bool {
	if entry.RequestID == "" {
		return false
	}
	if _, seen := r.seenRequestIDs[entry.RequestID]; seen {
		r.DuplicateRequests++
		return true
	}
	r.seenRequestIDs[entry.RequestID] = struct{}{}
	return false
}

func (r *SpendReport) process(entry *rawSpendEntry, classifier *Classifier, attr attribution) {
	if r.duplicate(entry) {
		return
	}
	r.Entries++
	spend := entry.Spend
	if spend > 0 {
		r.TotalSpend += spend
	}
	prNum, ok := attr.attribute(entry)
	if !ok {
		if spend > 0 {
			r.Unattributed += spend
		}
		return
	}
	if spend > 0 {
		r.SpendByPRNumber[prNum] += spend
	}
	r.RequestsByPRNumber[prNum]++
	if classifier.IsLocal(entry.ModelGroup, entry.Model) {
		r.LocalRequestsByPRNumber[prNum]++
	}
	if classifier.IsFrontier(entry.ModelGroup, entry.Model) {
		r.FrontierTokensByPRNumber[prNum] += entryTokens(entry)
	}
}

func buildAttribution(prs []forge.MergedPullRequest) attribution {
	attr := attribution{branchToPR: make(map[string]int, len(prs)), validPRs: make(map[int]bool, len(prs))}
	owner := branchOwners(prs)
	for _, pr := range prs {
		attr.validPRs[pr.Number] = true
	}
	for branch, number := range owner {
		attr.branchToPR[branch] = number
	}
	return attr
}

// branchOwners maps a head branch to the pull request that owns its usage: the one merged
// last when a branch name was reused.
func branchOwners(prs []forge.MergedPullRequest) map[string]int {
	owners := make(map[string]int, len(prs))
	latest := make(map[string]forge.MergedPullRequest, len(prs))
	for _, pr := range prs {
		if pr.HeadBranch == "" {
			continue
		}
		if prev, ok := latest[pr.HeadBranch]; !ok || pr.MergedAt.After(prev.MergedAt) {
			latest[pr.HeadBranch] = pr
			owners[pr.HeadBranch] = pr.Number
		}
	}
	return owners
}
