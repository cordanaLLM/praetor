package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
)

const (
	// cliDebt is Go source carrying one HISS infraction: an error result that is discarded.
	cliDebt = "package main\n\nfunc run() {\n\t_ = doSomething()\n}\n"
	// cliBaseline is the baseline file adoption records, relative to the repository.
	cliBaseline = ".standards-baseline.json"
)

// adoptWithFlags runs `adopt` on repo with extra flags and returns what it printed.
func adoptWithFlags(t *testing.T, repo, source string, flags ...string) (string, error) {
	t.Helper()
	args := append([]string{"--path", repo, "--profile", "framework", "--lock-source-root", source}, flags...)
	return captureStdout(t, func() error { return runAdopt(args) })
}

// TestAdoptBaselineFlags_3D pins #358 through the command. Positive: a first run records the
// baseline, and an allowed --rerecord-baseline raises it with the rationale. Negative: a default
// re-run, --force included, after the repository gained an infraction keeps the file byte for
// byte and prints the rejecting verdict with the finding; --rerecord-baseline alone is refused.
// Boundary: a dry run of the allowed re-record writes nothing, and increase flags without
// --rerecord-baseline, or a re-record --record-baseline=false declines, fail before any write.
func TestAdoptBaselineFlags_3D(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{
		"go.mod": "module example.invalid/adopted\n\ngo 1.27\n", "main.go": cliDebt,
	})
	full := filepath.Join(repo, cliBaseline)
	for _, flags := range [][]string{{"--allow-increase"}, {"--reason=why"}, {"--record-baseline=false", "--rerecord-baseline"}} {
		if out, err := adoptWithFlags(t, repo, source, flags...); !errors.Is(err, adopt.ErrBaselineOptions) {
			t.Fatalf("adopt %v = %v; want adopt.ErrBaselineOptions\n%s", flags, err, out)
		}
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("refused flags wrote a baseline: %v", err)
	}

	out, err := adoptWithFlags(t, repo, source)
	if err != nil {
		t.Fatalf("first adoption: %v\n%s", err, out)
	}
	recorded := readFixtureFile(t, repo, cliBaseline)
	mustContain(t, out, "Legacy Technical Debt Baselined (1 infractions)")

	writeFixtureFile(t, repo, "added.go", strings.Replace(cliDebt, "run", "added", 1))
	for _, flags := range [][]string{nil, {"--force"}} {
		out, err = adoptWithFlags(t, repo, source, flags...)
		if err != nil || readFixtureFile(t, repo, cliBaseline) != recorded {
			t.Fatalf("re-adoption %v = %v; want the baseline kept byte for byte\n%s", flags, err, out)
		}
		mustContain(t, out, "Existing Legacy Technical Debt Baseline: 1 infractions",
			"Baseline kept, not re-recorded; HISS-13 ratchet rejects: 2 active infractions against 1 recorded, 1 not in the baseline",
			"added.go:4", "--rerecord-baseline")
	}

	out, err = adoptWithFlags(t, repo, source, "--rerecord-baseline")
	if !errors.Is(err, baseline.ErrDebtIncrease) || readFixtureFile(t, repo, cliBaseline) != recorded {
		t.Fatalf("--rerecord-baseline of a higher count = %v; want baseline.ErrDebtIncrease and the file kept\n%s", err, out)
	}
	mustContain(t, err.Error(), "added.go:4", "--allow-increase --reason=<why>")

	allowed := []string{"--rerecord-baseline", "--allow-increase", "--reason=generated shim"}
	if out, err = adoptWithFlags(t, repo, source, append(allowed, "--dry-run")...); err != nil || readFixtureFile(t, repo, cliBaseline) != recorded {
		t.Fatalf("dry-run re-record = %v; want a plan that leaves the baseline byte for byte\n%s", err, out)
	}
	if out, err = adoptWithFlags(t, repo, source, allowed...); err != nil {
		t.Fatalf("allowed re-record: %v\n%s", err, out)
	}
	raised, err := baseline.LoadBaseline(full)
	if err != nil || raised.TotalInfractions != 2 || raised.IncreaseRationale != "generated shim" {
		t.Fatalf("re-recorded baseline = %+v, %v; want 2 infractions and the rationale", raised, err)
	}
	mustContain(t, out, "Legacy Technical Debt Baselined (2 infractions)", "recorded rationale: generated shim")
}

// pendingBaselineLine is how an applied run closes when only the kept baseline holds it back.
const pendingBaselineLine = "Repository adopted into cordanaLLM/praetor governance; not ready yet: Debt Baseline. See the warnings above."

