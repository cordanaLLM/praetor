// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// 1. Positive Tests
// ----------------------------------------------------------------------------

func TestParseClosingIssueNumbers_Positive(t *testing.T) {
	body := `
## Summary
Fixes #12
Closes cordanaLLM/praetor#42
Resolves other-repo#99
Also fixed #100 and closed #101.
`
	nums := ParseClosingIssueNumbers(body)
	// Repository-qualified references (42, 99) name another repository and are skipped.
	want := []int{12, 100, 101}
	if len(nums) != len(want) {
		t.Fatalf("expected %d closing issue numbers, got %d: %v", len(want), len(nums), nums)
	}
	for i, n := range want {
		if nums[i] != n {
			t.Errorf("at index %d: expected %d, got %d", i, n, nums[i])
		}
	}
}

func TestReadAndParseMergedPullRequests_Positive(t *testing.T) {
	now := time.Now().Truncate(time.Second).UTC()
	jsonContent := fmt.Sprintf(`[
		{
			"number": 101,
			"head_branch": "feat/first",
			"title": "First landed PR",
			"milestone": "1.0",
			"created_at": %q,
			"merged_at": %q,
			"closing_issues": [
				{
					"number": 12,
					"created_at": %q
				}
			]
		}
	]`, now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))

	prs, err := ParseMergedPullRequests([]byte(jsonContent))
	if err != nil {
		t.Fatalf("unexpected error parsing JSON: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if prs[0].Number != 101 || prs[0].HeadBranch != "feat/first" || prs[0].Milestone != "1.0" {
		t.Errorf("unexpected PR content: %+v", prs[0])
	}
	if len(prs[0].ClosingIssues) != 1 || prs[0].ClosingIssues[0].Number != 12 {
		t.Errorf("unexpected closing issues: %+v", prs[0].ClosingIssues)
	}
	assertReadMergedPullRequests(t, jsonContent)
}

func assertReadMergedPullRequests(t *testing.T, jsonContent string) {
	t.Helper()
	prsFromReader, err := ReadMergedPullRequests(strings.NewReader(jsonContent))
	if err != nil {
		t.Fatalf("unexpected error reading from reader: %v", err)
	}
	if len(prsFromReader) != 1 || prsFromReader[0].Number != 101 {
		t.Errorf("unexpected PR from reader: %+v", prsFromReader)
	}
}

func TestReadMergedPullRequestsFile_Positive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "merged_prs.json")
	content := `[{"number": 202, "head_branch": "fix/bug", "title": "Fixed bug", "created_at": "2026-10-01T10:00:00Z", "merged_at": "2026-10-01T12:00:00Z"}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	prs, err := ReadMergedPullRequestsFile(path)
	if err != nil {
		t.Fatalf("unexpected error reading file: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 202 {
		t.Errorf("unexpected PR from file: %+v", prs)
	}
}

func TestParseGitHubMergedPulls_Positive(t *testing.T) {
	raw := `[
		{
			"number": 303,
			"title": "Add feature",
			"body": "Resolves #42",
			"created_at": "2026-10-01T10:00:00Z",
			"merged_at": "2026-10-01T11:00:00Z",
			"head": {"ref": "feat/neat"},
			"milestone": {"title": "Milestone 1"}
		},
		{
			"number": 304,
			"title": "Closed without merge",
			"body": "Fixes #43",
			"created_at": "2026-10-01T10:00:00Z",
			"merged_at": null,
			"head": {"ref": "feat/abandoned"}
		}
	]`
	prs, rawCount, _, err := parseGitHubMergedPulls([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error parsing github pulls: %v", err)
	}
	if rawCount != 2 {
		t.Errorf("expected rawCount 2, got %d", rawCount)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 merged PR (excluding unmerged), got %d", len(prs))
	}
	if prs[0].Number != 303 || prs[0].HeadBranch != "feat/neat" || prs[0].Milestone != "Milestone 1" {
		t.Errorf("unexpected PR parsed: %+v", prs[0])
	}
	if len(prs[0].ClosingIssues) != 1 || prs[0].ClosingIssues[0].Number != 42 {
		t.Errorf("unexpected closing issues: %+v", prs[0].ClosingIssues)
	}
}

// ----------------------------------------------------------------------------
// 2. Negative Tests
// ----------------------------------------------------------------------------

func TestParseClosingIssueNumbers_Negative(t *testing.T) {
	// Refs and mentions should NOT be parsed as closing issues
	body := `
