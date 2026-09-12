package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
	} {
		if _, err := ownerManifest([]byte(raw), identity{"private", "praetor", "private"}); err == nil {
			t.Fatal("anchor allowed unrelated alias mutation")
		}
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
