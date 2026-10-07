// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

// A lifecycle-owned context (#841, pattern 1) is accepted only in the exact shape: a WithCancel
// context used inside the start function of an fx.Hook whose stop function calls its cancel.

// lifecycleSource wraps body lines in a file that imports context and fx.
func lifecycleSource(imports string, body ...string) string {
	return strings.Join(append([]string{
		"package svc", // 1
		"",            // 2
		"import (",    // 3
		"\t\"context\"",
		"\t" + imports,
		")", // 6
		"",  // 7
		"func Register(lc fx.Lifecycle, mgr Manager) {", // 8
	}, append(body, "}", "")...), "\n")
}

// Negative: the issue's shape, a derivation of the owned context inside the start function, a
// deferred cancel, an aliased import, WithCancelCause, and the stop field written first.
func TestGoIOLifecycle_Negative_OwnedContextInStartIsAccepted(t *testing.T) {
	src := lifecycleSource(`"go.uber.org/fx"`,
		"\tctx, cancel := context.WithCancel(context.Background())",
		"\tlc.Append(fx.Hook{",
		"\t\tOnStart: func(context.Context) error {",
		"\t\t\tgo func() { _ = mgr.Start(ctx) }()",
		"\t\t\tv := context.WithValue(ctx, key, 1)",
		"\t\t\tgo mgr.Run(v)",
		"\t\t\treturn nil",
		"\t\t},",
		"\t\tOnStop: func(stop context.Context) error { cancel(); return mgr.Wait(stop) },",
		"\t})",
		"\tc2, cause := context.WithCancelCause(context.Background())",
		"\tlc.Append(fx.Hook{",
		"\t\tOnStop:  func(context.Context) error { defer cause(nil); return nil },",
		"\t\tOnStart: func(context.Context) error { go mgr.Loop(c2); return nil },",
		"\t})",
	)
	if rep := scanGoIO(t, "svc.go", src); hiss02Count(rep) != 0 {
		t.Fatalf("a lifecycle-owned context used in its start function must be accepted: %+v", rep.Violations)
	}
	aliased := strings.ReplaceAll(lifecycleSource(`app "go.uber.org/fx"`,
		"\tctx, cancel := context.WithCancel(context.Background())",
		"\tlc.Append(app.Hook{",
		"\t\tOnStart: func(context.Context) error { go mgr.Start(ctx); return nil },",
		"\t\tOnStop:  func(context.Context) error { cancel(); return nil },",
		"\t})",
	), "lc fx.Lifecycle", "lc app.Lifecycle")
	if rep := scanGoIO(t, "svc.go", aliased); hiss02Count(rep) != 0 {
		t.Fatalf("an aliased fx import names the same hook: %+v", rep.Violations)
	}
}

// Positive: the near-misses stay reported. The cancel is never called from OnStop; the context is
// used in the constructor body and in the stop function; the stop function calls cancel only in a
// nested block or closure, or through its own parameter of that name; the context comes from
// WithValue, not WithCancel; cancel was rebound before the hook.
func TestGoIOLifecycle_Positive_NearMissesAreReported(t *testing.T) {
	src := lifecycleSource(`"go.uber.org/fx"`,
		"\ta, cancelA := context.WithCancel(context.Background())", // 9
		"\tlc.Append(fx.Hook{", // 10
		"\t\tOnStart: func(context.Context) error { go mgr.Start(a); return nil },", // 11 stop never cancels
		"\t\tOnStop:  func(context.Context) error { return nil },",                  // 12
		"\t})",          // 13
		"\t_ = cancelA", // 14
		"\tb, cancelB := context.WithCancel(context.Background())", // 15
		"\tmgr.Connect(b)",     // 16 constructor body, before the lifecycle starts
		"\tlc.Append(fx.Hook{", // 17
		"\t\tOnStart: func(context.Context) error { go mgr.Start(b); return nil },",                // 18 accepted
		"\t\tOnStop:  func(context.Context) error { _ = mgr.Shutdown(b); cancelB(); return nil },", // 19 stop function
		"\t})", // 20
		"\tc, cancelC := context.WithCancel(context.Background())", // 21
		"\tlc.Append(fx.Hook{", // 22
		"\t\tOnStart: func(context.Context) error { go mgr.Start(c); return nil },",                                      // 23 nested cancel only
		"\t\tOnStop:  func(context.Context) error { if mgr.Up() { cancelC() }; go func() { cancelC() }(); return nil },", // 24
		"\t})", // 25
		"\td, cancelD := context.WithCancel(context.Background())", // 26
		"\tlc.Append(fx.Hook{", // 27
		"\t\tOnStart: func(context.Context) error { go mgr.Start(d); return nil },",   // 28 parameter shadows cancel
		"\t\tOnStop:  func(cancelD context.Context) error { cancelD(); return nil },", // 29
		"\t})", // 30
		"\te := context.WithValue(context.Background(), key, 1)", // 31
		"\tlc.Append(fx.Hook{", // 32
		"\t\tOnStart: func(context.Context) error { go mgr.Start(e); return nil },", // 33 not a WithCancel pair
		"\t\tOnStop:  func(context.Context) error { return nil },",                  // 34
		"\t})", // 35
		"\tf, cancelF := context.WithCancel(context.Background())", // 36
		"\tcancelF = func() {}", // 37
		"\tlc.Append(fx.Hook{",  // 38
		"\t\tOnStart: func(context.Context) error { go mgr.Start(f); return nil },", // 39 cancel rebound
		"\t\tOnStop:  func(context.Context) error { cancelF(); return nil },",       // 40
		"\t})", // 41
	)
	rep := scanGoIO(t, "svc.go", src)
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "svc.go", 11}, {"HISS-02", "svc.go", 16}, {"HISS-02", "svc.go", 19},
		{"HISS-02", "svc.go", 23}, {"HISS-02", "svc.go", 28}, {"HISS-02", "svc.go", 33},
		{"HISS-02", "svc.go", 39}, {"HISS-07", "svc.go", 19},
	})
}