Related to #12.
Refs #42.
See issue #99 for details.
Depends-On: #100.
`
	nums := ParseClosingIssueNumbers(body)
	if len(nums) != 0 {
		t.Fatalf("expected 0 closing issue numbers for non-closing refs, got %v", nums)
	}

	// Empty body
	if nums := ParseClosingIssueNumbers(""); nums != nil {
		t.Errorf("expected nil for empty body, got %v", nums)
	}
	if nums := ParseClosingIssueNumbers("   \n\t  "); nums != nil {
		t.Errorf("expected nil for whitespace body, got %v", nums)
	}
}

func TestReadAndParseMergedPullRequests_Negative(t *testing.T) {
	// Nil reader
	if _, err := ReadMergedPullRequests(nil); err == nil {
		t.Error("expected error for nil reader")
	}

	// Empty data
	if _, err := ParseMergedPullRequests([]byte("")); err == nil {
		t.Error("expected error for empty byte slice")
	}
	if _, err := ParseMergedPullRequests([]byte("   \n")); err == nil {
		t.Error("expected error for whitespace byte slice")
	}

	// Invalid JSON syntax
	if _, err := ParseMergedPullRequests([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}

	// Missing file / empty path
	if _, err := ReadMergedPullRequestsFile(""); err == nil {
		t.Error("expected error for empty file path")
	}
	if _, err := ReadMergedPullRequestsFile("/nonexistent/file/path.json"); err == nil {
		t.Error("expected error for nonexistent file path")
	}

	// GitHub null body
	if _, _, _, err := parseGitHubMergedPulls([]byte("null")); err == nil {
		t.Error("expected error for null github pulls listing")
	}
}

// ----------------------------------------------------------------------------
// 3. Boundary Tests
// ----------------------------------------------------------------------------

func TestParseClosingIssueNumbers_Boundary_DedupeAndLimits(t *testing.T) {
	// Deduplication test: repeated references to #12
	body := "Closes #12, fixes #12, resolves #12."
	nums := ParseClosingIssueNumbers(body)
	if len(nums) != 1 || nums[0] != 12 {
		t.Fatalf("expected deduplication to [12], got %v", nums)
	}

	// Line limits: Lines beyond MaxLinesLimit should not be read
	var sb strings.Builder
	for i := 0; i < MaxLinesLimit+10; i++ {
		if i == MaxLinesLimit+5 {
			sb.WriteString("Closes #999\n")
		} else {
			sb.WriteString("Line of text\n")
		}
	}
	numsLimit := ParseClosingIssueNumbers(sb.String())
	for _, n := range numsLimit {
		if n == 999 {
			t.Errorf("found issue 999 beyond MaxLinesLimit: %v", numsLimit)
		}
	}

	// Dependency cap test
	var sb2 strings.Builder
	for i := 1; i <= MaxDependenciesLimit+20; i++ {
		fmt.Fprintf(&sb2, "Closes #%d\n", i)
	}
	numsCap := ParseClosingIssueNumbers(sb2.String())
	if len(numsCap) != MaxDependenciesLimit {
		t.Fatalf("expected cap of %d, got %d", MaxDependenciesLimit, len(numsCap))
	}
}

func TestReadAndParseMergedPullRequests_Boundary_Limits(t *testing.T) {
	// Exceeding byte limit in reader
	huge := bytes.Repeat([]byte(" "), MaxMergedPRBytes+10)
	if _, err := ReadMergedPullRequests(bytes.NewReader(huge)); err == nil {
		t.Error("expected error when byte limit is exceeded in ReadMergedPullRequests")
	}

	// Exceeding count limit
	var items []string
	for i := 1; i <= MaxMergedPRsLimit+5; i++ {
		items = append(items, fmt.Sprintf(`{"number": %d, "head_branch": "b%d", "title": "t%d"}`, i, i, i))
	}
	jsonList := "[" + strings.Join(items, ",") + "]"
	if _, err := ParseMergedPullRequests([]byte(jsonList)); err == nil {
		t.Error("expected error when count limit exceeds MaxMergedPRsLimit")
	}
}

func TestParseMergedPullRequests_UnitsWrapper_PositiveNegativeBoundary(t *testing.T) {
	// Positive: units wrapper with PR records
	pos := `{"units": [{"number": 1, "head_branch": "feat/wrapper", "title": "Wrapper PR"}]}`
	prs, err := ParseMergedPullRequests([]byte(pos))
	if err != nil {
		t.Fatalf("unexpected error parsing units wrapper: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 1 {
		t.Fatalf("expected 1 PR with number 1, got %+v", prs)
	}

	// Negative: malformed units field reports real wrapper error
	neg := `{"units": "not-an-array"}`
	_, err = ParseMergedPullRequests([]byte(neg))
	if err == nil {
		t.Fatal("expected error parsing malformed units wrapper")
	}
	if !strings.Contains(err.Error(), "parse merged pull requests wrapper") {
		t.Errorf("expected wrapper error message, got: %v", err)
	}

	// Boundary: empty units array yields empty ledger without error
	bound := `{"units": []}`
	prs, err = ParseMergedPullRequests([]byte(bound))
	if err != nil {
		t.Fatalf("unexpected error on empty units wrapper: %v", err)
	}
	if prs == nil || len(prs) != 0 {
		t.Fatalf("expected empty non-nil slice, got: %+v", prs)
	}
}

func TestMergedPullRequest_EffectiveNumberAndAlias_PositiveBoundary(t *testing.T) {
	// Positive: pull_request_number alias normalized and read by EffectiveNumber
	aliasJSON := `[{"pull_request_number": 88, "head_branch": "feat/alias", "title": "Alias PR"}]`
	prs, err := ParseMergedPullRequests([]byte(aliasJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing pull_request_number alias: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 88 || prs[0].EffectiveNumber() != 88 {
		t.Fatalf("expected number 88, got Number=%d, Effective=%d", prs[0].Number, prs[0].EffectiveNumber())
	}

	// Boundary: explicit number takes precedence over pull_request_number
	both := MergedPullRequest{Number: 42, PRNumber: 99}
	if got := both.EffectiveNumber(); got != 42 {
		t.Errorf("expected number 42 to take precedence over PRNumber 99, got %d", got)
	}

	// Boundary: neither set yields 0
	neither := MergedPullRequest{}
	if got := neither.EffectiveNumber(); got != 0 {
		t.Errorf("expected 0 for unset numbers, got %d", got)
	}
}

func TestVectorFieldRaw_Positive_StructuredObject(t *testing.T) {
	structuredJSON := `{"tokens_by_provider": {"value": {"anthropic": 100}, "provenance": "measured"}}`
	var pr1 MergedPullRequest
	if err := json.Unmarshal([]byte(structuredJSON), &pr1); err != nil {
		t.Fatalf("unexpected error unmarshaling structured vector field: %v", err)
	}
	if pr1.TokensByProvider == nil || pr1.TokensByProvider.Provenance != "measured" {
		t.Fatalf("expected provenance measured, got: %+v", pr1.TokensByProvider)
	}
}

func TestVectorFieldRaw_Boundary_RawScalar(t *testing.T) {
	rawScalar := `{"wall_seconds": 120}`
	var pr2 MergedPullRequest
	if err := json.Unmarshal([]byte(rawScalar), &pr2); err != nil {
		t.Fatalf("unexpected error on raw scalar fallback: %v", err)
	}
	if pr2.WallSeconds == nil || pr2.WallSeconds.Provenance != "" || string(pr2.WallSeconds.Value) != "120" {
		t.Fatalf("expected raw value 120 with empty provenance, got: %+v", pr2.WallSeconds)
	}
}

func TestVectorFieldRaw_Boundary_RawObject(t *testing.T) {
	rawObj := `{"tokens_by_provider": {"anthropic": 500}}`
	var pr3 MergedPullRequest
	if err := json.Unmarshal([]byte(rawObj), &pr3); err != nil {
		t.Fatalf("unexpected error on raw object fallback: %v", err)
	}
	if pr3.TokensByProvider == nil || pr3.TokensByProvider.Provenance != "" {
		t.Fatalf("expected empty provenance for raw object fallback, got: %+v", pr3.TokensByProvider)
	}
}
