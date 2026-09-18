package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJSONRejectsNestedReplacementAndDuplicateKeys(t *testing.T) {
	for _, raw := range []string{
		`{"name":"public/praetor", "nested": {"name": "public/praetor"}}`,
		`{"name": "public/praetor", "name":"public/praetor"}`,
		`{"name": "public/praetor", "nested": {"key":1,"key":2}}`,
	} {
		if _, err := ownerJSON([]byte(raw), "name", "public/praetor", "private/praetor"); err == nil {
			t.Fatalf("accepted ambiguous input: %s", raw)
		}
	}
	raw := []byte("{\"name\": \"public/praetor\", \"nested\":{\"name\":\"public/praetor\"},\"integer\":123456789123456789123456789}\n")
	got, err := ownerJSON(raw, "name", "public/praetor", "private/praetor")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"name\": \"private/praetor\", \"nested\":{\"name\":\"public/praetor\"},\"integer\":123456789123456789123456789}\n"
	if string(got) != want {
		t.Fatalf("unrelated data changed: %s", got)
	}
}

func TestManifestRejectsIdentityAnchors(t *testing.T) {
	for _, raw := range []string{
		"version: 1\nrepository:\n owner: &owner public\n name: praetor\n visibility: public\nother: *owner\n",
		"version: 1\nrepository: &repo\n owner: public\n name: praetor\n visibility: public\nother: *repo\n",
		"version: 1\nrepository:\n owner: public\n name: praetor\n visibility: public\n source: &source public/praetor\nother: *source\n",
	} {
		if _, err := ownerManifest([]byte(raw), identity{"private", "praetor", "private"}); err == nil {
			t.Fatal("anchor allowed unrelated alias mutation")
		}
	}
}

