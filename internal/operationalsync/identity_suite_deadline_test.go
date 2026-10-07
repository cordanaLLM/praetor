package operationalsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// suiteDeadlineNow is the fixed clock the deadline derivation tests run against, so none of
// them depends on how long the host takes to reach them.
var suiteDeadlineNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// requireBothDeadlines fails t unless message names both the nested and the outer deadline.
func requireBothDeadlines(t *testing.T, message string, nested, outer time.Time) {
	t.Helper()
	for _, want := range []string{nested.Format(time.RFC3339), outer.Format(time.RFC3339)} {
		if !strings.Contains(message, want) {
			t.Fatalf("message does not name deadline %s:\n%s", want, message)
		}
	}
}

func TestDeriveSuiteDeadline_Positive_OuterTimeoutLessTheMargin(t *testing.T) {
	outer := suiteDeadlineNow.Add(30 * time.Minute)
	d, err := deriveSuiteDeadline(suiteDeadlineNow, outer, true)
	if err != nil {
		t.Fatalf("a 30-minute outer deadline must leave the nested run room: %v", err)
	}
	if want := outer.Add(-overlaySuiteMargin); !d.nested.Equal(want) {
		t.Fatalf("nested deadline = %s, want the outer deadline less the margin, %s", d.nested, want)
	}
	requireBothDeadlines(t, d.String(), d.nested, outer)
}

func TestDeriveSuiteDeadline_Negative_OuterTooShortNamesBothDeadlines(t *testing.T) {
	for name, outer := range map[string]time.Time{
		"one second short": suiteDeadlineNow.Add(overlaySuiteMargin + overlaySuiteMinimum - time.Second),
		"inside margin":    suiteDeadlineNow.Add(overlaySuiteMargin / 2),
		"already passed":   suiteDeadlineNow.Add(-time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deriveSuiteDeadline(suiteDeadlineNow, outer, true)
			if err == nil {
				t.Fatal("an outer deadline under the margin plus the minimum must be refused")
			}
			requireBothDeadlines(t, err.Error(), outer.Add(-overlaySuiteMargin), outer)
			if !strings.Contains(err.Error(), "raise go test -timeout") {
				t.Fatalf("refusal does not name the flag that moves the outer deadline: %v", err)
			}
		})
	}
}

func TestDeriveSuiteDeadline_Boundary_ExactMinimumAndNoOuterDeadline(t *testing.T) {
	outer := suiteDeadlineNow.Add(overlaySuiteMargin + overlaySuiteMinimum)
	d, err := deriveSuiteDeadline(suiteDeadlineNow, outer, true)
	if err != nil {
		t.Fatalf("a window of exactly the minimum must be accepted: %v", err)
	}
	if want := suiteDeadlineNow.Add(overlaySuiteMinimum); !d.nested.Equal(want) {
		t.Fatalf("nested deadline = %s, want %s", d.nested, want)
	}
	// go test -timeout 0 sets no outer deadline; the nested run still gets an explicit bound.
	d, err = deriveSuiteDeadline(suiteDeadlineNow, time.Time{}, false)
	if err != nil {
		t.Fatalf("no outer deadline must fall back to the explicit bound: %v", err)
	}
	if want := suiteDeadlineNow.Add(overlaySuiteUnbounded); !d.nested.Equal(want) {
		t.Fatalf("nested deadline = %s, want the explicit bound %s", d.nested, want)
	}
	if !strings.Contains(d.String(), "none (go test -timeout 0)") {
		t.Fatalf("a missing outer deadline must be named as such: %s", d)
	}
}

