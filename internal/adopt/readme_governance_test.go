package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	legacyPraetorBadge        = "[![HISS lattice](https://img.shields.io/badge/Standards-Praetor%20HISS%20lattice-brightgreen)](AGENTS.md)"
	testReadmeGovernanceStart = "<!-- praetor:readme-governance:start -->"
	testReadmeGovernanceEnd   = "<!-- praetor:readme-governance:end -->"
)

func TestAdoptReadmeGovernanceNeverCertifiesAnUnverifiedRepository(t *testing.T) {
	repoPath := newTestRepo(t, "readme-pending")
	initial := "# Demo\n\n" + legacyPraetorBadge + "\n\nHuman introduction.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), initial)

	report, err := Adopt(t.Context(), AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t),
		Path:           repoPath,
		Profile:        "framework",
		RecordBaseline: false,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	assertNoIssues(t, report)
	content := mustRead(t, filepath.Join(repoPath, readmeFile))
	for _, falseClaim := range []string{"HISS%20Compliant", "This repository conforms"} {
		if strings.Contains(content, falseClaim) {
			t.Fatalf("unverified adoption published %q:\n%s", falseClaim, content)
		}
	}
	if strings.Count(content, legacyPraetorBadge) != 1 {
		t.Fatalf("custom HISS badge was removed or duplicated:\n%s", content)
	}
	for _, marker := range []string{testReadmeGovernanceStart, testReadmeGovernanceEnd} {
		if strings.Count(content, marker) != 1 {
			t.Fatalf("managed governance marker %q missing or duplicated:\n%s", marker, content)
		}
	}
	if !strings.Contains(content, "not a verification certificate") {
		t.Fatalf("managed block does not explain its evidence boundary:\n%s", content)
	}
}

func TestAdoptReadmeGovernanceRefreshesManagedStateIdempotently(t *testing.T) {
	repoPath := newTestRepo(t, "readme-refresh")
	readme := "# Legacy\n\n" + testReadmeGovernanceStart + "\nstale generated claim\n" +
		testReadmeGovernanceEnd + "\n\nHuman tail.\n"
	mustWrite(t, filepath.Join(repoPath, readmeFile), readme)
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = fail()\n}\nfunc fail() error { return nil }\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true}

	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	first := mustRead(t, filepath.Join(repoPath, readmeFile))
	if strings.Contains(first, "stale generated claim") || !strings.Contains(first, "1%20baselined") {
		t.Fatalf("managed block was not refreshed from the recorded baseline:\n%s", first)
	}
	if !strings.Contains(first, "Human tail.") {
		t.Fatalf("human README content was not preserved:\n%s", first)
	}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	if second := mustRead(t, filepath.Join(repoPath, readmeFile)); second != first {
		t.Fatalf("README reconciliation is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestAdoptReadmeGovernanceRejectsMalformedMarkersWithoutChangingReadme(t *testing.T) {
	repoPath := newTestRepo(t, "readme-malformed")
	initial := "# Demo\n\n" + testReadmeGovernanceStart + "\nunterminated\n\nHuman tail.\n"
	path := filepath.Join(repoPath, readmeFile)
	mustWrite(t, path, initial)

	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed managed markers must fail explicitly, got %v", err)
	}
	if got := mustRead(t, path); got != initial {
		t.Fatalf("failed reconciliation changed README:\n%s", got)
	}
}

// previousRelativeBlock is a README block an earlier Praetor rendered: its HISS badge linked
// AGENTS.md by a repository-relative path, which a strict MkDocs portal including the README
// cannot resolve (#506).
const previousRelativeBlock = testReadmeGovernanceStart + "\n" +
	"[![HISS Adopted][praetor-hiss-badge]](AGENTS.md)\n\n" +
	"[praetor-hiss-badge]: https://img.shields.io/badge/Standards-HISS%20Adopted%20(baseline%20pending)-yellow\n" +
	testReadmeGovernanceEnd

