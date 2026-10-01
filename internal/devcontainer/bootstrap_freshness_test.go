package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// wideBounds never trips, so a test reads the measurement rather than the verdict.
var wideBounds = FreshnessBounds{MaxCommits: 1000, MaxAge: 365 * 24 * time.Hour}

// commitPaths commits the named paths of root, every change when none is named, under the
// hermetic git environment, so the host's identity, signing and hooks cannot change what the
// fixture records.
func commitPaths(t *testing.T, root, message string, paths ...string) {
	t.Helper()
	if len(paths) == 0 {
		paths = []string{"."}
	}
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{append([]string{"add", "--"}, paths...), {"commit", "-q", "-m", message}} {
		if _, err := runSourceGit(ctx, root, args...); err != nil {
			t.Fatal(err)
		}
	}
}

// writeFreshnessBundle writes the ready bundle generation writes for root's source into root.
func writeFreshnessBundle(t *testing.T, root string) string {
	t.Helper()
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, bundle, false); err != nil {
		t.Fatal(err)
	}
	return path
}

// freshnessRepository returns a Praetor source checkout whose ready bundle is committed with
// the source it carries, and the bundle's configuration path.
func freshnessRepository(t *testing.T) (string, string) {
	t.Helper()
	root := bootstrapSourceFixture(t)
	path := writeFreshnessBundle(t, root)
	commitPaths(t, root, "bundle")
	return root, path
}

// driftSource commits count changes to the captured build source.
func driftSource(t *testing.T, root string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		writeBootstrapFile(t, root, "cmd/standardsctl/main.go", fmt.Sprintf("package main\n\n// revision %d\nfunc main() {}\n", i))
		commitPaths(t, root, fmt.Sprintf("source revision %d", i), "cmd/standardsctl/main.go")
	}
}

func headCommit(t *testing.T, root, revision string) string {
	t.Helper()
	commit, err := runSourceGit(t.Context(), root, "rev-parse", revision)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(commit)
}

