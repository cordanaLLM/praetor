package operationalsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func commitStaged(t *testing.T, g *gitRunner, dir string) string {
	t.Helper()
	testGit(t, g, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "commit", "-m", "fixture")
	return testGit(t, g, dir, "rev-parse", "HEAD")
}

// forceCommit tracks files the public .gitignore excludes, the way an operational fork does.
func forceCommit(t *testing.T, g *gitRunner, dir string, files map[string]string) string {
	t.Helper()
	for path, data := range files {
		testWrite(t, dir, path, data)
		testGit(t, g, dir, "add", "-f", "--", path)
	}
	return commitStaged(t, g, dir)
}

func planOptions(f syncFixture) Options {
	opts := f.opts
	opts.Destination = ""
	return opts
}

func TestOwnerOnlyPathsPassPlanAndPrepare(t *testing.T) {
	f := newSyncFixture(t)
	files := map[string]string{
		".config/fleet.yaml": "runners: {}\n", ".config/fleet-topology.yaml": "orgs: []\n",
		".config/orgs/example.yaml": "runners: {}\n", ".config/operator/workstations/host.yaml": "version: 1\n",
		"deploy/arc/scale-set.yaml": "kind: List\n", "deploy/k8s/app/kustomization.yaml": "resources: []\n"}
	f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, files)
	want := make([]string, 0, len(files))
	for path := range files {
		want = append(want, path)
	}
	slices.Sort(want)
	plan, err := Run(context.Background(), "plan", planOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "planned" || !slices.Equal(plan.OwnerOnlyPaths, want) {
		t.Fatalf("plan: %+v", plan)
	}
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !r.MergePending || !slices.Equal(r.OwnerOnlyPaths, want) {
		t.Fatalf("prepare: %+v", r)
	}
	commitStaged(t, f.git, r.Candidate)
	tracked := strings.Split(testGit(t, f.git, r.Candidate, "ls-files", "--", ".config", "deploy"), "\n")
	if !slices.Equal(tracked, want) {
		t.Fatalf("candidate lost operator files: %v", tracked)
	}
	raw, err := os.ReadFile(filepath.Join(r.Candidate, "engine.txt"))
	if err != nil || string(raw) != "reviewed upstream advance\n" {
		t.Fatalf("engine advance missing: %q %v", raw, err)
	}
}

func TestReportListsNoOwnerOnlyPathsAsEmptyArray(t *testing.T) {
	f := newSyncFixture(t)
	r, err := Run(context.Background(), "plan", planOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.OwnerOnlyPaths == nil || len(r.OwnerOnlyPaths) != 0 {
		t.Fatalf("owner_only_paths must encode as an empty array: %#v", r.OwnerOnlyPaths)
	}
}

func TestOwnerOnlyPathsRefuseEngineAndNeighbourPaths(t *testing.T) {
	for _, path := range []string{".config/orgsx/a.yaml", ".config/orgs.yaml", ".config/fleet.yaml.bak", ".config/fleet.yaml/nested",
		"deploy/k8sx/a.yaml", "deploy/helm/values.yaml", ".config/archetypes/a.yaml", "nested/deploy/k8s/a.yaml", ".github/workflows/fork.yml"} {
		t.Run(path, func(t *testing.T) {
			f := newSyncFixture(t)
			f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{path: "operator\n"})
			_, err := Run(context.Background(), "prepare", f.opts)
			if err == nil || !strings.Contains(err.Error(), "unexpected owner tree difference") {
				t.Fatalf("accepted or misreported %s: %v", path, err)
			}
			if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
				t.Fatal("refused plan created candidate")
			}
		})
	}
}

