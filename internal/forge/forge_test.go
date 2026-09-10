package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// 1. POSITIVE TESTS (3D Dimension 1)
// ============================================================================

func TestNewForge_Providers_Positive(t *testing.T) {
	ctx := context.Background()
	providers := []string{"github", "gitlab", "gitea", "forgejo"}

	for _, p := range providers {
		f, err := NewForge(p, "test-token", "")
		if err != nil {
			t.Fatalf("failed to create forge driver for %s: %v", p, err)
		}
		if err := f.Authenticate(ctx); err != nil {
			t.Fatalf("expected authentication to pass for %s: %v", p, err)
		}
	}
}

func TestIssueDependencyParsing_Positive(t *testing.T) {
	body := `
Resolves core architecture.
Depends-On: cordanaLLM/standards#42
Some other context.
Depends-On: #15
`
	refs := ParseIssueDependencies(body)
	if len(refs) != 2 {
		t.Fatalf("expected 2 dependency refs, got %d", len(refs))
	}
	if refs[0].Owner != "cordanaLLM" || refs[0].Repo != "standards" || refs[0].Number != 42 {
		t.Errorf("ref 0 mismatch: %+v", refs[0])
	}
	if refs[1].Number != 15 || refs[1].Owner != "" {
		t.Errorf("ref 1 mismatch: %+v", refs[1])
	}
}

func TestSyncIssues_Positive(t *testing.T) {
	ctx := context.Background()
	gh := NewGitHubDriver("test-token", "")

	issues := []IssueSpec{
		{
			Title: "Implement Pillar VII Multi-Forge Federation",
			Body:  "Depends-On: cordanaLLM/standards#100",
			State: "open",
		},
		{
			Title: "Refactor ADR Pipeline",
			State: "open",
		},
	}

	err := SyncIssues(ctx, gh, issues)
	if err != nil {
		t.Fatalf("unexpected error syncing issues: %v", err)
	}
}

func TestValidatePRChecklist_Positive(t *testing.T) {
	prBody := `
## Summary
Implemented Phase 3 Pillar VII.

## Checklist
- [x] **HISS-16 (Context Integrity)**: Checked and verified via compile-context.
- [X] **3D Test Discipline (HISS-15)**: Positive, Negative, and Boundary tests pass.

## Ed25519 Exit-0 Verification Receipt
` + "```text\n" + `All standards verification gates passed cleanly. Receipt: e25519_abcdef123456\n` + "```\n"

	res, err := ValidatePRChecklist(prBody)
	if err != nil {
		t.Fatalf("unexpected error validating checklist: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected PR checklist to be valid, got errors: %v", res.Errors)
	}
	if !res.HasHISS16Check || !res.Has3DTestsCheck || !res.HasReceipt {
		t.Fatalf("expected all checks true, got: %+v", res)
	}
}

func TestAssignReviewers_Positive(t *testing.T) {
	codeowners := `
# CODEOWNERS
*                   @cordanaLLM/core-leads
internal/forge/*    @cordanaLLM/multi-forge-team
docs/*              @cordanaLLM/docs-team
`
	touched := []string{"internal/forge/issues.go", "docs/wiki/Home.md"}
	assignment, err := AssignReviewers(touched, codeowners)
	if err != nil {
		t.Fatalf("unexpected error assigning reviewers: %v", err)
	}

	foundForge := false
	foundDocs := false
	for _, h := range assignment.HumanReviewers {
		if h == "@cordanaLLM/multi-forge-team" {
			foundForge = true
		}
		if h == "@cordanaLLM/docs-team" {
			foundDocs = true
		}
	}

	if !foundForge || !foundDocs {
		t.Errorf("expected multi-forge and docs teams assigned, got: %v", assignment.HumanReviewers)
	}

	if len(assignment.BotReviewers) == 0 || assignment.BotReviewers[0] != StandardReviewBot {
		t.Errorf("expected bot reviewer %s, got: %v", StandardReviewBot, assignment.BotReviewers)
	}
}

