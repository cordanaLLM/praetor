// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// brokenLib is a source file of the nested lib module that does not compile, standing in for a
// package whose C header is missing on the runner.
const brokenLib = "package lib\n\nfunc Connect() int { return \"x\" }\n"

// exceptionsEnvEntry returns the workflow's environment entry for the given exceptions.
func exceptionsEnvEntry(t *testing.T, entries ...ModuleException) []string {
	t.Helper()
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return []string{ExceptionsEnv + "=" + string(encoded)}
}

func libException(expires string) ModuleException {
	return ModuleException{Path: "lib/go.mod", Reason: "needs libudev.h", Expires: expires}
}

// Positive: a module that cannot be built takes a live exception: the gate reports it as not
// compared, with the reason and the expiry, runs the checker on the other modules and passes. The
// exception still holds on its expires day. Boundary: a module that builds is compared although
// an exception names it.
func TestGate_Positive_ExceptedModuleIsReportedNotCompared(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	f.git("tag", "v1.0.0")
	head := f.commit("break the build of lib", map[string]string{"lib/lib.go": brokenLib})
	today := time.Now().UTC().Format("2006-01-02")
	for _, expires := range []string{"2099-12-31", today} {
		run := h.run(t, f.dir, exceptionsEnvEntry(t, libException(expires)), "-base="+base)
		if run.status != 0 {
			t.Fatalf("expires %s: status %d, want 0:\n%s\n%s", expires, run.status, run.stdout, run.stderr)
		}
		requireContains(t, run.stdout, "not compared lib (example.com/widget/lib): excepted until "+expires+": needs libudev.h",
			"compatible   . (example.com/widget)")
		requireContains(t, run.stderr, "warning: module lib (example.com/widget/lib) was not compared")
	}
	if got := h.checked(t); len(got) != 2 || got[0] != ". "+base+" "+head || got[1] != got[0] {
		t.Fatalf("checker runs %q, want the root module only, twice", got)
	}
	f.commit("repair lib", goModule("lib", "example.com/widget/lib", "lib"))
	run := h.run(t, f.dir, exceptionsEnvEntry(t, libException("2099-12-31")), "-base="+base)
	requireContains(t, run.stdout, "compatible   lib (example.com/widget/lib)")
	if got := h.checked(t); len(got) != 4 {
		t.Fatalf("checker runs %q, want lib compared once its build is repaired", got)
	}
}

// Negative: an expired exception, an exception for another module and no exception at all fail
// the gate on the build failure, as before; the failure names an expired entry.
func TestGate_Negative_ExpiredOrMissingExceptionFails(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	f.commit("break the build of lib", map[string]string{"lib/lib.go": brokenLib})
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"expired", exceptionsEnvEntry(t, libException(yesterday)), "the exception for lib/go.mod expired on " + yesterday},
		{"other module", exceptionsEnvEntry(t, ModuleException{Path: "go.mod", Reason: "r", Expires: "2099-12-31"}), "do not build at HEAD"},
		{"none", nil, "do not build at HEAD"},
	}
	for _, tc := range cases {
		run := h.run(t, f.dir, tc.env, "-base="+base)
		if run.status != 2 || !strings.Contains(run.stderr, tc.want) {
			t.Errorf("%s: status %d, want 2 with %q:\n%s\n%s", tc.name, run.status, tc.want, run.stdout, run.stderr)
		}
	}
}

// Boundary: a damaged exceptions variable fails the run instead of reading as no exceptions: not
// JSON, an unknown field, a path that is no go.mod, an empty reason, a date that is no date.
func TestGate_Boundary_DamagedExceptionsFailTheRun(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	f.commit("document", map[string]string{"README.md": "widget\n"})
	for name, raw := range map[string]string{
		"not json":      "lib/go.mod",
		"unknown field": `[{"path":"lib/go.mod","reason":"r","expires":"2099-12-31","glob":"x"}]`,
		"not a go.mod":  `[{"path":"lib/lib.go","reason":"r","expires":"2099-12-31"}]`,
		"empty reason":  `[{"path":"lib/go.mod","reason":" ","expires":"2099-12-31"}]`,
		"bad date":      `[{"path":"lib/go.mod","reason":"r","expires":"soon"}]`,
	} {
		run := h.run(t, f.dir, []string{ExceptionsEnv + "=" + raw}, "-base="+base)
		if run.status != 2 || !strings.Contains(run.stderr, ExceptionsEnv) {
			t.Errorf("%s: status %d, want 2 naming %s:\n%s", name, run.status, ExceptionsEnv, run.stderr)
		}
	}
	empty := h.run(t, f.dir, []string{ExceptionsEnv + "="}, "-base="+base)
	if empty.status != 0 || !slices.Contains(h.checked(t), ". "+base+" "+strings.TrimSpace(f.git("rev-parse", "HEAD"))) {
		t.Fatalf("an empty variable is no exception: status %d:\n%s", empty.status, empty.stderr)
	}
}

// Boundary: the standalone gate restates config.MaxExceptions because it cannot import the
// engine; the two must stay equal, or the gate would refuse a list the manifest admits.
func TestGate_Boundary_ExceptionBoundMatchesConfig(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("gate", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^\s*maxExceptions\s*=\s*(\d+)\s*$`).FindSubmatch(source)
	if match == nil {
		t.Fatal("gate/main.go declares no maxExceptions constant")
	}
	if got := string(match[1]); got != strconv.Itoa(config.MaxExceptions) {
		t.Errorf("gate maxExceptions = %s, want config.MaxExceptions = %d", got, config.MaxExceptions)
	}
}
