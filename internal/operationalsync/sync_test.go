package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type syncFixture struct {
	opts Options
	git  *gitRunner
}

func testGit(t *testing.T, g *gitRunner, dir string, args ...string) string {
	t.Helper()
	out, err := g.text(g.ctx, dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func testWrite(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureCommit(t *testing.T, g *gitRunner, dir string) string {
	t.Helper()
	testGit(t, g, dir, "add", "--all")
	testGit(t, g, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "commit", "-m", "fixture")
	return testGit(t, g, dir, "rev-parse", "HEAD")
}

func newSyncFixture(t *testing.T) syncFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	g, err := newGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source, owner := filepath.Join(dir, "source"), filepath.Join(dir, "owner")
	testGit(t, g, "", "init", "--template=", source)
	testGit(t, g, source, "remote", "add", "origin", "https://github.com/public/praetor.git")
	files := map[string]string{
		ownerPaths[0]:    "version: 1\nrepository:\n  owner: public\n  name: praetor\n  visibility: public\nreceipt:\n  public_key: keep-this-unknown-field\n",
		ownerPaths[1]:    "{\n  \"name\": \"public/praetor\",\n  \"unknown\": 42\n}\n",
		ownerPaths[2]:    "{\n  \"platform\": \"public/praetor\",\n  \"future\": true\n}\n",
		ownerPaths[3]:    "# Paperclip Operating Rules (public/praetor)\n\nUntouched contracts.\n",
		".gitattributes": "engine.txt filter=probe\n",
		"engine.txt":     "base engine\n"}
	for path, data := range files {
		testWrite(t, source, path, data)
	}
	base := fixtureCommit(t, g, source)
	testGit(t, g, "", "clone", "--template=", "--no-hardlinks", source, owner)
	testGit(t, g, owner, "remote", "set-url", "origin", "https://github.com/private/praetor.git")
	testGit(t, g, owner, "remote", "add", "upstream", "https://github.com/public/praetor.git")
	for _, path := range ownerPaths {
		data := strings.ReplaceAll(files[path], "public/praetor", "private/praetor")
		if path == ownerPaths[0] {
			data = strings.ReplaceAll(data, "owner: public", "owner: private")
			data = strings.ReplaceAll(data, "visibility: public", "visibility: private")
		}
		testWrite(t, owner, path, data)
	}
	ownerSHA := fixtureCommit(t, g, owner)
	testWrite(t, source, "engine.txt", "reviewed upstream advance\n")
	next := fixtureCommit(t, g, source)
	return syncFixture{Options{OwnerPath: owner, SourcePath: source, OwnerSHA: ownerSHA, BaseSHA: base, SourceSHA: next, Destination: filepath.Join(dir, "candidate")}, g}
}

func TestRunPreparePreservesHistoryAndExactOverlay(t *testing.T) {
	f := newSyncFixture(t)
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !r.MergePending || r.UpToDate {
		t.Fatalf("unexpected state: %+v", r)
	}
	if got := testGit(t, f.git, r.Candidate, "rev-parse", "HEAD"); got != f.opts.OwnerSHA {
		t.Fatal(got)
	}
	if got := testGit(t, f.git, r.Candidate, "rev-parse", "MERGE_HEAD"); got != f.opts.SourceSHA {
		t.Fatal(got)
	}
	raw, err := os.ReadFile(filepath.Join(r.Candidate, "engine.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "reviewed upstream advance\n" {
		t.Fatal(string(raw))
	}
	manifestBytes, err := os.ReadFile(filepath.Join(r.Candidate, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestBytes), "keep-this-unknown-field") {
		t.Fatal("unknown field lost")
	}
	if got := testGit(t, f.git, f.opts.OwnerPath, "status", "--porcelain"); got != "" {
		t.Fatal("original changed", got)
	}
	fixtureCommit(t, f.git, r.Candidate)
	parents := testGit(t, f.git, r.Candidate, "show", "-s", "--format=%P", "HEAD")
	if parents != f.opts.OwnerSHA+" "+f.opts.SourceSHA {
		t.Fatalf("merge parents: %s", parents)
	}
}

func TestRunPlanAndUpToDateBoundary(t *testing.T) {
	f := newSyncFixture(t)
	opts := f.opts
	opts.Destination = ""
	r, err := Run(context.Background(), "plan", opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Candidate != "" || r.MergePending {
		t.Fatalf("plan mutated: %+v", r)
	}
	if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
		t.Fatal("plan created candidate")
	}
	f.opts.SourceSHA = f.opts.BaseSHA
	r, err = Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !r.UpToDate || r.MergePending {
		t.Fatalf("incorrect no-op: %+v", r)
	}
	if got := testGit(t, f.git, r.Candidate, "status", "--porcelain"); got != "" {
		t.Fatal("no-op staged changes", got)
	}
}

func TestRunRejectsInputDrift(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *syncFixture)
	}{
		{"dirty", func(t *testing.T, f *syncFixture) { testWrite(t, f.opts.OwnerPath, "engine.txt", "uncommitted") }},
		{"engine", func(t *testing.T, f *syncFixture) {
			testWrite(t, f.opts.OwnerPath, "engine.txt", "private fork")
			f.opts.OwnerSHA = fixtureCommit(t, f.git, f.opts.OwnerPath)
		}},
		{"unknown_override", func(t *testing.T, f *syncFixture) {
			testWrite(t, f.opts.OwnerPath, ownerPaths[1], "{\"name\":\"private/praetor\",\"unknown\":43}")
			f.opts.OwnerSHA = fixtureCommit(t, f.git, f.opts.OwnerPath)
		}},
		{"remote", func(t *testing.T, f *syncFixture) {
			testGit(t, f.git, f.opts.OwnerPath, "remote", "set-url", "origin", "https://github.com/elsewhere/praetor.git")
		}},
		{"stale_review", func(t *testing.T, f *syncFixture) { f.opts.OwnerSHA = f.opts.BaseSHA }},
		{"short_sha", func(t *testing.T, f *syncFixture) { f.opts.SourceSHA = f.opts.SourceSHA[:8] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			tc.change(t, &f)
			if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
				t.Fatal("expected rejection")
			}
			if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
				t.Fatal("invalid plan created candidate")
			}
		})
	}
}

func TestRunRejectsBoundaries(t *testing.T) {
	f := newSyncFixture(t)
	var missingContext context.Context
	if _, err := Run(missingContext, "plan", f.opts); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := Run(context.Background(), "publish", f.opts); err == nil {
		t.Fatal("unsupported publication accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, "prepare", f.opts); err == nil {
		t.Fatal("cancelled context accepted")
	}
	f.opts.Destination = f.opts.OwnerPath
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
		t.Fatal("existing destination accepted")
	}
	f.opts.Destination = filepath.Join(f.opts.OwnerPath, "nested")
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
		t.Fatal("nested destination accepted")
	}
}

func TestRunResolvesOnlyOwnerConflict(t *testing.T) {
	f := newSyncFixture(t)
	raw, err := os.ReadFile(filepath.Join(f.opts.SourcePath, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	testWrite(t, f.opts.SourcePath, ownerPaths[0], string(raw)+"future_field:\n  enabled: true\n")
	f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(r.Candidate, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "future_field:") || !strings.Contains(string(got), "owner: private") {
		t.Fatal(string(got))
	}
}
