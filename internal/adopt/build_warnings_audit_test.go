package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/templates"
)

// hiss10Fixtures holds the HISS-10 workflow fixtures .config/hiss/coverage.yaml attributes to
// this gate; TestAuditBuildWarningsReplaysTheHISS10Fixtures replays them.
const hiss10Fixtures = "../../.config/hiss/testdata/HISS-10/github-actions"

// buildWarningsToday is the day the exception cases judge expiry against.
var buildWarningsToday = time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)

// ciSteps is a pull request workflow whose one job, build, runs steps.
func ciSteps(steps string) string {
	return "on: pull_request\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" + steps
}

// auditBuildWarningsIn runs the gate over files with the exceptions list entries.
func auditBuildWarningsIn(t *testing.T, files map[string]string, entries ...config.Exception) (string, error) {
	t.Helper()
	return AuditBuildWarnings(context.Background(), BuildWarningsOptions{
		Root: supplyChainRepo(t, files), Exceptions: entries, Today: buildWarningsToday,
	})
}

// buildWarningsEntry is a HISS-10 exceptions entry for the workflow path, expiring on expires.
func buildWarningsEntry(path, expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleBuildWarnings, Path: path,
		Reason: "the vendored decoder still warns under MSVC; upstream fix pending", Expires: expires}
}

// Negative, per language: a lane without its toolchain's form fails, naming the workflow, the
// job, the step and what to add (#816).
func TestAuditBuildWarningsFailsALaneWithoutTheForm(t *testing.T) {
	cases := map[string]struct {
		steps string
		want  []string
	}{
		"C through CMake": {"      - run: cmake -S . -B build\n      - run: cmake --build build\n",
			[]string{`.github/workflows/ci.yml:6: job build, step "cmake -S . -B build": CMake builds without warnings as errors`,
				"-DCMAKE_COMPILE_WARNING_AS_ERROR=ON"}},
		"C++ with gcc": {"      - run: g++ -Wall -c src/main.cpp\n",
			[]string{`step "g++ -Wall -c src/main.cpp": gcc/clang builds without warnings as errors`, "add -Werror"}},
		"C through Meson": {"      - run: meson setup build\n",
			[]string{"Meson builds without warnings as errors", "meson setup --werror"}},
		"Rust": {"      - run: cargo build --locked\n",
			[]string{`step "cargo build --locked": Cargo builds without warnings as errors`, `RUSTFLAGS: "-D warnings"`}},
		"Go": {"      - run: go build ./...\n",
			[]string{"Go builds without warnings as errors: no binding step of any workflow runs go vet", "run go vet ./..."}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": ciSteps(tc.steps)})
			if err == nil {
				t.Fatalf("AuditBuildWarnings passed:\n%s", out)
			}
			for i := 0; i < len(tc.want); i++ {
				if !strings.HasPrefix(err.Error(), "[FAIL] Build warnings (HISS-10): ") || !strings.Contains(err.Error(), tc.want[i]) {
					t.Fatalf("failure = %q; want it to contain %q", err, tc.want[i])
				}
			}
			if !strings.Contains(err.Error(), "declare its workflow in the exceptions list of .standards.yaml (rule HISS-10") {
				t.Fatalf("failure = %q; want the way out named", err)
			}
		})
	}
}

// Positive, per language: the same lanes with their toolchain's form pass, counted by
// toolchain, with the scope of the measurement.
func TestAuditBuildWarningsPassesLanesWithTheForm(t *testing.T) {
	cases := map[string]struct {
		steps, want string
	}{
		"C through CMake": {"      - run: cmake -S . -B build -DCMAKE_COMPILE_WARNING_AS_ERROR=ON\n      - run: cmake --build build\n", "1 build lanes fail on a warning (CMake 1)."},
		"C++ with gcc":    {"      - run: g++ -Wall -Werror -c src/main.cpp\n", "(gcc/clang 1)"},
		"C through Meson": {"      - run: meson setup build --werror\n", "(Meson 1)"},
		"Rust":            {"      - run: cargo build --locked\n        env:\n          RUSTFLAGS: -D warnings\n      - run: cargo clippy -- -D warnings\n", "2 build lanes fail on a warning (Cargo 2)."},
		"Go":              {"      - run: go vet ./...\n      - run: go test ./...\n", "(Go 1)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": ciSteps(tc.steps)})
			if err != nil {
				t.Fatalf("AuditBuildWarnings: %v", err)
			}
			if !strings.HasPrefix(out, "[PASS] Build warnings (HISS-10): ") || !strings.Contains(out, tc.want) || !strings.HasSuffix(out, buildWarningsScope) {
				t.Fatalf("pass = %q; want %q and the measurement's scope", out, tc.want)
			}
		})
	}
}