func TestAnalyzeCommit_Positive_NonBreakingAndBreaking(t *testing.T) {
	// 1. Non-breaking commit
	nonBreaking := "feat(forge): add multi-forge issue syncing"
	res1, err1 := AnalyzeCommit(nonBreaking)
	if err1 != nil || !res1.Valid || res1.IsBreaking {
		t.Fatalf("expected valid non-breaking commit, got: %+v, err: %v", res1, err1)
	}

	// 2. Breaking commit with valid Migration footer
	breaking := `feat(api)!: remove deprecated legacy issue endpoint

Migration:
All callers must upgrade to POST /v2/issues before December 2026.`

	res2, err2 := AnalyzeCommit(breaking)
	if err2 != nil || !res2.Valid || !res2.IsBreaking || !res2.HasMigrationFooter {
		t.Fatalf("expected valid breaking commit with migration footer, got: %+v, err: %v", res2, err2)
	}
	if !strings.Contains(res2.MigrationText, "POST /v2/issues") {
		t.Errorf("migration instructions not parsed properly: %s", res2.MigrationText)
	}
}

func TestTranscribeDiscussionToADR_Positive(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	disc := Discussion{
		ID:                   42,
		Title:                "Multi-Forge Git Collaboration Federation",
		Category:             "RFC",
		Author:               "cordana-architect",
		Status:               "approved",
		ContextText:          "Modern fleets span GitHub, GitLab, and Gitea.",
		DecisionText:         "Adopt declarative Forge interface across all providers.",
		PositiveConsequences: []string{"Decoupled vendor neutrality"},
		NegativeConsequences: []string{"Multiple API clients to maintain"},
		CreatedAt:            time.Now(),
	}

	adr, err := TranscribeDiscussionToADR(ctx, disc, tempDir)
	if err != nil {
		t.Fatalf("unexpected error transcribing discussion: %v", err)
	}
	if adr.Number != 1 {
		t.Fatalf("expected ADR number 1, got %d", adr.Number)
	}
	if adr.Slug != "multi-forge-git-collaboration-federation" {
		t.Fatalf("unexpected slug: %s", adr.Slug)
	}
	if _, err := os.Stat(adr.FilePath); os.IsNotExist(err) {
		t.Fatalf("expected ADR file to exist at %s", adr.FilePath)
	}
}

func TestGenerateWiki_Positive(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	manifest, err := GenerateWiki(ctx, "/path/to/my-repo", tempDir)
	if err != nil {
		t.Fatalf("unexpected error generating wiki: %v", err)
	}
	if len(manifest.Pages) != 4 {
		t.Fatalf("expected 4 wiki pages, got %d", len(manifest.Pages))
	}

	expectedFiles := []string{
		"Home.md",
		"HISS-16-Invariants.md",
		"Architecture-Lattice.md",
		"API-Reference.md",
	}

	for _, ef := range expectedFiles {
		p := filepath.Join(tempDir, ef)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("missing expected wiki page %s: %v", ef, err)
			continue
		}
		if !strings.Contains(string(data), "```mermaid") {
			t.Errorf("expected wiki page %s to contain Mermaid diagram", ef)
		}
	}
}

// ============================================================================
// 2. NEGATIVE TESTS (3D Dimension 2)
// ============================================================================

func TestNewForge_Negative_InvalidProvider(t *testing.T) {
	_, err := NewForge("bitbucket", "token", "")
	if err == nil {
		t.Fatalf("expected error for unsupported forge provider, got nil")
	}
}

func TestSyncIssues_Negative_NilForgeAndUnauthenticated(t *testing.T) {
	ctx := context.Background()
	err := SyncIssues(ctx, nil, []IssueSpec{{Title: "Test"}})
	if err == nil {
		t.Fatalf("expected error for nil forge, got nil")
	}

	unauth := NewGitHubDriver("", "")
	errUnauth := SyncIssues(ctx, unauth, []IssueSpec{{Title: "Test"}})
	if errUnauth == nil {
		t.Fatalf("expected error for unauthenticated forge, got nil")
	}
}

func TestSyncIssues_Negative_EmptyTitleAndCancelledContext(t *testing.T) {
	ctx := context.Background()
	gh := NewGitHubDriver("token", "")

	err := SyncIssues(ctx, gh, []IssueSpec{{Title: ""}})
	if err == nil {
		t.Fatalf("expected error for issue with empty title")
	}

	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	errCanc := SyncIssues(cancCtx, gh, []IssueSpec{{Title: "Valid"}})
	if errCanc == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

func TestValidatePRChecklist_Negative_MissingInvariants(t *testing.T) {
	// Missing 3D test check and receipt
	partialBody := `
## Summary
Incomplete PR.

## Checklist
- [x] **HISS-16 (Context Integrity)**: Checked.
- [ ] **3D Test Discipline (HISS-15)**: Not done yet.
`
	res, err := ValidatePRChecklist(partialBody)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected checklist validation to fail on missing 3D tests and receipt")
	}
	if len(res.Errors) < 2 {
		t.Fatalf("expected at least 2 errors, got: %v", res.Errors)
	}
}

