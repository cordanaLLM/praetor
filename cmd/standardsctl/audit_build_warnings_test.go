package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// fixtureNativeWorkflow is a push workflow whose one job configures and builds with CMake,
// passing configure, the extra configure arguments.
func fixtureNativeWorkflow(configure string) string {
	return "on: push\njobs:\n  native:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - run: cmake -S . -B build" + configure + "\n      - run: cmake --build build\n"
}

// The CLI audit runs the HISS-10 build-warnings gate (#816). Boundary: the fixture has no
// workflow, so the gate skips with the reason. Negative: a CMake lane configured with its
// default fails the audit, naming the workflow, job, step and the form to add. Positive: the
// same lane configured with CMAKE_COMPILE_WARNING_AS_ERROR passes, and so does the failing lane
// while a live HISS-10 entry names its workflow; an expired entry fails it again.
func TestAuditBuildWarnings_CLI_RequiresWarningsAsErrors(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[SKIP] Build warnings (HISS-10) not checked: no run: step under .github/workflows builds C, C++, Rust or Go code")

	writeFixtureFile(t, f.dir, ".github/workflows/native.yml", fixtureNativeWorkflow(""))
	out, err = f.audit(t)
	if err == nil {
		t.Fatalf("audit passed a CMake lane without warnings as errors:\n%s", out)
	}
	mustErrContain(t, err, `[FAIL] Build warnings (HISS-10): .github/workflows/native.yml:6: job native, step "cmake -S . -B build": `+
		"CMake builds without warnings as errors: the configure command defines no CMAKE_COMPILE_WARNING_AS_ERROR. "+
		"Add: configure with -DCMAKE_COMPILE_WARNING_AS_ERROR=ON")

	writeFixtureFile(t, f.dir, ".github/workflows/native.yml", fixtureNativeWorkflow(" -DCMAKE_COMPILE_WARNING_AS_ERROR=ON"))
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed with warnings as errors: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Build warnings (HISS-10): 1 build lanes fail on a warning (CMake 1).")

	writeFixtureFile(t, f.dir, ".github/workflows/native.yml", fixtureNativeWorkflow(""))
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	entry := "exceptions:\n  - rule: HISS-10\n    path: .github/workflows/native.yml\n" +
		"    reason: the vendored codec warns until upstream lands its fix\n    expires: \"%s\"\n"
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+fmt.Sprintf(entry, expires))
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed with a live HISS-10 entry: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "excepted until "+expires+" by the exceptions entry (rule HISS-10, .github/workflows/native.yml)")

	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+fmt.Sprintf(entry, "2020-01-01"))
	if out, err = f.audit(t); err == nil {
		t.Fatalf("audit passed with an expired HISS-10 entry:\n%s", out)
	}
	mustErrContain(t, err, "the exceptions entry (rule HISS-10, .github/workflows/native.yml) expired on 2020-01-01")
}