// A HISS-10 entry naming the workflow declares its failing lanes, C, Rust and Go alike, until
// the entry expires. Positive: a live entry passes, printing its expiry and reason over every
// lane it excuses, while the other lanes are still counted. Boundary: the entry holds on its
// expires day.
func TestAuditBuildWarningsPassesAnExceptedWorkflow(t *testing.T) {
	files := map[string]string{
		".github/workflows/ci.yml": ciSteps("      - run: cl /W4 /c src\\decoder.c\n      - run: cmake -B build\n" +
			"      - run: cargo build --locked\n      - run: go build ./...\n"),
		".github/workflows/native.yml": ciSteps("      - run: cargo test\n        env:\n          RUSTFLAGS: -Dwarnings\n"),
	}
	for _, expires := range []string{"2026-12-31", "2026-10-07"} {
		out, err := auditBuildWarningsIn(t, files, buildWarningsEntry(".github/workflows/ci.yml", expires))
		if err != nil {
			t.Fatalf("expires %s: AuditBuildWarnings: %v", expires, err)
		}
		for _, want := range []string{
			"1 build lanes fail on a warning (Cargo 1); 4 excepted:",
			"  - .github/workflows/ci.yml, excepted until " + expires + " by the exceptions entry (rule HISS-10, .github/workflows/ci.yml): the vendored decoder",
			`    - .github/workflows/ci.yml:6: job build, step "cl /W4 /c src\\decoder.c": MSVC cl builds without warnings as errors`,
			`    - .github/workflows/ci.yml:7: job build, step "cmake -B build": CMake builds`,
			`    - .github/workflows/ci.yml:8: job build, step "cargo build --locked": Cargo builds without warnings as errors`,
			`    - .github/workflows/ci.yml:9: job build, step "go build ./...": Go builds without warnings as errors: no binding step`,
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("expires %s: output = %q; want it to contain %q", expires, out, want)
			}
		}
	}
}

// The cargo lanes take the level the [lints] tables of the root Cargo.toml's workspace give the
// warnings group (flavor.CargoWarningsLints). Positive: every crate denies them. Negative: a
// member that does not inherit the workspace's lints. Boundary: a cargo command run in another
// directory does not take them.
func TestAuditBuildWarningsReadsTheCargoLints(t *testing.T) {
	workspace := "[workspace]\nmembers = [\"crates/*\"]\n\n[workspace.lints.rust]\nwarnings = \"deny\"\n"
	member := "[package]\nname = \"a\"\nversion = \"0.1.0\"\n"
	cases := map[string]struct {
		files map[string]string
		want  string
		pass  bool
	}{
		"every crate denies": {map[string]string{"Cargo.toml": workspace, "crates/a/Cargo.toml": member + "\n[lints]\nworkspace = true\n",
			".github/workflows/ci.yml": ciSteps("      - run: cargo build --locked\n")}, "(Cargo 1)", true},
		"a member does not inherit": {map[string]string{"Cargo.toml": workspace, "crates/a/Cargo.toml": member,
			".github/workflows/ci.yml": ciSteps("      - run: cargo build --locked\n")}, "crates/a/Cargo.toml does not deny warnings", false},
		"another directory": {map[string]string{"Cargo.toml": workspace, "crates/a/Cargo.toml": member + "\n[lints]\nworkspace = true\n",
			".github/workflows/ci.yml": ciSteps("      - run: cd fuzz && cargo build\n")}, "another directory", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditBuildWarningsIn(t, tc.files)
			if (err == nil) != tc.pass || !strings.Contains(out+fmt.Sprint(err), tc.want) {
				t.Fatalf("AuditBuildWarnings = %q, %v; want pass %t naming %q", out, err, tc.pass, tc.want)
			}
		})
	}
}

// Negative: an expired entry fails like a missing one, naming its expiry; an entry for a workflow
// without a failing lane is stale and fails; a malformed entry fails before anything is read.
// Boundary: with no lane at all, an entry is stale too.
func TestAuditBuildWarningsRefusesExpiredStaleAndMalformedEntries(t *testing.T) {
	failing := map[string]string{".github/workflows/ci.yml": ciSteps("      - run: cargo build\n")}
	cases := map[string]struct {
		files map[string]string
		entry config.Exception
		want  []string
	}{
		"an expired entry": {failing, buildWarningsEntry(".github/workflows/ci.yml", "2026-10-06"),
			[]string{"Cargo builds without warnings as errors",
				"the exceptions entry (rule HISS-10, .github/workflows/ci.yml) expired on 2026-10-06, so the lanes above in .github/workflows/ci.yml fail"}},
		"an entry for another workflow": {failing, buildWarningsEntry(".github/workflows/release.yml", "2026-12-31"),
			[]string{"Cargo builds without warnings as errors", "for .github/workflows/release.yml excuse no lane"}},
		"an entry without a lane": {map[string]string{"README.md": "x\n"}, buildWarningsEntry(".github/workflows/ci.yml", "2026-12-31"),
			[]string{"excuse no lane, because no workflow step builds C, C++, Rust or Go code; remove them"}},
		"a malformed entry": {failing, buildWarningsEntry("ci.yml", "2026-12-31"),
			[]string{"rule HISS-10 must name one workflow file directly in .github/workflows by path, such as .github/workflows/ci.yml"}},
		"an entry past the bound": {failing, buildWarningsEntry(".github/workflows/ci.yml", "2027-01-06"),
			[]string{"more than 90 days after today"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditBuildWarningsIn(t, tc.files, tc.entry)
			if err == nil {
				t.Fatalf("AuditBuildWarnings passed:\n%s", out)
			}
			for i := 0; i < len(tc.want); i++ {
				if !strings.Contains(err.Error(), tc.want[i]) {
					t.Fatalf("failure = %q; want it to contain %q", err, tc.want[i])
				}
			}
		})
	}
}

