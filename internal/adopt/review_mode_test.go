package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// reviewSession returns a session for a github.com origin acme/solo whose collaborator count is
// writers, or err, and counts how often it was asked.
func reviewSession(writers int, err error, asked *int) *adoptSession {
	return &adoptSession{
		report:   &AdoptReport{},
		identity: repoIdentity{owner: "acme", name: "solo", host: githubHost, path: "acme/solo"},
		collaborators: func(context.Context, string, string) (int, error) {
			*asked++
			return writers, err
		},
	}
}

// Positive (#1075): one writer is a single maintainer; the forge is asked once per run, and the
// overrides adoption writes carry exactly the single-maintainer review mode.
func TestSoloMaintainer_Positive_OneWriter(t *testing.T) {
	asked := 0
	s := reviewSession(1, nil, &asked)
	solo, note := s.soloMaintainer(t.Context())
	again, _ := s.soloMaintainer(t.Context())
	if !solo || !again || asked != 1 || !strings.Contains(note, "review_mode: single_maintainer, because acme/solo has one collaborator") {
		t.Fatalf("solo=%v again=%v asked=%d note=%q", solo, again, asked, note)
	}
	if bp := soloOverrides().BranchProtection; bp == nil || bp.ReviewMode != config.BranchReviewModeSingleMaintainer || bp.RequiredApprovingReviewers != 0 {
		t.Fatalf("overrides %+v", bp)
	}
}

// Negative: two writers are independent review with no note; a forge that cannot answer, or an
// origin that is not on GitHub, is unknown and warns that approvals may exceed approvers; a run
// without a counter (a library caller, a test) asks nothing and notes nothing.
func TestSoloMaintainer_Negative_SeveralOrUnknown(t *testing.T) {
	asked := 0
	if solo, note := reviewSession(2, nil, &asked).soloMaintainer(t.Context()); solo || note != "" {
		t.Fatalf("two writers: solo=%v note=%q", solo, note)
	}
	if solo, note := reviewSession(0, errNoForgeToken, &asked).soloMaintainer(t.Context()); solo || !strings.Contains(note, "approvals may exceed approvers") {
		t.Fatalf("no token: solo=%v note=%q", solo, note)
	}
	gitlab := reviewSession(1, nil, &asked)
	gitlab.identity.host = "gitlab.com"
	before := asked
	if solo, note := gitlab.soloMaintainer(t.Context()); solo || asked != before || !strings.Contains(note, "GitHub origin only") {
		t.Fatalf("gitlab origin: solo=%v asked=%d note=%q", solo, asked-before, note)
	}
	unresolved := reviewSession(1, nil, &asked)
	unresolved.identity = repoIdentity{}
	before = asked
	if solo, note := unresolved.soloMaintainer(t.Context()); solo || asked != before || !strings.Contains(note, "identity is unresolved") {
		t.Fatalf("unresolved identity: solo=%v asked=%d note=%q", solo, asked-before, note)
	}
	unasked := reviewSession(1, nil, &asked)
	unasked.collaborators = nil
	if solo, note := unasked.soloMaintainer(t.Context()); solo || note != "" {
		t.Fatalf("no counter must leave the review mode unexamined: solo=%v note=%q", solo, note)
	}
}

// Boundary: an existing manifest of a one-writer repository is warned about unless it already
// declares the single-maintainer review mode; an unknown count warns with the note; no writer at
// all (zero, a forge answer) is not a single maintainer.
func TestWarnUnsatisfiableReview_Boundary(t *testing.T) {
	asked := 0
	s := reviewSession(1, nil, &asked)
	s.warnUnsatisfiableReview(t.Context(), &config.Manifest{})
	if !hasWarningContaining(s.report, "declare overrides.branch_protection.review_mode: single_maintainer") {
		t.Fatalf("warnings %v", s.report.Warnings)
	}
	declared := &config.Manifest{Overrides: soloOverrides()}
	s = reviewSession(1, nil, &asked)
	if s.warnUnsatisfiableReview(t.Context(), declared); len(s.report.Warnings) != 0 {
		t.Fatalf("declared single maintainer warned: %v", s.report.Warnings)
	}
	s = reviewSession(0, errors.New("403"), &asked)
	if s.warnUnsatisfiableReview(t.Context(), &config.Manifest{}); !hasWarningContaining(s.report, "could not count the collaborators") {
		t.Fatalf("unknown count not warned: %v", s.report.Warnings)
	}
	if solo, _ := reviewSession(0, nil, &asked).soloMaintainer(t.Context()); solo {
		t.Fatal("zero writers counted as a single maintainer")
	}
}

// TestAdopt_SoloReviewMode_3D (#1075): a first adoption of a GitHub repository with one writer
// writes the single-maintainer review mode into the .standards.yaml it creates and says so
// (positive); two writers get no overrides (negative); a re-adoption never rewrites the manifest
// and only warns (boundary).
func TestAdopt_SoloReviewMode_3D(t *testing.T) {
	requireGit(t)
	for _, tc := range []struct {
		name    string
		writers int
		solo    bool
	}{{"one writer", 1, true}, {"two writers", 2, false}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "dev", "solo")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			initTestGit(t, repo)
			mustWrite(t, filepath.Join(repo, readmeFile), "# Solo\n")
			writeOriginRemote(t, repo, "git@github.com:acme/solo.git")
			source := newAdoptLockSource(t)
			writeCheckpointBundle(t, source)
			asked := 0
			opts := AdoptOptions{Path: repo, SkipGitValidation: true, SkipHookActivation: true, LockSourceRoot: source,
				Collaborators: func(_ context.Context, owner, name string) (int, error) {
					asked++
					if owner != "acme" || name != "solo" {
						t.Errorf("counted %s/%s, want acme/solo", owner, name)
					}
					return tc.writers, nil
				}}
			report, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatalf("adoption failed: %v", err)
			}
			manifest, err := config.LoadManifest(filepath.Join(repo, manifestFile))
			if err != nil {
				t.Fatal(err)
			}
			bp := manifest.Overrides.BranchProtection
			declared := bp != nil && bp.ReviewMode == config.BranchReviewModeSingleMaintainer
			noted := strings.Contains(findActionDetail(report.ActionDetails, manifestFile), "one collaborator with write access")
			if declared != tc.solo || noted != tc.solo || asked != 1 {
				t.Fatalf("declared=%v noted=%v asked=%d, want solo=%v asked=1\n%s", declared, noted, asked, tc.solo,
					mustRead(t, filepath.Join(repo, manifestFile)))
			}
			if !tc.solo {
				return
			}
			// Re-adopt a manifest from which the operator removed the review mode: warned, not rewritten.
			stripped := strings.Replace(mustRead(t, filepath.Join(repo, manifestFile)), "review_mode: single_maintainer", "review_mode: independent", 1)
			mustWrite(t, filepath.Join(repo, manifestFile), stripped)
			again, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatalf("re-adoption failed: %v", err)
			}
			if mustRead(t, filepath.Join(repo, manifestFile)) != stripped || !hasWarningContaining(again, "declare overrides.branch_protection.review_mode: single_maintainer") {
				t.Fatalf("re-adoption rewrote the manifest or did not warn: %v", again.Warnings)
			}
		})
	}
}