func TestAnalyzeCommit_Negative_BreakingWithoutMigration(t *testing.T) {
	breakingNoMigration := "feat(protocol)!: breaking protocol payload change without footer"
	res, err := AnalyzeCommit(breakingNoMigration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected breaking commit without migration footer to be invalid")
	}
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "HISS-14 violation") {
		t.Fatalf("expected HISS-14 violation error, got: %v", res.Errors)
	}
}

func TestTranscribeDiscussionToADR_Negative_UnapprovedAndEmpty(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	unapproved := Discussion{
		ID:           1,
		Title:        "Proposal",
		Status:       "open",
		ContextText:  "Some context",
		DecisionText: "Some decision",
	}
	_, err := TranscribeDiscussionToADR(ctx, unapproved, tempDir)
	if err == nil {
		t.Fatalf("expected error transcribing unapproved discussion")
	}

	emptyTitle := Discussion{
		ID:           2,
		Title:        "",
		Status:       "approved",
		ContextText:  "Some context",
		DecisionText: "Some decision",
	}
	_, errTitle := TranscribeDiscussionToADR(ctx, emptyTitle, tempDir)
	if errTitle == nil {
		t.Fatalf("expected error on empty discussion title")
	}
}

func TestGenerateWiki_Negative_EmptyOutputDirAndCancelledContext(t *testing.T) {
	ctx := context.Background()
	_, err := GenerateWiki(ctx, "repo", "")
	if err == nil {
		t.Fatalf("expected error on empty output directory")
	}

	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, errCanc := GenerateWiki(cancCtx, "repo", t.TempDir())
	if errCanc == nil {
		t.Fatalf("expected error on cancelled context")
	}
}

// ============================================================================
// 3. BOUNDARY TESTS (3D Dimension 3)
// ============================================================================

func TestIssueDependencyParsing_Boundary_EmptyAndNoMatch(t *testing.T) {
	refs1 := ParseIssueDependencies("")
	if len(refs1) != 0 {
		t.Fatalf("expected 0 refs for empty body, got %d", len(refs1))
	}

	refs2 := ParseIssueDependencies("Regular body text without any depends-on markers.")
	if len(refs2) != 0 {
		t.Fatalf("expected 0 refs for plain text, got %d", len(refs2))
	}
}

func TestValidatePRChecklist_Boundary_EmptyBody(t *testing.T) {
	_, err := ValidatePRChecklist("")
	if err == nil {
		t.Fatalf("expected error for empty PR body")
	}
}

func TestAnalyzeCommit_Boundary_EmptyMessage(t *testing.T) {
	_, err := AnalyzeCommit("")
	if err == nil {
		t.Fatalf("expected error for empty commit message")
	}
}

func TestTranscribeDiscussionToADR_Boundary_IncrementalNumbering(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Seed directory with 0001-first.md and 0002-second.md
	if err := os.WriteFile(filepath.Join(tempDir, "0001-first.md"), []byte("first"), 0644); err != nil {
		t.Fatalf("failed to seed 0001: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "0002-second.md"), []byte("second"), 0644); err != nil {
		t.Fatalf("failed to seed 0002: %v", err)
	}

	disc := Discussion{
		ID:           3,
		Title:        "Third Architectural Choice",
		Status:       "Accepted",
		ContextText:  "Valid context",
		DecisionText: "Valid decision",
	}

	adr, err := TranscribeDiscussionToADR(ctx, disc, tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adr.Number != 3 {
		t.Fatalf("expected next ADR number 3, got %d", adr.Number)
	}
	if !strings.HasSuffix(adr.FilePath, "0003-third-architectural-choice.md") {
		t.Fatalf("unexpected file path: %s", adr.FilePath)
	}
}

func TestAssignReviewers_Boundary_EmptyCodeowners(t *testing.T) {
	assignment, err := AssignReviewers([]string{"cmd/standardsctl/main.go"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assignment.HumanReviewers) != 0 {
		t.Fatalf("expected 0 human reviewers for empty codeowners, got %d", len(assignment.HumanReviewers))
	}
	if len(assignment.BotReviewers) != 1 || assignment.BotReviewers[0] != StandardReviewBot {
		t.Fatalf("expected bot reviewer to still be assigned")
	}
}