// Boundary: a Hook type of another package named fx is no lifecycle, a local named fx shadows the
// package, and the hook must be keyed; a positional hook literal is not read.
func TestGoIOLifecycle_Boundary_TypeResolutionAndKeyedFields(t *testing.T) {
	other := lifecycleSource(`"example.com/fx"`,
		"\tctx, cancel := context.WithCancel(context.Background())", // 9
		"\tlc.Append(fx.Hook{", // 10
		"\t\tOnStart: func(context.Context) error { go mgr.Start(ctx); return nil },", // 11
		"\t\tOnStop:  func(context.Context) error { cancel(); return nil },",          // 12
		"\t})", // 13
	)
	assertViolations(t, scanGoIO(t, "svc.go", other), []expectedViolation{{"HISS-02", "svc.go", 11}})
	shadowed := lifecycleSource(`"go.uber.org/fx"`,
		"\tctx, cancel := context.WithCancel(context.Background())", // 9
		"\tfx := hooks{}",      // 10
		"\tlc.Append(fx.Hook{", // 11
		"\t\tOnStart: func(context.Context) error { go mgr.Start(ctx); return nil },", // 12
		"\t\tOnStop:  func(context.Context) error { cancel(); return nil },",          // 13
		"\t})", // 14
	)
	assertViolations(t, scanGoIO(t, "svc.go", shadowed), []expectedViolation{{"HISS-02", "svc.go", 12}})
	positional := lifecycleSource(`"go.uber.org/fx"`,
		"\tctx, cancel := context.WithCancel(context.Background())", // 9
		"\tlc.Append(fx.Hook{", // 10
		"\t\tfunc(context.Context) error { go mgr.Start(ctx); return nil },", // 11
		"\t\tfunc(context.Context) error { cancel(); return nil },",          // 12
		"\t})", // 13
	)
	assertViolations(t, scanGoIO(t, "svc.go", positional), []expectedViolation{{"HISS-02", "svc.go", 11}})
}

// TestLifecycleHooksCiteEvidence: every framework shape names both fields and its evidence.
func TestLifecycleHooksCiteEvidence(t *testing.T) {
	if len(lifecycleHooks) == 0 {
		t.Fatal("the lifecycle table must hold the fx hook")
	}
	for fn, hook := range lifecycleHooks {
		if fn.Path == "" || fn.Name == "" || hook.Start == "" || hook.Stop == "" || !strings.Contains(hook.Evidence, ".go") {
			t.Errorf("lifecycle hook %+v = %+v names no fields or no source evidence", fn, hook)
		}
	}
}

// hiss02Count counts a report's HISS-02 findings.
func hiss02Count(rep *ScanReport) int {
	count := 0
	for i := 0; i < len(rep.Violations); i++ {
		if rep.Violations[i].RuleID == "HISS-02" {
			count++
		}
	}
	return count
}
