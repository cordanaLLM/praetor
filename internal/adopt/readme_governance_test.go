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

// Positive: plain adoption, without --force, refreshes the previous block to one linking the
// repository's AGENTS.md on the default branch by absolute URL, with and without the
// documentation gate, keeps the human text, and converges on the rerun.
func TestAdoptReadmeGovernanceRefreshesRelativeAgentsLink(t *testing.T) {
	for name, facets := range map[string][]string{"documentation": nil, "no documentation": {"security:high"}} {
		t.Run(name, func(t *testing.T) {
			repoPath := newTestRepo(t, "readme-agents-link")
			path := filepath.Join(repoPath, readmeFile)
			mustWrite(t, path, "# Demo\n\n"+previousRelativeBlock+"\n\nHuman tail.\n")
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Facets: facets}
			report, err := Adopt(t.Context(), opts)
			if err != nil {
				t.Fatalf("adopt: %v", err)
			}
			first := mustRead(t, path)
			if strings.Contains(first, "](AGENTS.md)") || !strings.Contains(first, "Human tail.") ||
				!strings.Contains(first, "]: https://github.com/acme/readme-agents-link/blob/HEAD/AGENTS.md\n") {
				t.Fatalf("previous block not refreshed to the absolute AGENTS.md link:\n%s", first)
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

// ReadmeIdentity in three dimensions. Positive: a full manifest identity is returned with the
// documentation gate on or off. Boundary: no manifest or no identity without the gate is an
// unlinked badge, not an error. Negative: the gate without an identity, or half an identity,
// is errReadmeIdentityUnset.
func TestReadmeIdentity(t *testing.T) {
	full := &config.Manifest{Repository: config.RepositoryMetadata{Owner: "acme", Name: "widgets"}}
	for _, docs := range []bool{false, true} {
		if owner, name, err := ReadmeIdentity(full, docs); err != nil || owner != "acme" || name != "widgets" {
			t.Fatalf("docs=%v: %q/%q %v", docs, owner, name, err)
		}
	}
	for _, manifest := range []*config.Manifest{nil, {}} {
		if owner, name, err := ReadmeIdentity(manifest, false); err != nil || owner != "" || name != "" {
			t.Fatalf("identity-free manifest %+v: %q/%q %v", manifest, owner, name, err)
		}
	}
	for _, tc := range []struct {
		identity config.RepositoryMetadata
		docs     bool
	}{
		{config.RepositoryMetadata{}, true},
		{config.RepositoryMetadata{Owner: "acme"}, false},
		{config.RepositoryMetadata{Name: "widgets"}, true},
	} {
		if _, _, err := ReadmeIdentity(&config.Manifest{Repository: tc.identity}, tc.docs); !errors.Is(err, errReadmeIdentityUnset) {
			t.Fatalf("%+v docs=%v: %v, want errReadmeIdentityUnset", tc.identity, tc.docs, err)
		}
	}
}
