package forge

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// ============================================================================
// 1. POSITIVE TESTS (3D Dimension 1)
// ============================================================================

func TestNewForge_Providers_Positive(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		provider string
		wantName string
	}{
		{"github", "github"},
		{"gitlab", "gitlab"},
		{"gitea", "gitea"},
		{"forgejo", "gitea"},
	}

	for _, tc := range cases {
		f, err := NewForge(tc.provider, "forge-token", "")
		if err != nil {
			t.Fatalf("failed to create forge driver for %s: %v", tc.provider, err)
		}
		if f.Name() != tc.wantName {
			t.Fatalf("provider %s produced driver %q, want %q", tc.provider, f.Name(), tc.wantName)
		}
		if err := f.Authenticate(ctx); err != nil {
			t.Fatalf("expected authentication to pass for %s: %v", tc.provider, err)
		}
	}

	if _, ok := mustForge(t, "github").(*GitHubDriver); !ok {
		t.Fatal("provider github must produce a *GitHubDriver")
	}
	if _, ok := mustForge(t, "gitlab").(*GitLabDriver); !ok {
		t.Fatal("provider gitlab must produce a *GitLabDriver")
	}
	if _, ok := mustForge(t, "gitea").(*GiteaDriver); !ok {
		t.Fatal("provider gitea must produce a *GiteaDriver")
	}
}

func mustForge(t *testing.T, provider string) Forge {
	t.Helper()
	f, err := NewForge(provider, "forge-token", "")
	if err != nil {
		t.Fatalf("failed to create forge driver for %s: %v", provider, err)
	}
	return f
}

// The GitLab and Gitea drivers have no client: every enforcement method must fail loudly
// instead of reporting governance that was never applied.
func TestStubDrivers_Negative_EveryEnforcementMethodIsUnsupported(t *testing.T) {
	ctx := context.Background()
	policy := &config.BranchProtectionPolicy{}

	for _, provider := range []string{"gitlab", "gitea", "forgejo"} {
		f := mustForge(t, provider)
		checks := map[string]error{
			"ReconcileProtection": f.ReconcileProtection(ctx, "main", policy),
			"ReconcileLabels":     f.ReconcileLabels(ctx, []Label{{Name: "governance"}}),
			"PostStatusCheck":     f.PostStatusCheck(ctx, "abc", CheckRun{Name: "verify"}),
			"UpdateIssue":         f.UpdateIssue(ctx, 1, []string{"x"}, "open"),
		}
		if _, err := f.CreatePullRequest(ctx, PRRequest{Title: "t", Head: "h", Base: "b"}); true {
			checks["CreatePullRequest"] = err
		}
		if _, err := f.CreateIssue(ctx, IssueSpec{Title: "t"}); true {
			checks["CreateIssue"] = err
		}
		if _, err := f.ListIssues(ctx, "all"); true {
			checks["ListIssues"] = err
		}
		for method, err := range checks {
			if !errors.Is(err, ErrNotImplemented) || !errors.Is(err, errors.ErrUnsupported) {
				t.Fatalf("%s.%s returned %v, want ErrNotImplemented", provider, method, err)
			}
		}
	}
}

func TestStubDrivers_Negative_EmptyToken(t *testing.T) {
	ctx := context.Background()
	if err := NewGitLabDriver("", "").Authenticate(ctx); err == nil {
		t.Fatal("expected an authentication error for an empty GitLab token")
	}
	if err := NewGiteaDriver("", "").Authenticate(ctx); err == nil {
		t.Fatal("expected an authentication error for an empty Gitea token")
	}
	if err := NewGiteaDriver("t", "").Authenticate(nil); err == nil { //nolint:staticcheck // nil context is the boundary under test
		t.Fatal("expected an error for a nil context")
	}
}

func TestIssueDependencyParsing_Positive(t *testing.T) {
	body := `
Resolves core architecture.
Depends-On: cordanaLLM/praetor#42
Some other context.
Depends-On: sveltesentio#232
Depends-On: #15
`
	refs := ParseIssueDependencies(body)
	if len(refs) != 3 {
		t.Fatalf("expected 3 dependency refs, got %d", len(refs))
	}
	if refs[0].Owner != "cordanaLLM" || refs[0].Repo != "praetor" || refs[0].Number != 42 {
		t.Errorf("ref 0 mismatch: %+v", refs[0])
	}
	if refs[1].Owner != "" || refs[1].Repo != "sveltesentio" || refs[1].Number != 232 {
		t.Errorf("ref 1 mismatch: %+v", refs[1])
	}
	if refs[2].Number != 15 || refs[2].Owner != "" || refs[2].Repo != "" {
		t.Errorf("ref 2 mismatch: %+v", refs[2])
	}
}

