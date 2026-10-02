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
