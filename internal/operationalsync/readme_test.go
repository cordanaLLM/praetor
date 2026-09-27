package operationalsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/funding"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

// sourceGovernance is what the public source's README block records: a debt baseline and the
// documentation contract, whose badge links to the source repository.
var sourceGovernance = readmegovernance.State{BaselineKnown: true, LegacyDebtCount: 3, DocumentationEnabled: true,
	RepositoryOwner: "public", RepositoryName: "praetor"}

// readmeBody is an engine README with a custom HISS badge, which suppresses the managed one,
// and an unconfigured funding block beside the governance block.
const readmeBody = "# Praetor\n\n[![HISS policy](https://img.shields.io/badge/Custom-HISS-blue)](policy.md)\n\n" +
	"  <!-- praetor:funding-badges:start -->\n  <!-- praetor:funding-badges:end -->\n\nengine text\n"

// forkGovernance is the state the fork's own audit verifies: the same recorded facts under
// the fork's manifest identity.
func forkGovernance(state readmegovernance.State) readmegovernance.State {
	state.RepositoryOwner = "private"
	return state
}

// engineReadme renders the README the engine commits for state: the unconfigured funding
// rendering and the governance block as adoption writes it.
func engineReadme(t *testing.T, state readmegovernance.State) string {
	t.Helper()
	out, _, err := readmegovernance.Reconcile(renderedFor(t, "", map[string]string{"README.md": readmeBody})["README.md"], state)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// newReadmeFixture is the standard owner/source pair whose incorporated base carries readme;
// the owner, cloned from that base, carries the source's block as every fork synced before
// the overlay existed does.
func newReadmeFixture(t *testing.T, readme string) syncFixture {
	t.Helper()
	return newSyncFixtureWith(t, map[string]string{"README.md": readme})
}

func verifyForkReadme(t *testing.T, candidate string, state readmegovernance.State) string {
	t.Helper()
	readme := readCandidate(t, candidate, "README.md")
	if err := readmegovernance.Verify(readme, forkGovernance(state)); err != nil {
		t.Fatalf("synced fork fails its own README governance audit: %v\n%s", err, readme)
	}
	return readme
}

// Positive: plan reports the README, prepare renders the block for the fork so the fork's own
// audit check passes, and the rendered fork passes the next sync, which carries a new debt
// count and reworded engine text through.
func TestReadmeGovernanceOverlayRendersTheForkIdentity(t *testing.T) {
	f := newReadmeFixture(t, engineReadme(t, sourceGovernance))
	plan, err := Run(context.Background(), "plan", planOptions(f))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.ChangedPaths, "README.md") {
		t.Fatalf("plan changed_paths lacks README.md: %v", plan.ChangedPaths)
	}
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if readme := verifyForkReadme(t, r.Candidate, sourceGovernance); strings.Contains(readme, "github.com/public/praetor") {
		t.Fatalf("candidate README still links the source repository:\n%s", readme)
	}
	merged := commitStaged(t, f.git, r.Candidate)

	testGit(t, f.git, f.opts.OwnerPath, "fetch", "--no-tags", "--", r.Candidate, merged)
	testGit(t, f.git, f.opts.OwnerPath, "merge", "--ff-only", merged)
	advanced := sourceGovernance
	advanced.LegacyDebtCount = 4
	testWrite(t, f.opts.SourcePath, "README.md", strings.Replace(engineReadme(t, advanced), "engine text", "engine text, reworded", 1))
	next := fixtureCommit(t, f.git, f.opts.SourcePath)
	f.opts.OwnerSHA, f.opts.BaseSHA, f.opts.SourceSHA = merged, f.opts.SourceSHA, next
	f.opts.Destination = filepath.Join(filepath.Dir(f.opts.Destination), "candidate-2")
	again, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatalf("rendered fork refused by the next sync: %v", err)
	}
	if readme := verifyForkReadme(t, again.Candidate, advanced); !strings.Contains(readme, "engine text, reworded") {
		t.Fatalf("engine text outside the block lost:\n%s", readme)
	}
}