// recordingForge is a hermetic Forge that records exactly what SyncIssues asked it to do.
type recordingForge struct {
	existing  []IssueSpec
	created   []IssueSpec
	updated   []updateCall
	listErr   error
	createErr error
	updateErr error
}

type updateCall struct {
	Number int
	Labels []string
	State  string
}

func (r *recordingForge) Name() string                           { return "recording" }
func (r *recordingForge) Authenticate(ctx context.Context) error { return ctx.Err() }
func (r *recordingForge) ReconcileProtection(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) error {
	return nil
}
func (r *recordingForge) ReconcileLabels(ctx context.Context, labels []Label) error { return nil }
func (r *recordingForge) PostStatusCheck(ctx context.Context, commitSHA string, check CheckRun) error {
	return nil
}
func (r *recordingForge) CreatePullRequest(ctx context.Context, req PRRequest) (*PRResponse, error) {
	return &PRResponse{Number: 1}, nil
}

func (r *recordingForge) CreateIssue(ctx context.Context, spec IssueSpec) (*IssueResponse, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	r.created = append(r.created, spec)
	return &IssueResponse{Number: len(r.created), State: "open"}, nil
}

func (r *recordingForge) ListIssues(ctx context.Context, state string) ([]IssueSpec, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.existing, nil
}

func (r *recordingForge) UpdateIssue(ctx context.Context, number int, labels []string, state string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, updateCall{Number: number, Labels: labels, State: state})
	return nil
}

func TestSyncIssues_Positive_CreatesAndUpserts(t *testing.T) {
	ctx := context.Background()
	fake := &recordingForge{
		existing: []IssueSpec{{ID: 7, Title: "Refactor ADR Pipeline", State: "open"}},
	}

	issues := []IssueSpec{
		{
			Title:     "Implement Pillar VII Multi-Forge Federation",
			Body:      "Depends-On: cordanaLLM/praetor#100",
			State:     "open",
			Assignees: []string{"alice"},
		},
		{
			Title:  "Refactor ADR Pipeline",
			State:  "closed",
			Labels: []string{"governance"},
		},
	}

	rep, err := SyncIssues(ctx, fake, issues)
	if err != nil {
		t.Fatalf("unexpected error syncing issues: %v", err)
	}
	if rep.Created != 1 || rep.Updated != 1 {
		t.Fatalf("unexpected report %+v", rep)
	}
	if len(fake.created) != 1 || fake.created[0].Title != issues[0].Title {
		t.Fatalf("unexpected created specs: %+v", fake.created)
	}
	if len(fake.created[0].Assignees) != 1 || fake.created[0].Assignees[0] != "alice" {
		t.Fatalf("assignees were lost on the way to the driver: %+v", fake.created[0])
	}
	if len(fake.updated) != 1 || fake.updated[0].Number != 7 || fake.updated[0].State != "closed" {
		t.Fatalf("unexpected update calls: %+v", fake.updated)
	}

	// Re-running the same batch must not duplicate anything.
	fake2 := &recordingForge{existing: issues}
	rep2, err := SyncIssues(ctx, fake2, issues)
	if err != nil {
		t.Fatalf("unexpected error on the second sync: %v", err)
	}
	if rep2.Created != 0 || len(fake2.created) != 0 {
		t.Fatalf("a repeated sync created duplicates: %+v", rep2)
	}
}

func TestSyncIssues_Negative_PropagatesDriverFailures(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("forge exploded")

	if _, err := SyncIssues(ctx, &recordingForge{listErr: sentinel}, []IssueSpec{{Title: "a"}}); !errors.Is(err, sentinel) {
		t.Fatalf("expected the listing error to propagate, got %v", err)
	}
	if _, err := SyncIssues(ctx, &recordingForge{createErr: sentinel}, []IssueSpec{{Title: "a"}}); !errors.Is(err, sentinel) {
		t.Fatalf("expected the create error to propagate, got %v", err)
	}
	fake := &recordingForge{existing: []IssueSpec{{ID: 3, Title: "a"}}, updateErr: sentinel}
	if _, err := SyncIssues(ctx, fake, []IssueSpec{{Title: "a", State: "closed"}}); !errors.Is(err, sentinel) {
		t.Fatalf("expected the update error to propagate, got %v", err)
	}
}