// Boundary: a repository whose workflows build no C, C++, Rust or Go code skips the gate with
// the reason; a malformed workflow fails closed; a nil context is refused; a failure lists at
// most maxBuildWarningsLanes lanes and counts the rest.
func TestAuditBuildWarningsBoundaries(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no workflows":    {"README.md": "x\n"},
		"no native lanes": {".github/workflows/docs.yml": ciSteps("      - run: npm ci && npm test\n      - run: make docs\n")},
	} {
		out, err := auditBuildWarningsIn(t, files)
		if err != nil || out != "[SKIP] Build warnings (HISS-10) not checked: no run: step under .github/workflows builds C, C++, "+
			"Rust or Go code, so no lane needs warnings as errors." {
			t.Fatalf("%s: AuditBuildWarnings = %q, %v; want the stated skip", name, out, err)
		}
	}
	if _, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": "jobs: [unclosed\n"}); err == nil ||
		!strings.Contains(err.Error(), "cannot read the workflows: workflow ci.yml") {
		t.Fatalf("malformed workflow = %v; want a closed failure naming it", err)
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := AuditBuildWarnings(nil, BuildWarningsOptions{Root: t.TempDir()}); err == nil {
		t.Fatal("AuditBuildWarnings(nil context) passed; want a failure")
	}
	var steps strings.Builder
	for i := 0; i < maxBuildWarningsLanes+3; i++ {
		fmt.Fprintf(&steps, "      - run: gcc -c src/unit%d.c\n", i)
	}
	_, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": ciSteps(steps.String())})
	if err == nil || strings.Count(err.Error(), "gcc/clang builds without warnings as errors") != maxBuildWarningsLanes ||
		!strings.Contains(err.Error(), "3 more lanes build without warnings as errors") {
		t.Fatalf("oversized failure = %v; want %d lanes listed and 3 counted", err, maxBuildWarningsLanes)
	}
}

// Every workflow template a flavor scaffolds passes the gate it is audited by: a fresh adoption
// must not fail its own audit. The Rust template denies warnings for its test build too.
func TestAuditBuildWarningsPassesTheScaffoldedWorkflows(t *testing.T) {
	names, err := templates.Names()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range names {
		if !strings.HasSuffix(name, ".yml.tmpl") || !strings.Contains(name, "/ci-") && !strings.Contains(name, "/security-") {
			continue
		}
		body, err := templates.RenderFile(name, templates.Context{RepoName: "widget", Owner: "acme"})
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": body})
		if err != nil {
			t.Errorf("%s fails the gate it is audited by: %v", name, err)
		}
		if name == "rust/ci-rust.yml.tmpl" && !strings.Contains(out, "2 build lanes fail on a warning (Cargo 2)") {
			t.Errorf("%s: %q; want both cargo steps to fail on a warning", name, out)
		}
		checked++
	}
	if checked < 6 {
		t.Fatalf("checked %d workflow templates; want every ci- and security- template", checked)
	}
}

// HISS-20: the HISS-10 workflow fixtures .config/hiss/coverage.yaml attributes to this gate
// replay in both directions. Positive fixtures fail it, negative ones pass, and gap fixtures
// pass although warnings in them would not fail the run: the gap the catalog records.
func TestAuditBuildWarningsReplaysTheHISS10Fixtures(t *testing.T) {
	buckets := map[string]bool{"positive": true, "negative": false, "gap": false}
	for bucket, wantFail := range buckets {
		entries, err := os.ReadDir(filepath.Join(hiss10Fixtures, bucket))
		if err != nil || len(entries) == 0 {
			t.Fatalf("read %s fixtures: %d entries, %v", bucket, len(entries), err)
		}
		for i := 0; i < len(entries); i++ {
			data, err := os.ReadFile(filepath.Join(hiss10Fixtures, bucket, entries[i].Name()))
			if err != nil {
				t.Fatal(err)
			}
			out, err := auditBuildWarningsIn(t, map[string]string{".github/workflows/ci.yml": string(data)})
			if (err != nil) != wantFail || (!wantFail && !strings.HasPrefix(out, "[PASS] ")) {
				t.Errorf("%s/%s: AuditBuildWarnings = %q, %v; want failure %t", bucket, entries[i].Name(), out, err, wantFail)
			}
		}
	}
}
