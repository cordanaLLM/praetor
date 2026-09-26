package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/funding"
)

const (
	ownerFundingDoc   = "polar: exampleOrg\nko_fi: example\n"
	fundingBadgeLink  = "](https://polar.sh/exampleOrg)"
	fundingFileRecord = "polar: exampleOrg"
	unconfiguredList  = "No sponsoring channel is configured"
)

// engineSurfaces returns the engine's funding surfaces as the engine commits them: the
// unconfigured rendering of two marked files.
func engineSurfaces(t *testing.T) map[string]string {
	t.Helper()
	return renderedFor(t, "", map[string]string{
		"README.md":            "# Praetor\n\n  <!-- praetor:funding-badges:start -->\n  <!-- praetor:funding-badges:end -->\n\nengine text\n",
		"docs/monetization.md": "# Strategy\n\n<!-- praetor:funding-channels:start -->\n<!-- praetor:funding-channels:end -->\n",
	})
}

// newFundingFixture is the standard owner/source pair with the engine funding surfaces in the
// incorporated base; ownerDoc, when set, is the fork's force-added funding document.
func newFundingFixture(t *testing.T, ownerDoc string) syncFixture {
	t.Helper()
	f := newSyncFixtureWith(t, engineSurfaces(t))
	if ownerDoc != "" {
		f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{funding.ConfigFile: ownerDoc})
	}
	return f
}

// renderedFor renders surface files the way `praetorctl docs funding` would; an empty doc is
// the unconfigured rendering.
func renderedFor(t *testing.T, doc string, files map[string]string) map[string]string {
	t.Helper()
	var cfg *funding.Config
	if doc != "" {
		parsed, err := funding.Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		cfg = parsed
	}
	in := make(map[string][]byte, len(files))
	for path, data := range files {
		in[path] = []byte(data)
	}
	out, err := funding.RenderFiles(context.Background(), in, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rendered := make(map[string]string, len(out))
	for path, data := range out {
		rendered[path] = string(data)
	}
	return rendered
}

func readCandidate(t *testing.T, candidate, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(candidate, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Positive: an owner document renders the surfaces into the candidate, the rendered commit
// passes the next plan, and an engine edit inside a rendered block is resolved by the overlay.
func TestFundingOverlayRendersTheOwnerDocument(t *testing.T) {
	f := newFundingFixture(t, ownerFundingDoc)
	plan, err := Run(context.Background(), "plan", planOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"README.md", "docs/monetization.md", ".github/FUNDING.yml"} {
		if !slices.Contains(plan.ChangedPaths, path) {
			t.Errorf("plan changed_paths lacks %s: %v", path, plan.ChangedPaths)
		}
	}
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if readme := readCandidate(t, r.Candidate, "README.md"); !strings.Contains(readme, fundingBadgeLink) || !strings.Contains(readme, "engine text") {
		t.Fatalf("candidate README not rendered:\n%s", readme)
	}
	if record := readCandidate(t, r.Candidate, ".github/FUNDING.yml"); !strings.Contains(record, fundingFileRecord) {
		t.Fatalf("candidate FUNDING.yml not rendered:\n%s", record)
	}
	merged := commitStaged(t, f.git, r.Candidate)

	// The fork publishes the merge; the engine then rewords the sentence inside its block.
	testGit(t, f.git, f.opts.OwnerPath, "fetch", "--no-tags", "--", r.Candidate, merged)
	testGit(t, f.git, f.opts.OwnerPath, "merge", "--ff-only", merged)
	testWrite(t, f.opts.SourcePath, "docs/monetization.md", strings.Replace(engineSurfaces(t)["docs/monetization.md"], unconfiguredList, "No sponsoring account is linked", 1))
	next := fixtureCommit(t, f.git, f.opts.SourcePath)
	f.opts.OwnerSHA, f.opts.BaseSHA, f.opts.SourceSHA = merged, f.opts.SourceSHA, next
	f.opts.Destination = filepath.Join(filepath.Dir(f.opts.Destination), "candidate-2")
	again, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatalf("rendered fork refused or block conflict unresolved: %v", err)
	}
	if list := readCandidate(t, again.Candidate, "docs/monetization.md"); !strings.Contains(list, "https://ko-fi.com/example") || strings.Contains(list, "No sponsoring") {
		t.Fatalf("conflicted block not re-rendered:\n%s", list)
	}
}

// Negative: a hand-edited block, a rendering without a document and an invalid document are
// refused before any candidate exists.
func TestFundingOverlayRefusesUnrenderedOwnerEdits(t *testing.T) {
	cases := map[string]struct {
		doc   string
		files func(t *testing.T) map[string]string
		want  string
	}{
		"other account": {doc: ownerFundingDoc, want: "unexpected owner override in README.md", files: func(t *testing.T) map[string]string {
			return map[string]string{"README.md": renderedFor(t, "polar: someoneElse\n", engineSurfaces(t))["README.md"]}
		}},
		"no document": {want: "unexpected owner override in README.md", files: func(t *testing.T) map[string]string {
			return map[string]string{"README.md": renderedFor(t, ownerFundingDoc, engineSurfaces(t))["README.md"]}
		}},
		"hand-made FUNDING.yml": {want: "unexpected owner override in .github/FUNDING.yml", files: func(*testing.T) map[string]string {
			return map[string]string{".github/FUNDING.yml": "github: [someone]\n"}
		}},
		"invalid document": {doc: "unknown: true\n", want: "owner funding configuration", files: func(*testing.T) map[string]string { return nil }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFundingFixture(t, tc.doc)
			if files := tc.files(t); len(files) > 0 {
				f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, files)
			}
			_, err := Run(context.Background(), "prepare", f.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if _, statErr := os.Stat(f.opts.Destination); !os.IsNotExist(statErr) {
				t.Fatal("refused plan created a candidate")
			}
		})
	}
}

// Boundary: without a document the surfaces stay the engine's and are not reported as
// changed; an already incorporated source still receives the rendering, staged, and a fork
// that already carries it stays a clean no-op.
func TestFundingOverlayBoundaries(t *testing.T) {
	plain := newFundingFixture(t, "")
	plan, err := Run(context.Background(), "plan", planOptions(plain))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.ChangedPaths, ownerPaths) {
		t.Fatalf("unconfigured changed_paths = %v, want only %v", plan.ChangedPaths, ownerPaths)
	}

	upToDate := newFundingFixture(t, ownerFundingDoc)
	upToDate.opts.SourceSHA = upToDate.opts.BaseSHA
	r, err := Run(context.Background(), "prepare", upToDate.opts)
	if err != nil {
		t.Fatal(err)
	}
	status := testGit(t, upToDate.git, r.Candidate, "status", "--porcelain")
	if !r.UpToDate || !strings.Contains(status, "M  README.md") || !strings.Contains(status, "A  .github/FUNDING.yml") {
		t.Fatalf("up-to-date candidate lacks the staged rendering: %+v\n%s", r, status)
	}

	rendered := newFundingFixture(t, ownerFundingDoc)
	rendered.opts.OwnerSHA = forceCommit(t, rendered.git, rendered.opts.OwnerPath,
		renderedFor(t, ownerFundingDoc, engineSurfaces(t)))
	rendered.opts.SourceSHA = rendered.opts.BaseSHA
	r, err = Run(context.Background(), "prepare", rendered.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, rendered.git, r.Candidate, "status", "--porcelain"); !r.UpToDate || got != "" {
		t.Fatalf("an already rendered fork must stay a clean no-op: %+v %q", r, got)
	}
}
