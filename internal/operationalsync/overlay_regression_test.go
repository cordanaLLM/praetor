package operationalsync

import (
	"context"
	"os"
	"path/filepath"
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
	repo, _ := doc["repository"].(map[string]any)
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
	repo, _ := doc["repository"].(map[string]any)
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