// TestOwnerManifestWritesRepositorySource is the positive dimension for #255: overlaying a
// canonical manifest that has never carried repository.source inserts it, set to the source
// identity the overlay is rewriting owner/name away from.
func TestOwnerManifestWritesRepositorySource(t *testing.T) {
	raw := []byte("version: 1\nrepository:\n  owner: public\n  name: praetor\n  visibility: public\n")
	out, err := ownerManifest(raw, identity{"private", "praetor", "private"})
	if err != nil {
		t.Fatal(err)
	}
	_, decoded, err := manifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Owner != "private" || decoded.Visibility != "private" {
		t.Fatalf("owner/visibility not overlaid: %+v", decoded)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	repo, ok := doc["repository"].(map[string]any)
	if !ok {
		t.Fatalf("repository section decoded as %T, want map[string]any", doc["repository"])
	}
	if repo["source"] != "public/praetor" {
		t.Fatalf("repository.source = %v, want public/praetor", repo["source"])
	}
}

// TestOwnerManifestOverwritesStaleRepositorySource is the boundary dimension: a manifest that
// already declares repository.source (a second sync round, applied to the owner's own
// previously overlaid file re-read as a base -- or any stray prior value) gets the field
// forced to the freshly computed source identity, the same way owner and visibility already
// are, rather than preserving whatever value was already there.
func TestOwnerManifestOverwritesStaleRepositorySource(t *testing.T) {
	raw := []byte("version: 1\nrepository:\n  owner: public\n  name: praetor\n  visibility: public\n  source: stale/value\n")
	out, err := ownerManifest(raw, identity{"private", "praetor", "private"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	repo, ok := doc["repository"].(map[string]any)
	if !ok {
		t.Fatalf("repository section decoded as %T, want map[string]any", doc["repository"])
	}
	if repo["source"] != "public/praetor" {
		t.Fatalf("repository.source = %v, want public/praetor to overwrite the stale value", repo["source"])
	}
}

func TestCandidateVerificationRejectsUnstagedAndUntrackedData(t *testing.T) {
	for _, path := range []string{"engine.txt", "untracked.txt"} {
		t.Run(path, func(t *testing.T) {
			f := newSyncFixture(t)
			ctx := context.Background()
			if _, err := Run(ctx, "prepare", f.opts); err != nil {
				t.Fatal(err)
			}
			op := operation{git: f.git, opts: f.opts}
			if err := op.validate(ctx); err != nil {
				t.Fatal(err)
			}
			testWrite(t, f.opts.Destination, path, "unexpected worktree data")
			if err := op.verifyCandidate(ctx); err == nil {
				t.Fatal("candidate drift accepted")
			}
		})
	}
}

// preOverlaySourceManifest rewrites owner/visibility the way the #253 overlay always did, but
// -- unlike newSyncFixture's default owner manifest -- never adds repository.source, the shape
// #258 started writing. It reproduces the owner manifest of a fork that ran `operational sync
// init` before #258 shipped (#263).
func preOverlaySourceManifest(publicManifest string) string {
	out := strings.ReplaceAll(publicManifest, "owner: public", "owner: private")
	return strings.ReplaceAll(out, "visibility: public", "visibility: private")
}

// TestPlanAcceptsAndPrepareBackfillsPreOverlaySourceManifest is the positive dimension for
// #263's migration: an owner manifest overlaid before repository.source existed must still
// `plan` (checkOverlay's equivalentManifestOverlay accepts the missing key), and `prepare` must
// write the field into the candidate rather than silently leaving it missing forever. SourceSHA
// is set equal to BaseSHA so prepare takes the up-to-date branch (nothing to merge), the same
// technique TestRunPlanAndUpToDateBoundary uses, which is exactly the shape backfillManifestOverlay
// exists for: a real merge already carries the field through writeOverlay unconditionally.
func TestPlanAcceptsAndPrepareBackfillsPreOverlaySourceManifest(t *testing.T) {
	f := newSyncFixtureCustom(t, nil, preOverlaySourceManifest)

	planOpts := f.opts
	planOpts.Destination = ""
	planned, err := Run(context.Background(), "plan", planOpts)
	if err != nil {
		t.Fatalf("pre-#258 owner manifest must still plan: %v", err)
	}
	if planned.Status != "planned" {
		t.Fatalf("status = %q, want planned: %+v", planned.Status, planned)
	}

	f.opts.SourceSHA = f.opts.BaseSHA
	prepared, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatalf("prepare on a pre-#258 owner manifest: %v", err)
	}
	if !prepared.UpToDate || prepared.MergePending {
		t.Fatalf("unexpected state: %+v", prepared)
	}
	got, err := os.ReadFile(filepath.Join(prepared.Candidate, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	_, decoded, err := manifest(got)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Owner != "private" || decoded.Visibility != "private" {
		t.Fatalf("owner/visibility lost during backfill: %+v", decoded)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	repo, ok := doc["repository"].(map[string]any)
	if !ok {
		t.Fatalf("repository section decoded as %T, want map[string]any", doc["repository"])
	}
	if repo["source"] != "public/praetor" {
		t.Fatalf("repository.source = %v, want public/praetor written by prepare", repo["source"])
	}
	if status := testGit(t, f.git, prepared.Candidate, "status", "--porcelain"); status == "" {
		t.Fatal("prepare must stage the backfilled manifest")
	}
}

// TestPlanRefusesWrongRepositorySource is the negative dimension: an owner manifest that
// carries a repository.source differing from the actual public source is not the missing-field
// migration case equivalentManifestOverlay forgives -- it is a wrong value, and stays refused.
func TestPlanRefusesWrongRepositorySource(t *testing.T) {
	f := newSyncFixtureCustom(t, nil, func(publicManifest string) string {
		out := preOverlaySourceManifest(publicManifest)
		return strings.Replace(out, "visibility: private\n", "visibility: private\n  source: wrong/praetor\n", 1)
	})
	opts := f.opts
	opts.Destination = ""
	if _, err := Run(context.Background(), "plan", opts); err == nil {
		t.Fatal("owner manifest with a wrong repository.source was accepted")
	}
}

func TestSourceManifestBoundIsExplicit(t *testing.T) {
	f := newSyncFixture(t)
	raw, err := os.ReadFile(filepath.Join(f.opts.SourcePath, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := append(raw, make([]byte, 1<<20)...)
	if err := os.WriteFile(filepath.Join(f.opts.SourcePath, ownerPaths[0]), tooLarge, 0o600); err != nil {
		t.Fatal(err)
	}
	f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
		t.Fatal("partial scope created candidate")
	}
}