func TestOwnerOnlyPathsRefuseSourceCollisions(t *testing.T) {
	t.Run("present at base", func(t *testing.T) {
		f := newSyncFixtureWith(t, map[string]string{"deploy/k8s/app.yaml": "upstream\n"})
		f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{"deploy/k8s/app.yaml": "operator\n"})
		if _, err := Run(context.Background(), "plan", planOptions(f)); err == nil || !strings.Contains(err.Error(), "exists in the public source at "+f.opts.BaseSHA) {
			t.Fatalf("base collision: %v", err)
		}
	})
	t.Run("differs from a public path only by letter case", func(t *testing.T) {
		// The owner tree carries the case-differing spelling in place of the public one
		// rather than beside it. A case-insensitive checkout (APFS by default on macOS,
		// NTFS on Windows) folds deploy/k8s/APP.yaml onto deploy/k8s/app.yaml, so no
		// fixture can hold both names there at once: the write lands on the existing
		// dirent, git's case-sensitive pathspec matching then finds nothing to stage, and
		// the commit failed with an exit status and an empty stderr (#135). The refusal
		// under test is the engine's, which folds case before comparing against the public
		// source, and it does not need the two spellings to coexist in one checkout.
		f := newSyncFixtureWith(t, map[string]string{"deploy/k8s/app.yaml": "upstream\n"})
		testGit(t, f.git, f.opts.OwnerPath, "rm", "-q", "--", "deploy/k8s/app.yaml")
		f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{"deploy/k8s/APP.yaml": "operator\n"})
		// The named path is the case-differing one, which is what proves the fold ran: a
		// byte-exact comparison would have reported the deleted public spelling instead.
		_, err := Run(context.Background(), "plan", planOptions(f))
		if err == nil || !strings.Contains(err.Error(), "exists in the public source at "+f.opts.BaseSHA+`: "deploy/k8s/APP.yaml"`) {
			t.Fatalf("case collision: %v", err)
		}
	})
	t.Run("present at source", func(t *testing.T) {
		f := newSyncFixture(t)
		f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{".config/fleet.yaml": "operator\n"})
		f.opts.SourceSHA = forceCommit(t, f.git, f.opts.SourcePath, map[string]string{".config/fleet.yaml": "upstream\n"})
		if _, err := Run(context.Background(), "plan", planOptions(f)); err == nil || !strings.Contains(err.Error(), "exists in the public source at "+f.opts.SourceSHA) {
			t.Fatalf("source collision: %v", err)
		}
	})
	t.Run("engine file renamed under a prefix", func(t *testing.T) {
		f := newSyncFixture(t)
		if err := os.MkdirAll(filepath.Join(f.opts.OwnerPath, "deploy", "k8s"), 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, f.git, f.opts.OwnerPath, "mv", "engine.txt", "deploy/k8s/engine.txt")
		testGit(t, f.git, f.opts.OwnerPath, "add", "-f", "--", "deploy/k8s/engine.txt")
		f.opts.OwnerSHA = commitStaged(t, f.git, f.opts.OwnerPath)
		if _, err := Run(context.Background(), "plan", planOptions(f)); err == nil || !strings.Contains(err.Error(), `"engine.txt"`) {
			t.Fatalf("rename hid the engine deletion: %v", err)
		}
	})
}