// Positive: plain adoption, without --force, refreshes the previous block, keeps the human
// text and converges on the rerun. With the documentation gate the badge links the
// repository's AGENTS.md on the default branch by absolute URL; without it the badge renders
// unlinked, since the gate's workflow is the only evidence that the repository is on GitHub.
func TestAdoptReadmeGovernanceRefreshesRelativeAgentsLink(t *testing.T) {
	const linked = "]: https://github.com/acme/readme-agents-link/blob/HEAD/AGENTS.md\n"
	for name, tc := range map[string]struct {
		facets []string
		badge  string
	}{
		"documentation":    {nil, "[![HISS Adopted][praetor-hiss-badge]][praetor-hiss-agents]\n"},
		"no documentation": {[]string{"security:high"}, "\n![HISS Adopted][praetor-hiss-badge]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "readme-agents-link")
			path := filepath.Join(repoPath, readmeFile)
			mustWrite(t, path, "# Demo\n\n"+previousRelativeBlock+"\n\nHuman tail.\n")
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Facets: tc.facets}
			report, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatalf("adopt: %v", err)
			}
			first := mustRead(t, path)
			if strings.Contains(first, "](AGENTS.md)") || !strings.Contains(first, "Human tail.") ||
				!strings.Contains(first, tc.badge) || strings.Contains(first, linked) != (tc.facets == nil) {
				t.Fatalf("previous block not refreshed to %q:\n%s", tc.badge, first)
			}
			if detail := findActionDetail(report.ActionDetails, readmeFile); !strings.Contains(detail, "Reconciled the marker-owned") {
				t.Fatalf("README refresh not reported: %q", detail)
			}
			if _, err := Adopt(t.Context(), opts); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			if second := mustRead(t, path); second != first {
				t.Fatalf("rerun changed the refreshed README:\n%s", second)
			}
		})
	}
}

// ReadmeIdentity in three dimensions. Positive: the documentation gate returns the full
// manifest identity. Boundary: without the gate no identity is returned, whatever the
// manifest names, so the HISS badge renders unlinked. Negative: the gate without an identity,
// or with half of one, is errReadmeIdentityUnset.
func TestReadmeIdentity(t *testing.T) {
	full := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"}}
	if owner, name, err := ReadmeIdentity(full, true); err != nil || owner != "acme" || name != "widgets" {
		t.Fatalf("documentation gate: %q/%q %v", owner, name, err)
	}
	half := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme"}}
	for _, manifest := range []*config.Manifest{nil, {}, half, full} {
		if owner, name, err := ReadmeIdentity(manifest, false); err != nil || owner != "" || name != "" {
			t.Fatalf("no documentation gate, manifest %+v: %q/%q %v", manifest, owner, name, err)
		}
	}
	for _, manifest := range []*config.Manifest{nil, {}, half, {Repository: config.RepositoryMetadata{Name: "widgets"}}} {
		if _, _, err := ReadmeIdentity(manifest, true); !errors.Is(err, errReadmeIdentityUnset) {
			t.Fatalf("documentation gate, manifest %+v: %v, want errReadmeIdentityUnset", manifest, err)
		}
	}
}

// Negative (review of #506): a repository whose origin remote is on another forge records
// that forge's owner/name in its manifest, and only the documentation gate's GitHub Actions
// workflow establishes that the repository lives on GitHub. Without the gate the HISS badge
// renders unlinked rather than point at github.com, where the same owner/name may name an
// unrelated repository.
func TestAdoptReadmeGovernanceNeverLinksGitHubForAnotherForge(t *testing.T) {
	repoPath := newTestRepo(t, "readme-other-forge")
	writeOriginRemote(t, repoPath, "https://gitlab.com/acme/widgets.git")
	path := filepath.Join(repoPath, readmeFile)
	mustWrite(t, path, "# Demo\n\n"+previousRelativeBlock+"\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Facets: []string{"security:high"}}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	got := mustRead(t, path)
	if strings.Contains(got, "github.com") || strings.Contains(got, "](AGENTS.md)") ||
		!strings.Contains(got, "\n![HISS Adopted][praetor-hiss-badge]\n") {
		t.Fatalf("badge of a non-GitHub repository must render unlinked:\n%s", got)
	}
}