// Positive: a bundle carrying the working tree's source is fresh and needs no history; one
// whose source fell behind inside the bounds is measured, reported and passes, while Verify,
// the self-consistency check this one backstops, still accepts it.
func TestCheckFreshnessPositive(t *testing.T) {
	root, path := freshnessRepository(t)
	report, err := CheckFreshness(t.Context(), root, path, wideBounds, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Fresh() || report.Exceeded() || report.Err() != nil || report.Commit != "" || !strings.Contains(report.String(), "matches the working tree") {
		t.Fatalf("fresh bundle reported %+v (%s)", report, report)
	}
	bundleCommit := headCommit(t, root, "HEAD")
	driftSource(t, root, 2)
	report, err = CheckFreshness(t.Context(), root, path, wideBounds, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Fresh() || report.Exceeded() || report.Err() != nil {
		t.Fatalf("drift inside the bounds failed: %+v", report)
	}
	if report.Behind != 2 || report.Commit != bundleCommit || report.Current == report.Recorded {
		t.Fatalf("drift measured as %+v, want 2 commits behind %s", report, bundleCommit)
	}
	if line := report.String(); !strings.Contains(line, "2 commits") || !strings.Contains(line, report.Recorded) {
		t.Fatalf("drift report line %q omits the count or the recorded source", line)
	}
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatalf("self-consistency verification refused a drifted bundle: %v", err)
	}
}

// Negative: drift past either bound fails with the measurement, and every state that cannot be
// measured is an error, never a fresh report.
func TestCheckFreshnessNegativePastBound(t *testing.T) {
	root, path := freshnessRepository(t)
	driftSource(t, root, 2)
	report, err := CheckFreshness(t.Context(), root, path, FreshnessBounds{MaxCommits: 1, MaxAge: wideBounds.MaxAge}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Err(); !report.Exceeded() || !errors.Is(err, ErrBundleStale) || !strings.Contains(err.Error(), "2 commits behind (bound 1)") {
		t.Fatalf("drift past the commit bound: exceeded %v, err %v", report.Exceeded(), err)
	}
	aged := FreshnessBounds{MaxCommits: wideBounds.MaxCommits, MaxAge: time.Hour}
	report, err = CheckFreshness(t.Context(), root, path, aged, time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Err(); !errors.Is(err, ErrBundleStale) || !strings.Contains(err.Error(), "(bound 0.0 days)") {
		t.Fatalf("drift past the age bound: %v", err)
	}
}

func TestCheckFreshnessNegativeUnmeasurable(t *testing.T) {
	root, path := freshnessRepository(t)
	custom := filepath.Join(t.TempDir(), "devcontainer.json")
	writeBootstrapFile(t, filepath.Dir(custom), "devcontainer.json", "{\"name\":\"operator owned\",\"image\":\"custom:tag\"}\n")
	unavailable := writeRecordedBundle(t, BootstrapOptions{})
	cases := map[string]struct {
		root, path string
		bounds     FreshnessBounds
		want       string
	}{
		"missing bundle":       {root, filepath.Join(root, ".devcontainer", "absent.json"), wideBounds, "failed to read devcontainer file"},
		"custom config":        {root, custom, wideBounds, "records no bootstrap bundle"},
		"unavailable bundle":   {root, unavailable, wideBounds, ErrBootstrapUnavailable.Error()},
		"not a Praetor source": {t.TempDir(), path, wideBounds, "holds no go.mod"},
		"zero commit bound":    {root, path, FreshnessBounds{MaxAge: time.Hour}, "bounds must be positive"},
		"zero age bound":       {root, path, FreshnessBounds{MaxCommits: 1}, "bounds must be positive"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			report, err := CheckFreshness(t.Context(), tc.root, tc.path, tc.bounds, time.Now())
			if err == nil || report != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got report %+v, err %v; want an error naming %q", report, err, tc.want)
			}
		})
	}
	var absent context.Context
	if _, err := CheckFreshness(absent, root, path, wideBounds, time.Now()); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Negative: a git failure while measuring drift is an error. A shallow clone would count from
// its graft, a bundle no commit records has no age, a repository without commits has no HEAD,
// and a bundle outside the source root has no path in its history.
func TestCheckFreshnessNegativeGitFailures(t *testing.T) {
	t.Run("shallow clone", func(t *testing.T) {
		root, path := freshnessRepository(t)
		driftSource(t, root, 1)
		writeBootstrapFile(t, root, ".git/shallow", headCommit(t, root, "HEAD")+"\n")
		assertFreshnessError(t, root, path, "shallow clone")
	})
	t.Run("uncommitted bundle", func(t *testing.T) {
		root := bootstrapSourceFixture(t)
		path := writeFreshnessBundle(t, root)
		commitPaths(t, root, "source only", "cmd", "go.mod", "go.sum", "LICENSE")
		driftSource(t, root, 1)
		assertFreshnessError(t, root, path, "no commit records the bundle")
	})
	t.Run("no commits", func(t *testing.T) {
		root := bootstrapSourceFixture(t)
		path := writeFreshnessBundle(t, root)
		writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "package main\n\n// drifted\nfunc main() {}\n")
		assertFreshnessError(t, root, path, "devcontainer freshness: git")
	})
	t.Run("bundle outside the source root", func(t *testing.T) {
		root, _ := freshnessRepository(t)
		driftSource(t, root, 1)
		elsewhere := writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
		assertFreshnessError(t, root, elsewhere, "lies outside the source root")
	})
}

func assertFreshnessError(t *testing.T, root, path, want string) {
	t.Helper()
	report, err := CheckFreshness(t.Context(), root, path, wideBounds, time.Now())
	if err == nil || report != nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got report %+v, err %v; want an error naming %q", report, err, want)
	}
}

// Boundary: a bundle exactly at either bound passes and one past it fails; a clock behind the
// bundle commit measures no age rather than a negative one.
func TestCheckFreshnessBoundary(t *testing.T) {
	root, path := freshnessRepository(t)
	driftSource(t, root, 2)
	atCount, err := CheckFreshness(t.Context(), root, path, FreshnessBounds{MaxCommits: 2, MaxAge: wideBounds.MaxAge}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if atCount.Exceeded() || atCount.Err() != nil {
		t.Fatalf("a bundle exactly at the commit bound failed: %+v", atCount)
	}
	bounds := FreshnessBounds{MaxCommits: wideBounds.MaxCommits, MaxAge: 24 * time.Hour}
	committed := atCount.CommitTime
	for _, tc := range []struct {
		name     string
		now      time.Time
		age      time.Duration
		exceeded bool
	}{
		{"exactly at the age bound", committed.Add(bounds.MaxAge), bounds.MaxAge, false},
		{"one second past the age bound", committed.Add(bounds.MaxAge + time.Second), bounds.MaxAge + time.Second, true},
		{"clock behind the bundle commit", committed.Add(-time.Hour), 0, false},
	} {
		report, err := CheckFreshness(t.Context(), root, path, bounds, tc.now)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if report.Age != tc.age || report.Exceeded() != tc.exceeded || (report.Err() != nil) != tc.exceeded {
			t.Fatalf("%s: age %s exceeded %v, want age %s exceeded %v", tc.name, report.Age, report.Exceeded(), tc.age, tc.exceeded)
		}
	}
}