func TestOwnerOnlyPathsRefuseIrregularEntries(t *testing.T) {
	t.Run("executable", func(t *testing.T) {
		f := newSyncFixture(t)
		testWrite(t, f.opts.OwnerPath, "deploy/arc/run.sh", "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(f.opts.OwnerPath, "deploy/arc/run.sh"), 0o700); err != nil {
			t.Fatal(err)
		}
		testGit(t, f.git, f.opts.OwnerPath, "add", "-f", "--", "deploy/arc/run.sh")
		testGit(t, f.git, f.opts.OwnerPath, "update-index", "--chmod=+x", "--", "deploy/arc/run.sh")
		f.opts.OwnerSHA = commitStaged(t, f.git, f.opts.OwnerPath)
		if _, err := Run(context.Background(), "plan", planOptions(f)); err == nil || !strings.Contains(err.Error(), "100755 blob") {
			t.Fatalf("executable: %v", err)
		}
	})
	for name, tc := range map[string][2]string{"symlink": {"120000", "120000 blob"}, "gitlink": {"160000", "submodule"}} {
		mode, reason := tc[0], tc[1]
		t.Run(name, func(t *testing.T) {
			f := newSyncFixture(t)
			object := f.opts.OwnerSHA
			if mode == "120000" {
				object = blobObject(t, f.git, f.opts.OwnerPath, "engine.txt")
			}
			stageIndexEntry(t, f.git, f.opts.OwnerPath, mode, object, "deploy/k8s/entry")
			f.opts.OwnerSHA = commitStaged(t, f.git, f.opts.OwnerPath)
			testGit(t, f.git, f.opts.OwnerPath, "checkout", "--", ".")
			if _, err := Run(context.Background(), "plan", planOptions(f)); err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestOwnerOnlyPathBounds(t *testing.T) {
	cases := []struct {
		name    string
		count   int
		size    int
		refused string
	}{
		{"256 paths", maxOwnerOnlyPaths, 1, ""},
		{"257 paths", maxOwnerOnlyPaths + 1, 1, "owner-only paths exceed 256"},
		{"1 MiB", 1, maxOwnerOnlyBytes, ""},
		{"1 MiB + 1", 1, maxOwnerOnlyBytes + 1, "exceeds 1 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			files := make(map[string]string, tc.count)
			for i := 0; i < tc.count; i++ {
				files[fmt.Sprintf(".config/orgs/org-%03d.yaml", i)] = strings.Repeat("a", tc.size)
			}
			f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, files)
			r, err := Run(context.Background(), "plan", planOptions(f))
			if tc.refused == "" {
				if err != nil || len(r.OwnerOnlyPaths) != tc.count {
					t.Fatalf("boundary refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("boundary accepted: %v", err)
			}
		})
	}
}

func TestCandidateMustCarryTheReviewedOwnerOnlySet(t *testing.T) {
	f := newSyncFixture(t)
	f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{"deploy/k8s/app.yaml": "operator\n"})
	ctx := context.Background()
	if _, err := Run(ctx, "prepare", f.opts); err != nil {
		t.Fatal(err)
	}
	op := operation{git: f.git, opts: f.opts}
	if err := op.validate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := op.verifyCandidate(ctx); err != nil {
		t.Fatal(err)
	}
	op.ownerOnly = []string{"deploy/k8s/other.yaml"}
	if err := op.verifyCandidate(ctx); err == nil || !strings.Contains(err.Error(), "owner-only paths differ") {
		t.Fatalf("candidate set drift accepted: %v", err)
	}
}

func TestOwnerOnlyPathMatchesWholeSegments(t *testing.T) {
	cases := map[string]bool{
		".config/fleet.yaml": true, ".config/fleet-topology.yaml": true, ".config/orgs/a.yaml": true,
		".config/orgs/deep/a.yaml": true, ".config/operator/fleet.yaml": true, "deploy/arc/a.yaml": true, "deploy/k8s/a.yaml": true,
		".config/orgs": false, ".config/orgs/": false, ".config/orgsx/a.yaml": false, ".config/fleet.yaml/a": false,
		".config/fleet.yamlx": false, "deploy/k8s": false, "deploy/k8sx/a.yaml": false, "x/deploy/k8s/a.yaml": false,
		"": false, ".standards.yaml": false, "Deploy/k8s/a.yaml": false}
	for path, want := range cases {
		if got := ownerOnlyPath(path); got != want {
			t.Errorf("ownerOnlyPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestValidateOwnerOnlyEntry(t *testing.T) {
	cases := []struct {
		name    string
		entry   treeEntry
		exists  bool
		refused bool
	}{
		{"regular", treeEntry{"100644", "blob", 12}, true, false},
		{"empty", treeEntry{"100644", "blob", 0}, true, false},
		{"limit", treeEntry{"100644", "blob", maxOwnerOnlyBytes}, true, false},
		{"over limit", treeEntry{"100644", "blob", maxOwnerOnlyBytes + 1}, true, true},
		{"executable", treeEntry{"100755", "blob", 12}, true, true},
		{"symlink", treeEntry{"120000", "blob", 12}, true, true},
		{"gitlink", treeEntry{"160000", "commit", -1}, true, true},
		{"unsized blob", treeEntry{"100644", "blob", -1}, true, true},
		{"absent", treeEntry{}, false, true},
	}
	for _, tc := range cases {
		if err := validateOwnerOnlyEntry("deploy/k8s/a.yaml", tc.entry, tc.exists); (err != nil) != tc.refused {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestParseTreeEntries(t *testing.T) {
	sha := strings.Repeat("a", 40)
	good := "100644 blob " + sha + "      39\tdeploy/k8s/a b.yaml\x00160000 commit " + sha + "       -\tdeploy/k8s/sub\x00"
	entries, err := parseTreeEntries([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if entries["deploy/k8s/a b.yaml"] != (treeEntry{"100644", "blob", 39}) || entries["deploy/k8s/sub"] != (treeEntry{"160000", "commit", -1}) {
		t.Fatalf("parsed: %+v", entries)
	}
	if empty, err := parseTreeEntries(nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty tree: %v %v", empty, err)
	}
	for _, bad := range []string{"100644 blob " + sha + " 39 deploy/k8s/a.yaml", "100644 blob " + sha + "\ta", "100644 blob " + sha + " big\ta",
		"100644 blob " + sha + " -5\ta", "100644 blob " + sha + " 1\t"} {
		if _, err := parseTreeEntries([]byte(bad)); err == nil {
			t.Errorf("accepted malformed record %q", bad)
		}
	}
}

// TestOwnerOnlyPrefixesAreIgnoredByTheEngine replays the engine's own .gitignore in a scratch
// repository: every owner-only prefix must be ignored there, so the public source cannot grow a
// colliding tracked path and the two lists cannot drift. The neighbours prove the probe discriminates.
func TestOwnerOnlyPrefixesAreIgnoredByTheEngine(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Fatalf("engine .gitignore is required for the drift check: %v", err)
	}
	f := newSyncFixture(t)
	repo := filepath.Join(t.TempDir(), "engine")
	testGit(t, f.git, "", "init", "--template=", repo)
	testWrite(t, repo, ".gitignore", string(raw))
	for _, prefix := range ownerOnlyPrefixes {
		probe := ownerOnlyProbe(prefix)
		if got, err := f.git.text(f.git.ctx, repo, "check-ignore", "--", probe); err != nil || got != probe {
			t.Errorf("engine .gitignore does not ignore owner-only prefix %s: %q %v", prefix, got, err)
		}
	}
	for _, neighbour := range []string{".config/orgsx/operator.yaml", ".config/archetypes/operator.yaml", "deploy/helm/values.yaml"} {
		if out, err := f.git.text(f.git.ctx, repo, "check-ignore", "--", neighbour); err == nil {
			t.Errorf("probe does not discriminate: %s reported ignored (%s)", neighbour, out)
		}
	}
}