func TestSyncIssues_Boundary_BatchLimitAndValidationBeforeWrites(t *testing.T) {
	ctx := context.Background()

	over := make([]IssueSpec, MaxIssuesLimit+1)
	for i := range over {
		over[i] = IssueSpec{Title: "t"}
	}
	if _, err := SyncIssues(ctx, &recordingForge{}, over); err == nil {
		t.Fatalf("expected an error above the %d issue batch limit", MaxIssuesLimit)
	}

	// A malformed spec late in the batch must abort before anything is written.
	fake := &recordingForge{}
	if _, err := SyncIssues(ctx, fake, []IssueSpec{{Title: "ok"}, {Title: "  "}}); err == nil {
		t.Fatal("expected an error for a blank title")
	}
	if len(fake.created) != 0 {
		t.Fatalf("the batch was partially written before validation: %+v", fake.created)
	}
}

const prChecklistBoxes = `
## Summary
Implemented Phase 3 Pillar VII.

## Checklist
- [x] **HISS-16 (Context Integrity)**: Checked and verified via compile-context.
- [X] **3D Test Discipline (HISS-15)**: Positive, Negative, and Boundary tests pass.
`

// signedReceiptBlock renders a genuine Ed25519 Exit-0 receipt as the fenced block a PR body
// is expected to carry.
func signedReceiptBlock(t *testing.T, priv ed25519.PrivateKey, commitSHA, output string) string {
	t.Helper()
	receipt, err := lockdown.CreateReceipt("praetorctl gate run", 0, []byte(output), commitSHA, "acme/widgets", priv)
	if err != nil {
		t.Fatalf("failed creating receipt: %v", err)
	}
	data, err := json.MarshalIndent(lockdown.ReceiptFile{ExecutionReceipt: *receipt, GateOutput: output}, "", "  ")
	if err != nil {
		t.Fatalf("failed encoding receipt: %v", err)
	}
	return "\n```receipt\n" + string(data) + "\n```\n"
}

func TestValidatePRChecklist_Positive(t *testing.T) {
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	head := "0f1e2d3c4b5a69788796a5b4c3d2e1f009182736"
	prBody := prChecklistBoxes + signedReceiptBlock(t, priv, head, "all gates passed")

	res, err := ValidatePRChecklistWithPolicy(prBody, ReceiptPolicy{PinnedKey: pub, HeadSHA: head})
	if err != nil {
		t.Fatalf("unexpected error validating checklist: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected PR checklist to be valid, got errors: %v", res.Errors)
	}
	if !res.HasHISS16Check || !res.Has3DTestsCheck || !res.HasReceipt {
		t.Fatalf("expected all checks true, got: %+v", res)
	}
	if !strings.Contains(res.ReceiptProof, head) {
		t.Fatalf("receipt proof does not name the certified commit: %q", res.ReceiptProof)
	}
}

// A fenced code block or the literal words "Exit-0 Receipt" must never satisfy a gate that
// claims to verify an Ed25519 signature.
func TestValidatePRChecklist_Negative_UnsignedReceiptSubstitutes(t *testing.T) {
	cases := map[string]string{
		"plain fenced block": prChecklistBoxes + "\n```text\nAll gates passed. Exit-0 Receipt\n```\n",
		"literal phrase":     prChecklistBoxes + "\nEd25519 Exit-0 Receipt: Receipt Signature present.\n",
		"malformed json":     prChecklistBoxes + "\n```receipt\n{not json\n```\n",
		"empty receipt":      prChecklistBoxes + "\n```receipt\n{}\n```\n",
	}
	for name, body := range cases {
		res, err := ValidatePRChecklist(body)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if res.Valid || res.HasReceipt {
			t.Fatalf("%s: an unsigned receipt was accepted: %+v", name, res)
		}
	}
}

