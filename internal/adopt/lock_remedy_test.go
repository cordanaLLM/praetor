package adopt

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// profileSetRemedy is the narrow re-pin a lock mismatch names.
const profileSetRemedy = "re-pin the declaration with praetorctl profile set --lock-source-root="

// Positive (#123 comment): the mismatch stops at the first differing pin, here the framework text
// whose layout alone moved; the remedy names the facet whose values the source changes, and only
// it, then the narrow re-pin.
func TestExistingLockMismatchNamesTheValueChanges(t *testing.T) {
	s, manifest := earlierCatalogRepo(t)
	changed := successorBodies(t)
	facet := strings.Replace(changed["security:high"], "required_approving_reviewers: 2", "required_approving_reviewers: 3", 1)
	if facet == changed["security:high"] {
		t.Fatal("fixture did not change a value of the facet")
	}
	changed["security:high"] = facet
	s.opts.LockSourceRoot = newCatalogLockSource(t, manifest, changed)
	err := runLockAndCatalog(t, s)
	if !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Fatalf("a changed value must still fail plain adoption: %v", err)
	}
	message := err.Error()
	_, remedy, _ := strings.Cut(message, "; the source bundle changes values of ")
	if !strings.HasPrefix(remedy, `facet "security:high"; `+profileSetRemedy+s.opts.LockSourceRoot) || strings.Contains(remedy, `profile "framework"`) {
		t.Fatalf("the remedy must name the value change alone, then profile set:\n%s", message)
	}
}

// Boundary: a declared profile the lock does not pin, the edit a profile change starts with, gets
// the re-pin command; the texts are not compared, since the repository does not vendor the new
// profile yet.
func TestExistingLockMissingPinNamesProfileSet(t *testing.T) {
	s, _ := earlierCatalogRepo(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [os-image]\nfacets: [\"security:high\"]\n")
	err := runLockAndCatalog(t, s)
	if !errors.Is(err, config.ErrLockEntryMissing) || !strings.Contains(err.Error(), profileSetRemedy) ||
		strings.Contains(err.Error(), "changes values of") {
		t.Fatalf("a missing pin must name profile set and no value change: %v", err)
	}
}

// Negative: a verification failure the re-pin does not address, a catalog that no longer defines
// a declared id, gets no remedy.
func TestExistingLockOtherFailureHasNoRemedy(t *testing.T) {
	s, _ := earlierCatalogRepo(t)
	s.opts.LockSourceRoot = ""
	mustWrite(t, filepath.Join(s.repoPath, ".config", "archetypes", "framework.yaml"), "id: renamed\nname: Framework\n")
	err := runLockAndCatalog(t, s)
	if !errors.Is(err, config.ErrLockSourceMissing) || strings.Contains(err.Error(), "profile set") {
		t.Fatalf("a renamed archetype must fail without the re-pin remedy: %v", err)
	}
}
