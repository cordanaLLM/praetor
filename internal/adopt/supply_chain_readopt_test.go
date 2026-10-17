package adopt

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// readoptManifest is a manifest an older engine wrote: no exceptions list at all.
const readoptManifest = "---\nversion: 1\nrepository:\n  owner: acme\n  name: readopt\nprofiles:\n  - framework\nfacets:\n  - security:high\n"

// gapSession returns a session whose supply-chain measurement is already resolved to entry, as
// supplyChainException caches it for the run.
func gapSession(t *testing.T, entry *config.Exception) *adoptSession {
	t.Helper()
	repo := t.TempDir()
	s := &adoptSession{repoPath: repo, report: &AdoptReport{}, opts: AdoptOptions{Path: repo}}
	s.supplyChain.resolved, s.supplyChain.entry = true, entry
	return s
}

// measuredGap is the entry a first adoption records for a repository without a release workflow.
func measuredGap() *config.Exception {
	entry := newSupplyChainException(supplyChainGap{}, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	return &entry
}

// readoptedManifest returns manifest with the exceptions the re-adoption records, written through
// the manifest writer the run uses (setManifestExceptions).
func readoptedManifest(t *testing.T, s *adoptSession, manifest string) string {
	t.Helper()
	decoded, err := config.DecodeManifest([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	next, _ := s.withSupplyChainException(t.Context(), decoded.Exceptions)
	out, err := setManifestExceptions(t.Context(), []byte(manifest), next)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Positive (#1069): a manifest without a HISS-11 entry gets the one a first adoption records,
// after the entries it already declares, and the manifest writer keeps the rest of the file.
func TestWithSupplyChainException_Positive_ReadoptionRecordsTheGap(t *testing.T) {
	linter := config.Exception{Rule: "declared-linters", Tool: "gitleaks", Reason: "r", Expires: "2027-01-11"}
	got, recorded := gapSession(t, measuredGap()).withSupplyChainException(t.Context(), []config.Exception{linter})
	if recorded == nil || len(got) != 2 || got[0] != linter || got[1].Rule != config.ExceptionRuleSupplyChain {
		t.Fatalf("exceptions %+v, recorded %+v; want the linter entry then the HISS-11 entry", got, recorded)
	}
	if note := supplyChainExceptionNote(*recorded); !strings.Contains(note, "recorded rule HISS-11") {
		t.Fatalf("report note %q", note)
	}
	text := readoptedManifest(t, gapSession(t, measuredGap()), readoptManifest)
	if !strings.Contains(text, "rule: HISS-11") || !strings.HasPrefix(text, "---\nversion: 1") {
		t.Fatalf("the re-adopted manifest declares no HISS-11 entry or lost its head:\n%s", text)
	}
}

// Negative: an entry the manifest declares stands, renewed or narrowed by hand, and workflows that
// meet the declared supply chain record nothing.
func TestWithSupplyChainException_Negative_DeclaredEntryOrNoGap(t *testing.T) {
	own := config.Exception{Rule: config.ExceptionRuleSupplyChain, Path: ".github/workflows/ship.yml", Reason: "own", Expires: "2026-12-01"}
	if got, recorded := gapSession(t, measuredGap()).withSupplyChainException(t.Context(), []config.Exception{own}); recorded != nil || len(got) != 1 || got[0] != own {
		t.Fatalf("declared entry replaced: %+v, %+v", got, recorded)
	}
	if got, recorded := gapSession(t, nil).withSupplyChainException(t.Context(), nil); recorded != nil || len(got) != 0 {
		t.Fatalf("no gap recorded %+v, %+v", got, recorded)
	}
}

// Boundary: a second re-adoption over the manifest the first wrote records nothing more, and a
// manifest without the gap measured is left byte for byte.
func TestWithSupplyChainException_Boundary_Idempotent(t *testing.T) {
	once := readoptedManifest(t, gapSession(t, measuredGap()), readoptManifest)
	decoded, err := config.DecodeManifest([]byte(once))
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := gapSession(t, measuredGap()).withSupplyChainException(t.Context(), decoded.Exceptions); recorded != nil {
		t.Fatalf("a second re-adoption records another entry: %+v", recorded)
	}
	if got := readoptedManifest(t, gapSession(t, nil), readoptManifest); got != readoptManifest {
		t.Fatalf("a manifest with no gap was rewritten:\n%s", got)
	}
}

// Positive, through Adopt (#1069): a re-adoption over a manifest without the HISS-11 entry the
// first adoption recorded, as an older engine wrote it, records the entry again and says so.
func TestAdopt_Readoption_RecordsTheSupplyChainGap(t *testing.T) {
	repoPath, source := adoptedRepo(t, "readopt-supply-chain")
	full := filepath.Join(repoPath, manifestFile)
	adopted := mustRead(t, full)
	at := strings.Index(adopted, "\nexceptions:")
	if at < 0 {
		t.Skip("the first adoption recorded no HISS-11 entry for this fixture")
	}
	mustWrite(t, full, adopted[:at+1])
	rep := readoptWith(t, repoPath, source, AdoptOptions{Force: true})
	if !strings.Contains(mustRead(t, full), "rule: HISS-11") || !hasWarningContaining(rep, "declared no HISS-11 exception") {
		t.Fatalf("re-adoption did not record the gap: %v\n%s", rep.Warnings, mustRead(t, full))
	}
}