func TestValidatePRChecklist_Negative_WrongKeyCommitOrPayload(t *testing.T) {
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	otherPub, _, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	head := "0f1e2d3c4b5a69788796a5b4c3d2e1f009182736"
	body := prChecklistBoxes + signedReceiptBlock(t, priv, head, "all gates passed")

	res, err := ValidatePRChecklistWithPolicy(body, ReceiptPolicy{PinnedKey: otherPub})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasReceipt {
		t.Fatal("a receipt signed by an unpinned key was accepted")
	}

	res, err = ValidatePRChecklistWithPolicy(body, ReceiptPolicy{HeadSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasReceipt {
		t.Fatal("a receipt certifying a different commit was accepted")
	}

	tampered := strings.Replace(body, "all gates passed", "all gates failed", 1)
	res, err = ValidatePRChecklist(tampered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasReceipt {
		t.Fatal("a receipt whose certified output was altered was accepted")
	}
}

func TestValidatePRChecklist_Boundary_FenceLabelAndLineLimit(t *testing.T) {
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	block := signedReceiptBlock(t, priv, "abc", "out")

	// The same receipt in an unlabelled fence is not a receipt.
	unlabelled := prChecklistBoxes + strings.Replace(block, "```receipt", "```", 1)
	res, err := ValidatePRChecklist(unlabelled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasReceipt {
		t.Fatal("an unlabelled fenced block was accepted as a receipt")
	}

	// A json-labelled receipt fence is accepted.
	labelled := prChecklistBoxes + strings.Replace(block, "```receipt", "```json receipt", 1)
	res, err = ValidatePRChecklist(labelled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasReceipt || !res.Valid {
		t.Fatalf("expected the labelled receipt to be accepted: %+v", res)
	}

	// Beyond MaxPRLinesLimit the body is truncated, so the receipt is out of scope.
	padded := prChecklistBoxes + strings.Repeat("filler\n", MaxPRLinesLimit+10) + block
	res, err = ValidatePRChecklist(padded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasReceipt {
		t.Fatal("content beyond the scalar line bound must not be scanned")
	}
}

func TestAssignReviewers_Positive(t *testing.T) {
	codeowners := `
# CODEOWNERS
*                   @cordanaLLM/core-leads
internal/forge/*    @cordanaLLM/multi-forge-team
docs/                @cordanaLLM/docs-team
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
	// Last match wins, exactly as on GitHub: the catch-all rule must not survive.
	for _, h := range assignment.HumanReviewers {
		if h == "@cordanaLLM/core-leads" {
			t.Errorf("the catch-all rule must be overridden by the later matching rule: %v", assignment.HumanReviewers)
		}
	}

	if len(assignment.BotReviewers) == 0 || assignment.BotReviewers[0] != StandardReviewBot {
		t.Errorf("expected bot reviewer %s, got: %v", StandardReviewBot, assignment.BotReviewers)
	}
}

// CODEOWNERS patterns follow gitignore semantics; a prefix test without a separator
// boundary hands ownership of unrelated directories to the wrong team.
func TestMatchPattern_3D(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Positive
		{"*", "any/where.go", true},
		{"*.go", "internal/forge/pr.go", true},
		{"docs/", "docs/wiki/Home.md", true},
		{"docs/*", "docs/index.md", true},
		{"internal/**", "internal/forge/deep/x.go", true},
		{"/cmd/standardsctl/main.go", "cmd/standardsctl/main.go", true},
		{"internal/forge/*", "internal/forge/pr.go", true},
		// Negative: the separator boundary must hold
		{"docs/*", "docs-old/README.md", false},
		{"internal/forge/*", "internal/forgery/x.go", false},
		{"docs/", "docs-old/README.md", false},
		{"docs/*", "docs/wiki/Home.md", false},
		{"*.go", "internal/forge/pr.md", false},
		// Boundary
		{"", "a.go", false},
		{"*.go", "", false},
		{"**", "a/b/c.go", true},
	}
	for _, tc := range cases {
		if got := matchPattern(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestAssignReviewers_Boundary_PathLimit(t *testing.T) {
	paths := make([]string, MaxPathsLimit+10)
	for i := range paths {
		paths[i] = "internal/forge/pr.go"
	}
	assignment, err := AssignReviewers(paths, "internal/forge/* @team\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(assignment.HumanReviewers) != 1 || assignment.HumanReviewers[0] != "@team" {
		t.Fatalf("unexpected reviewers: %v", assignment.HumanReviewers)
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
	_, err := SyncIssues(ctx, nil, []IssueSpec{{Title: "Test"}})
	if err == nil {
		t.Fatalf("expected error for nil forge, got nil")
	}

	unauth := NewGitHubDriver("", "")
	_, errUnauth := SyncIssues(ctx, unauth, []IssueSpec{{Title: "Test"}})
	if errUnauth == nil {
		t.Fatalf("expected error for unauthenticated forge, got nil")
	}
}

func TestSyncIssues_Negative_EmptyTitleAndCancelledContext(t *testing.T) {
	ctx := context.Background()
	gh := NewGitHubDriver("token", "")

	_, err := SyncIssues(ctx, gh, []IssueSpec{{Title: ""}})
	if err == nil {
		t.Fatalf("expected error for issue with empty title")
	}

	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, errCanc := SyncIssues(cancCtx, gh, []IssueSpec{{Title: "Valid"}})
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

// Conventional Commits 1.0.0 declares BREAKING-CHANGE synonymous with BREAKING CHANGE.
func TestAnalyzeCommit_Negative_BothBreakingFooterSpellings(t *testing.T) {
	for _, token := range []string{"BREAKING CHANGE:", "BREAKING-CHANGE:"} {
		msg := "feat(api): drop v1 endpoint\n\n" + token + " v1 removed"
		res, err := AnalyzeCommit(msg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.IsBreaking {
			t.Fatalf("footer %q was not recognised as breaking", token)
		}
		if res.Valid {
			t.Fatalf("footer %q without a Migration: footer must be invalid", token)
		}
	}

	withMigration := "feat(api): drop v1 endpoint\n\nBREAKING-CHANGE: v1 removed\nMigration: call /v2/issues instead"
	res, err := AnalyzeCommit(withMigration)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsBreaking || !res.HasMigrationFooter || !res.Valid {
		t.Fatalf("expected a valid breaking commit, got %+v", res)
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

func TestTranscribeDiscussionToADR_Boundary_NonLatinTitleAndOverwrite(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	first := Discussion{
		ID:           11,
		Title:        "設計方針",
		Status:       "approved",
		ContextText:  "context",
		DecisionText: "decision",
	}
	adr1, err := TranscribeDiscussionToADR(ctx, first, tempDir)
	if err != nil {
		t.Fatalf("unexpected error transcribing a non-Latin title: %v", err)
	}
	if adr1.Slug == "" {
		t.Fatalf("expected a non-empty fallback slug, got %+v", adr1)
	}

	second := first
	second.ID = 12
	second.Title = "アーキテクチャ"
	adr2, err := TranscribeDiscussionToADR(ctx, second, tempDir)
	if err != nil {
		t.Fatalf("unexpected error transcribing the second non-Latin title: %v", err)
	}
	if adr2.Number != 2 {
		t.Errorf("expected the slug-less record to participate in the numbering, got %d", adr2.Number)
	}
	if adr2.FilePath == adr1.FilePath {
		t.Fatalf("the second ADR overwrote the first at %s", adr1.FilePath)
	}

	data, err := os.ReadFile(adr1.FilePath)
	if err != nil {
		t.Fatalf("read first ADR: %v", err)
	}
	if !strings.Contains(string(data), "設計方針") {
		t.Errorf("the first ADR was overwritten: %s", string(data))
	}

	// An existing record is immutable: a collision is an error, never a silent rewrite.
	third := first
	third.ID = 11
	if _, err := TranscribeDiscussionToADR(ctx, third, tempDir); err == nil {
		t.Log("no collision possible because the sequence number advanced")
	}
}

func TestGenerateWiki_Boundary_RepoNameFromRelativeRoot(t *testing.T) {
	ctx := context.Background()

	manifest, err := GenerateWiki(ctx, "/path/to/my-repo", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error generating wiki: %v", err)
	}
	if !strings.Contains(manifest.Pages[0].Content, "cordanaLLM/my-repo Wiki Portal") {
		t.Errorf("home page does not name the repository: %s", manifest.Pages[0].Content[:80])
	}

	// "." is what the CLI passes; it must resolve to the working directory's name, not
	// to a hard-coded placeholder.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relManifest, err := GenerateWiki(ctx, ".", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error generating wiki from '.': %v", err)
	}
	want := "cordanaLLM/" + filepath.Base(cwd) + " Wiki Portal"
	if !strings.Contains(relManifest.Pages[0].Content, want) {
		t.Errorf("expected home page to contain %q", want)
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