// Boundary: only the badge identity changes, every byte outside the block survives beside a
// funding rendering; an owner block an older engine wrote is re-rendered; an owner edit
// outside the block is refused, never overwritten.
func TestReadmeGovernanceOverlayKeepsTextOutsideTheBlock(t *testing.T) {
	source := engineReadme(t, sourceGovernance)
	f := newReadmeFixture(t, source)
	f.opts.OwnerSHA = forceCommit(t, f.git, f.opts.OwnerPath, map[string]string{funding.ConfigFile: ownerFundingDoc})
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(renderedFor(t, ownerFundingDoc, map[string]string{"README.md": source})["README.md"],
		"github.com/public/praetor/", "github.com/private/praetor/")
	if got := readCandidate(t, r.Candidate, "README.md"); got != want {
		t.Fatalf("candidate README differs beyond the badge identity:\n%s\nwant:\n%s", got, want)
	}

	older := newReadmeFixture(t, source)
	first := strings.Index(source, readmegovernance.Start) + len(readmegovernance.Start)
	last := strings.Index(source, readmegovernance.End)
	older.opts.OwnerSHA = forceCommit(t, older.git, older.opts.OwnerPath,
		map[string]string{"README.md": source[:first] + "\nwording an older engine rendered\n" + source[last:]})
	older.opts.SourceSHA = older.opts.BaseSHA
	r, err = Run(context.Background(), "prepare", older.opts)
	if err != nil {
		t.Fatalf("owner block from an older engine refused: %v", err)
	}
	if status := testGit(t, older.git, r.Candidate, "status", "--porcelain"); !r.UpToDate || !strings.Contains(status, "M  README.md") {
		t.Fatalf("up-to-date candidate lacks the staged rendering: %+v\n%s", r, status)
	}
	verifyForkReadme(t, r.Candidate, sourceGovernance)

	edited := newReadmeFixture(t, source)
	edited.opts.OwnerSHA = forceCommit(t, edited.git, edited.opts.OwnerPath, map[string]string{"README.md": source + "\nfork note\n"})
	if _, err := Run(context.Background(), "prepare", edited.opts); err == nil || !strings.Contains(err.Error(), "unexpected owner override in README.md") {
		t.Fatalf("owner edit outside the block: %v", err)
	}
	if _, statErr := os.Stat(edited.opts.Destination); !os.IsNotExist(statErr) {
		t.Fatal("refused plan created a candidate")
	}
}

// Negative: no README gets none invented and no block is inserted into a README without one,
// as adoption behaves; a block without the documentation contract names no repository and is
// left alone; a source block this renderer cannot read back stops the operation before any
// candidate exists.
func TestReadmeGovernanceOverlayInventsNothing(t *testing.T) {
	absent := newSyncFixture(t)
	r, err := Run(context.Background(), "prepare", absent.opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(r.Candidate, "README.md")); !os.IsNotExist(statErr) {
		t.Fatalf("candidate gained a README: %v", statErr)
	}
	undocumented := sourceGovernance
	undocumented.DocumentationEnabled, undocumented.RepositoryOwner, undocumented.RepositoryName = false, "", ""
	for name, readme := range map[string]string{
		"no block":         "# Praetor\n\nengine text\n",
		"no documentation": engineReadme(t, undocumented),
	} {
		t.Run(name, func(t *testing.T) {
			f := newReadmeFixture(t, readme)
			r, err := Run(context.Background(), "prepare", f.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := readCandidate(t, r.Candidate, "README.md"); got != readme || slices.Contains(r.ChangedPaths, "README.md") {
				t.Fatalf("README changed (%v):\n%s", r.ChangedPaths, got)
			}
		})
	}
	stale := newReadmeFixture(t, engineReadme(t, sourceGovernance))
	testWrite(t, stale.opts.SourcePath, "README.md", strings.Replace(engineReadme(t, sourceGovernance), "Praetor manages this repository", "Praetor governs this repository", 1))
	stale.opts.SourceSHA = fixtureCommit(t, stale.git, stale.opts.SourcePath)
	_, err = Run(context.Background(), "prepare", stale.opts)
	if !errors.Is(err, readmegovernance.ErrStale) || !strings.Contains(err.Error(), "README.md governance block of the reviewed source") {
		t.Fatalf("stale source block: %v", err)
	}
	if _, statErr := os.Stat(stale.opts.Destination); !os.IsNotExist(statErr) {
		t.Fatal("refused plan created a candidate")
	}
}

// Boundary: a fork synced before the overlay existed receives the rendering in an up-to-date
// candidate, staged; a fork that already carries it stays a clean no-op.
func TestReadmeGovernanceOverlayUpToDateBoundaries(t *testing.T) {
	source := engineReadme(t, sourceGovernance)
	earlier := newReadmeFixture(t, source)
	earlier.opts.SourceSHA = earlier.opts.BaseSHA
	r, err := Run(context.Background(), "prepare", earlier.opts)
	if err != nil {
		t.Fatal(err)
	}
	if status := testGit(t, earlier.git, r.Candidate, "status", "--porcelain"); !r.UpToDate || !strings.Contains(status, "M  README.md") {
		t.Fatalf("up-to-date candidate lacks the staged rendering: %+v\n%s", r, status)
	}
	verifyForkReadme(t, r.Candidate, sourceGovernance)

	rendered := newReadmeFixture(t, source)
	rendered.opts.OwnerSHA = forceCommit(t, rendered.git, rendered.opts.OwnerPath,
		map[string]string{"README.md": strings.ReplaceAll(source, "github.com/public/praetor/", "github.com/private/praetor/")})
	rendered.opts.SourceSHA = rendered.opts.BaseSHA
	r, err = Run(context.Background(), "prepare", rendered.opts)
	if err != nil {
		t.Fatal(err)
	}
	if status := testGit(t, rendered.git, r.Candidate, "status", "--porcelain"); !r.UpToDate || status != "" {
		t.Fatalf("an already rendered fork must stay a clean no-op: %+v %q", r, status)
	}
}