func TestOverlaySuiteFailure_NamesBothDeadlinesOnlyWhenTheDeadlineStoppedTheRun(t *testing.T) {
	outer := suiteDeadlineNow.Add(30 * time.Minute)
	d, err := deriveSuiteDeadline(suiteDeadlineNow, outer, true)
	if err != nil {
		t.Fatal(err)
	}
	stopped := overlaySuiteFailure(d, context.DeadlineExceeded, errors.New("signal: terminated"), "ok config")
	requireBothDeadlines(t, stopped, d.nested, outer)
	if !strings.Contains(stopped, "did not finish") || !strings.Contains(stopped, "ok config") {
		t.Fatalf("a deadline stop must say so and keep the nested output:\n%s", stopped)
	}
	failed := overlaySuiteFailure(d, nil, errors.New("exit status 1"), "--- FAIL: TestIdentity")
	if strings.Contains(failed, "did not finish") || !strings.Contains(failed, "--- FAIL: TestIdentity") {
		t.Fatalf("a failing test must be reported as the regression, not as a deadline:\n%s", failed)
	}
}

// overlayFixtureReader is the helper both fixture test packages read the module's manifest with.
const overlayFixtureReader = "import (\n\t\"os\"\n\t\"path/filepath\"\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
	"func manifest(t *testing.T) string {\n\tdata, err := os.ReadFile(filepath.Join(\"..\", \".standards.yaml\"))\n" +
	"\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\treturn string(data)\n}\n\n"

// writeOverlayFixture writes a Git work tree holding a dependency-free module with a canonical
// manifest and two test packages: canonical asserts the canonical owner, the planted identity
// regression the guard exists to catch; neutral reads only what the overlay keeps.
func writeOverlayFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	g, err := newGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	testGit(t, g, source, "init", "--quiet")
	testWrite(t, source, "go.mod", "module example.com/overlayfixture\n\ngo 1.22\n")
	testWrite(t, source, ".standards.yaml",
		"version: 1\nrepository:\n  owner: cordanaLLM\n  name: praetor\n  visibility: public\n")
	testWrite(t, source, "canonical/canonical_test.go", "package canonical\n\n"+overlayFixtureReader+
		"func TestPlantedCanonicalOwner(t *testing.T) {\n\tif !strings.Contains(manifest(t), \"owner: cordanaLLM\\n\") {\n"+
		"\t\tt.Fatal(\"repository.owner is not the canonical owner\")\n\t}\n}\n")
	testWrite(t, source, "neutral/neutral_test.go", "package neutral\n\n"+overlayFixtureReader+
		"func TestRepositoryName(t *testing.T) {\n\tif !strings.Contains(manifest(t), \"name: praetor\\n\") {\n"+
		"\t\tt.Fatal(\"repository.name changed\")\n\t}\n}\n")
	return source
}

// TestOverlaySuiteRefusesAPlantedIdentityRegression proves the guard can fail (rule 13): a test
// asserting the canonical owner passes in the source tree and fails once runOverlaySuite has
// overlaid the copy, while an identity-neutral package beside it passes the same run.
func TestOverlaySuiteRefusesAPlantedIdentityRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns nested go test runs over a fixture module; excluded from -short")
	}
	outer, hasOuter := t.Deadline()
	deadline, err := deriveSuiteDeadline(time.Now(), outer, hasOuter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.nested)
	defer cancel()
	ctx, err = util.WithCommandEnvironment(ctx, testsupport.OfflineGoEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	source := writeOverlayFixture(t, ctx)

	if out, err := util.RunCommand(ctx, source, "go", "test", "-count=1", "./canonical/..."); err != nil {
		t.Fatalf("the planted test must pass against the canonical manifest (%v):\n%s", err, out)
	}
	if out, err := runOverlaySuite(t, ctx, source, []string{"./neutral/..."}); err != nil {
		t.Fatalf("an identity-neutral package must pass under the overlay (%v):\n%s", err, out)
	}
	out, err := runOverlaySuite(t, ctx, source, []string{"./canonical/..."})
	if err == nil {
		t.Fatalf("the guard passed a test that asserts the canonical owner:\n%s", out)
	}
	if !strings.Contains(out, "TestPlantedCanonicalOwner") {
		t.Fatalf("the guard failed without naming the planted test (%v):\n%s", err, out)
	}
}