// TestAdoptRejectedKeptBaseline_3D pins the shape #358 was reported in through the command: a
// repository adopted without debt holds a baseline with zero entries and then gains one finding.
// Positive: the first adoption ends with the success line. Negative: a default re-run, --force
// included, keeps the baseline byte for byte and ends without the success line, naming the Debt
// Baseline as not ready, the verdict and each command that resolves it; it exits 0, as a pending
// Verification Gate does (#594), while `praetorctl baseline --verify` rejects the same tree.
// Boundary: once the debt is accepted with the allowed re-record the success line returns.
func TestAdoptRejectedKeptBaseline_3D(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{"go.mod": "module example.invalid/adopted\n\ngo 1.27\n"})
	full := filepath.Join(repo, cliBaseline)
	out, err := adoptWithFlags(t, repo, source)
	if err != nil {
		t.Fatalf("first adoption: %v\n%s", err, out)
	}
	mustContain(t, out, "Legacy Technical Debt: 0 infractions (Baselined)", adoptSuccessLine)
	recorded := readFixtureFile(t, repo, cliBaseline)
	if zero, err := baseline.LoadBaseline(full); err != nil || len(zero.Infractions) != 0 {
		t.Fatalf("first baseline = %+v, %v; want zero entries", zero, err)
	}

	writeFixtureFile(t, repo, "added.go", cliDebt)
	for _, flags := range [][]string{nil, {"--force"}} {
		out, err = adoptWithFlags(t, repo, source, flags...)
		if err != nil || readFixtureFile(t, repo, cliBaseline) != recorded {
			t.Fatalf("re-adoption %v = %v; want exit 0 and the baseline kept byte for byte\n%s", flags, err, out)
		}
		mustContain(t, out, pendingBaselineLine,
			"  Debt Baseline: Baseline kept, not re-recorded; HISS-13 ratchet rejects: 1 active infractions against 0 recorded, 1 not in the baseline",
			"  Resolve: fix the findings, or accept them deliberately with 'praetorctl adopt --rerecord-baseline --allow-increase --reason=<why>' or "+
				"'praetorctl baseline --record --allow-increase --reason=<why>'; until then praetorctl audit rejects the repository",
			"added.go:4")
		if strings.Contains(out, adoptSuccessLine) {
			t.Fatalf("re-adoption %v ended with the success line over a rejected baseline:\n%s", flags, out)
		}
		if verify, err := captureStdout(t, func() error { return runBaseline([]string{"--verify", "--file", full}) }); err == nil {
			t.Fatalf("baseline --verify passed the tree adoption %v reported as rejected:\n%s", flags, verify)
		}
	}

	out, err = adoptWithFlags(t, repo, source, "--rerecord-baseline", "--allow-increase", "--reason=generated shim")
	if err != nil {
		t.Fatalf("allowed re-record: %v\n%s", err, out)
	}
	mustContain(t, out, adoptSuccessLine)
	if strings.Contains(out, "not ready yet") {
		t.Fatalf("an accepted baseline still held the success line back:\n%s", out)
	}
}

// TestAdoptAllMissing_PassesTheRerecordFlags: --all-missing hands every repository the baseline
// flags of the command line. An unmanaged repository that carries a baseline with zero entries
// and one finding is re-recorded with the rationale, which only the three flags together allow;
// without them the kept baseline would still hold zero entries.
func TestAdoptAllMissing_PassesTheRerecordFlags(t *testing.T) {
	devDir := t.TempDir()
	repo := filepath.Join(devDir, "unmanaged")
	full := writeFixtureFile(t, repo, cliBaseline, "")
	if err := baseline.SaveBaseline(full, &baseline.Baseline{Version: 1, Infractions: []baseline.Infraction{}}); err != nil {
		t.Fatal(err)
	}
	source := adoptFixtureAt(t, repo, map[string]string{"go.mod": "module example.invalid/adopted\n\ngo 1.27\n", "main.go": cliDebt})

	out, err := captureStdout(t, func() error {
		return runAdopt([]string{
			"--all-missing", "--dev-dir", devDir, "--lock-source-root", source,
			"--rerecord-baseline", "--allow-increase", "--reason=imported shim",
		})
	})
	if err != nil {
		t.Fatalf("adopt --all-missing: %v\n%s", err, out)
	}
	mustContain(t, out, "[ADOPTED] unmanaged", "recorded rationale: imported shim")
	raised, err := baseline.LoadBaseline(full)
	if err != nil || raised.TotalInfractions != 1 || raised.IncreaseRationale != "imported shim" {
		t.Fatalf("baseline after --all-missing = %+v, %v; want the finding re-recorded with the rationale", raised, err)
	}
}
