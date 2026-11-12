// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func scanHotSources(t *testing.T, sources map[string]string) []InvariantViolation {
	t.Helper()
	dir := t.TempDir()
	for name, body := range sources {
		path := filepath.Join(dir, filepath.FromSlash(name))
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a test directory needs its execute bit; 0o750 is the directory rule's bound
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Scan(context.Background(), dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var violations []InvariantViolation
	for i := 0; i < len(rep.Violations); i++ {
		if rep.Violations[i].RuleID == hotAllocRuleID {
			violations = append(violations, rep.Violations[i])
		}
	}
	return violations
}

type hotAllocTestCase struct {
	name      string
	sources   map[string]string
	wantCount int
	wantSub   string
}

func hotAllocPositiveCases() []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "positive: hot function without test",
			sources: map[string]string{
				"hot.go": "package p\n\n//hiss:hot simulation tick\nfunc Tick() {}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "positive: test without zero assertion",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot rendering frame\nfunc Frame() {}\n",
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestFrame(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Frame() })\n\t_ = allocs\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "positive: zero comparison whose body only logs",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot rendering frame\nfunc Frame() {}\n",
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestFrame(t *testing.T) {\n\tif allocs := testing.AllocsPerRun(100, func() { Frame() }); allocs != 0 {\n\t\tt.Logf(\"allocs %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "positive: method test does not cover same-name function",
			sources: map[string]string{
				"hot.go":      "package p\n\ntype Engine struct{}\n\n//hiss:hot engine step\nfunc (e *Engine) Step() {}\n\n//hiss:hot plain step\nfunc Step() {}\n",
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\te := &Engine{}\n\tif allocs := testing.AllocsPerRun(100, func() { e.Step() }); allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 1,
			wantSub:   "function Step has no testing.AllocsPerRun",
		},
		{
			name: "positive: error logging does not fail test",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot rendering frame\nfunc Frame() {}\n",
				"hot_test.go": "package p\n\nimport (\n\t\"testing\"\n\t\"log\"\n)\n\nfunc TestFrame(t *testing.T) {\n\tvar err error\n\tif allocs := testing.AllocsPerRun(100, func() { Frame() }); allocs != 0 { log.Print(err.Error()) }\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "positive: marker without reason",
			sources: map[string]string{
				"hot.go": "package p\n\n//hiss:hot\nfunc Step() {}\n",
			},
			wantCount: 1,
			wantSub:   "requires a reason",
		},
	}
}

func hotAllocNegativeCases() []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "negative: internal test with != 0",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot physics step\nfunc Step() {}\n",
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 {\n\t\tt.Fatalf(\"allocs: %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "negative: external test with > 0",
			sources: map[string]string{
				"go.mod":      "module example.com/p\n\ngo 1.27\n",
				"hot.go":      "package p\n\n//hiss:hot audio loop\nfunc Audio() {}\n",
				"hot_test.go": "package p_test\n\nimport (\n\t\"testing\"\n\t\"example.com/p\"\n)\n\nfunc TestAudio(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { p.Audio() })\n\tif allocs > 0 {\n\t\tt.Fatalf(\"allocs: %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "gap: hot loop without marker",
			sources: map[string]string{
				"loop.go": "package p\n\nfunc Loop() []byte {\n\tvar b []byte\n\tfor i := 0; i < 100; i++ { b = append(b, byte(i)) }\n\treturn b\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "gap: zero asserted through helper function",
			sources: map[string]string{
				"trans.go":      "package p\n\nfunc Trans(b []byte) []byte { return b }\n",
				"trans_test.go": "package p\n\nimport \"testing\"\n\nfunc assertZero(t *testing.T, f func()) {\n\tif allocs := testing.AllocsPerRun(50, f); allocs != 0 { t.Fatal(allocs) }\n}\nfunc TestTrans(t *testing.T) { assertZero(t, func() { Trans(nil) }) }\n",
			},
			wantCount: 0,
		},
	}
}

func hotAllocBoundaryCases() []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "boundary: method on pointer receiver",
			sources: map[string]string{
				"engine.go":      "package p\n\ntype Engine struct{}\n\n//hiss:hot engine step\nfunc (e *Engine) Step() {}\n",
				"engine_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\te := &Engine{}\n\tallocs := testing.AllocsPerRun(100, func() { e.Step() })\n\tif allocs != 0 {\n\t\tt.Fatalf(\"allocs: %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "boundary: generic function",
			sources: map[string]string{
				"gen.go":      "package p\n\n//hiss:hot generic push\nfunc Push[T any](q []T, x T) []T { return q }\n",
				"gen_test.go": "package p\n\nimport \"testing\"\n\nfunc TestPush(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Push[int](nil, 1) })\n\tif allocs != 0 {\n\t\tt.Fatalf(\"allocs: %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "boundary: marker on non-function",
			sources: map[string]string{
				"cfg.go": "package p\n\n//hiss:hot constants\nconst MaxTicks = 100\n",
			},
			wantCount: 1,
			wantSub:   "ignored on non-function",
		},
		{
			name: "boundary: AllocsPerRun with runs 0",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot run step\nfunc Run() {}\n",
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tallocs := testing.AllocsPerRun(0, func() { Run() })\n\tif allocs != 0 {\n\t\tt.Fatalf(\"allocs: %v\", allocs)\n\t}\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
	}
}

func hotAllocTestCases() []hotAllocTestCase {
	cases := append([]hotAllocTestCase{}, hotAllocPositiveCases()...)
	cases = append(cases, hotAllocNegativeCases()...)
	return append(cases, hotAllocBoundaryCases()...)
}

func runHotAllocTestCase(t *testing.T, tc hotAllocTestCase) {
	t.Helper()
	violations := scanHotSources(t, tc.sources)
	if len(violations) != tc.wantCount {
		t.Fatalf("scanHotSources count = %d, want %d (violations: %+v)", len(violations), tc.wantCount, violations)
	}
	if tc.wantSub != "" && len(violations) > 0 {
		if !strings.Contains(violations[0].Message, tc.wantSub) {
			t.Fatalf("violation message %q does not contain %q", violations[0].Message, tc.wantSub)
		}
	}
}

// TestGoHotAlloc_Table replays positive, negative and boundary cases for hot-path allocation checks.
func TestGoHotAlloc_Table(t *testing.T) {
	for _, tc := range hotAllocTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			runHotAllocTestCase(t, tc)
		})
	}
}

// TestCheckHotAllocations_Boundary_ParseErrorNotSkipped checks that an invalid source file fails the allocation scan instead of being skipped.
func TestCheckHotAllocations_Boundary_ParseErrorNotSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(path, []byte("package bad\nfunc ( {"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := newScanReport(10)
	err := checkDirHotAlloc(context.Background(), rep, dir, []string{path})
	if err == nil {
		t.Fatal("checkDirHotAlloc on malformed Go source succeeded, want parse error")
	}
}

func TestHotAllocFinishUnreadableFileIncomplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.go")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a test directory needs its execute bit; 0o750 is the directory rule's bound
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	rep := newScanReport(10)
	(goLanguage{}).finish(context.Background(), rep, dir, []string{path})
	if !rep.Incomplete() {
		t.Fatal("unreadable hot-allocation source left report complete")
	}
}

type stepCancelCtx struct {
	context.Context
	cancel      context.CancelFunc
	calls       atomic.Int32
	cancelAfter int32
}

func (c *stepCancelCtx) Err() error {
	if c.calls.Add(1) >= c.cancelAfter {
		c.cancel()
	}
	return c.Context.Err()
}

// TestCheckHotAllocations_Negative_SecondPassHonorsContext verifies that both second-pass
// file parses (collectHotTargets and findZeroAllocTests) read under ctx and propagate cancellation.
func TestCheckHotAllocations_Negative_SecondPassHonorsContext(t *testing.T) {
	dir := t.TempDir()
	hotPath := filepath.Join(dir, "hot.go")
	testPath := filepath.Join(dir, "hot_test.go")
	if err := os.WriteFile(hotPath, []byte("package p\n\n//hiss:hot tick\nfunc Tick() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("package p\n\nimport \"testing\"\n\nfunc TestTick(t *testing.T) {\n\tif a := testing.AllocsPerRun(10, func() { Tick() }); a != 0 { t.Fatal(a) }\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		cancelAfter int32
	}{
		{"collectHotTargets", 2},
		{"findZeroAllocTests", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &stepCancelCtx{Context: inner, cancel: cancel, cancelAfter: tc.cancelAfter}
			rep := newScanReport(10)
			err := checkHotAllocations(ctx, rep, dir, []string{hotPath, testPath})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("checkHotAllocations err = %v, want context.Canceled", err)
			}
		})
	}
}

// hotAllocAliasCases builds test cases exercising aliased, dot and blank testing imports.
func hotAllocAliasCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "aliased *tt.T credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport tt \"testing\"\n\nfunc TestStep(t *tt.T) {\n\tallocs := tt.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "aliased *bench.B and bench.TB credited",
			sources: map[string]string{
				"hot.go":      "package p\n\n//hiss:hot step\nfunc Step() {}\n\n//hiss:hot tick\nfunc Tick() {}\n",
				"hot_test.go": "package p\n\nimport bench \"testing\"\n\nfunc BenchmarkStep(b *bench.B) {\n\tif a := bench.AllocsPerRun(10, func() { Step() }); a != 0 { b.Fatal(a) }\n\tcheckTick(b)\n}\nfunc checkTick(tb bench.TB) {\n\tif a := bench.AllocsPerRun(10, func() { Tick() }); a != 0 { tb.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "dot import binds no testing qualifier",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport . \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "blank import binds no testing qualifier",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport _ \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
	}
}

// TestGoHotAlloc_AliasedTestingImport verifies per-file local bindings to import "testing".
func TestGoHotAlloc_AliasedTestingImport(t *testing.T) {
	for _, tc := range hotAllocAliasCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

func hotAllocBindingCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "reassigned before assertion",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tallocs = 0\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "compound assign before assertion",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tallocs -= allocs\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "nested short declaration shadows binding",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\t_ = allocs\n\t{\n\t\tallocs := 0\n\t\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "if init declaration shadows binding",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\t_ = allocs\n\tif allocs := 0; allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control plain measurement then assertion",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tother := 1\n\t_ = other\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_ReassignedOrShadowedAllocs verifies assertions after reassignment or shadowing are not credited.
func TestGoHotAlloc_ReassignedOrShadowedAllocs(t *testing.T) {
	for _, tc := range hotAllocBindingCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

func hotAllocZeroLiteralCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "call budget(0) not literal zero",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc budget(v float64) float64 { return v + 4 }\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != budget(0) { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "qualified call pkg.F(0) not literal zero",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport (\n\t\"math\"\n\t\"testing\"\n)\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != math.Abs(0) { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "named type conversion not literal zero",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\ntype budget float64\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != float64(budget(0)) { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control float64(0) credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != float64(0) { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_ZeroLiteralUnwrap verifies only predeclared numeric conversions unwrap around zero.
func TestGoHotAlloc_ZeroLiteralUnwrap(t *testing.T) {
	for _, tc := range hotAllocZeroLiteralCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// hotAllocEntryNegativeCases builds cases where assertions sit outside reachable test entries.
func hotAllocEntryNegativeCases(hotSrc string) []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "uncalled helper with *testing.T not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc checkStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "lower-case suffix on Test prefix not an entry point",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc Teststep(t *testing.T) {\n\tif a := testing.AllocsPerRun(10, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "method named TestHot not in reach set",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\ntype suite struct{}\nfunc (suite) TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(10, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
	}
}

// hotAllocEntryControlCases builds reachable helper and entry-point control cases.
func hotAllocEntryControlCases(hotSrc string) []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "control helper called from TestHot credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc checkStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n}\nfunc TestHot(t *testing.T) { checkStep(t) }\n",
			},
			wantCount: 0,
		},
		{
			name: "control two-hop helper chain across package files credited",
			sources: map[string]string{
				"hot.go":         hotSrc,
				"helper_test.go": "package p\n\nimport \"testing\"\n\nfunc leafStep(tb testing.TB) {\n\tif a := testing.AllocsPerRun(10, func() { Step() }); a != 0 { tb.Fatal(a) }\n}\nfunc midStep(t *testing.T) { leafStep(t) }\n",
				"hot_test.go":    "package p\n\nimport \"testing\"\n\nfunc TestHot(t *testing.T) { midStep(t) }\n",
			},
			wantCount: 0,
		},
		{
			name: "control BenchmarkHot entry credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc BenchmarkHot(b *testing.B) {\n\tif a := testing.AllocsPerRun(10, func() { Step() }); a != 0 { b.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "control FuzzHot entry credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc FuzzHot(f *testing.F) {\n\tif a := testing.AllocsPerRun(10, func() { Step() }); a != 0 { f.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_EntryReachability verifies only Go test entry points and reachable helpers earn credit.
func TestGoHotAlloc_EntryReachability(t *testing.T) {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	cases := append(hotAllocEntryNegativeCases(hotSrc), hotAllocEntryControlCases(hotSrc)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// hotAllocFuncLitCases builds uncalled-closure negative cases and invoked-literal controls.
func hotAllocFuncLitCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "assertion inside uncalled func literal variable not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tcheck := func() {\n\t\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n\t}\n\t_ = check\n}\n",
			},
			wantCount: 1,
			wantSub:   "has no testing.AllocsPerRun test asserting zero allocations",
		},
		{
			name: "full check inside uncalled func literal variable not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tcheck := func() {\n\t\tif a := testing.AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n\t}\n\t_ = check\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control immediately invoked func literal credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tfunc() {\n\t\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n\t}()\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "control t.Run subtest literal credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tt.Run(\"zero\", func(t *testing.T) {\n\t\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\t\tif allocs != 0 { t.Fatalf(\"allocs: %v\", allocs) }\n\t})\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_FuncLitInvocation verifies uncalled function literals earn no allocation credit.
func TestGoHotAlloc_FuncLitInvocation(t *testing.T) {
	for _, tc := range hotAllocFuncLitCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

const (
	maxSyntheticHotPaths = maxHotAllocFiles + 2
	maxSyntheticHotDecls = maxHotAllocDecls + 2
)

// buildSyntheticHotPaths creates n synthetic _test.go paths in one or n directories.
func buildSyntheticHotPaths(root string, n int, separateDirs bool) []string {
	paths := make([]string, 0, n)
	for i := 0; i < n && i < maxSyntheticHotPaths; i++ {
		idx := strconv.Itoa(i)
		if separateDirs {
			paths = append(paths, filepath.Join(root, "d"+idx, "x_test.go"))
		} else {
			paths = append(paths, filepath.Join(root, "f"+idx+"_test.go"))
		}
	}
	return paths
}

// buildRepeatedGoFuncs builds a Go file with prefix and count trivial functions starting at offset.
func buildRepeatedGoFuncs(prefix, fnPrefix, sig string, offset, count int) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := 0; i < count && i < maxSyntheticHotDecls; i++ {
		b.WriteString("func ")
		b.WriteString(fnPrefix)
		b.WriteString(strconv.Itoa(offset + i))
		b.WriteString(sig)
		b.WriteString(" {}\n")
	}
	return b.String()
}

// TestGoHotAlloc_CapTruncation_PathsAndDirs verifies file and directory caps mark Truncated only past the limit.
func TestGoHotAlloc_CapTruncation_PathsAndDirs(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name         string
		count        int
		separateDirs bool
		wantTrunc    bool
	}{
		{"paths at cap", maxHotAllocFiles, false, false},
		{"paths over cap", maxHotAllocFiles + 1, false, true},
		{"dirs at cap", maxHotAllocFiles, true, false},
		{"dirs over cap", maxHotAllocFiles + 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := newScanReport(10)
			paths := buildSyntheticHotPaths(root, tc.count, tc.separateDirs)
			if err := checkHotAllocations(context.Background(), rep, root, paths); err != nil {
				t.Fatalf("checkHotAllocations err = %v", err)
			}
			if rep.Truncated != tc.wantTrunc {
				t.Fatalf("rep.Truncated = %v, want %v", rep.Truncated, tc.wantTrunc)
			}
		})
	}
}

// runHotAllocSourcesReport writes sources to a temporary root and runs checkHotAllocations directly.
func runHotAllocSourcesReport(t *testing.T, sources map[string]string) *ScanReport {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(sources))
	for name, body := range sources {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	rep := newScanReport(10)
	if err := checkHotAllocations(context.Background(), rep, root, paths); err != nil {
		t.Fatalf("checkHotAllocations err = %v", err)
	}
	return rep
}

// TestGoHotAlloc_CapTruncation_DeclsAndTestFuncs verifies declaration and test-function caps mark Truncated only when dropping input.
func TestGoHotAlloc_CapTruncation_DeclsAndTestFuncs(t *testing.T) {
	const hotFile = "package p\n\n//hiss:hot tick\nfunc Tick() {}\n"
	const testHdr = "package p\n\nimport \"testing\"\n\n"
	const tickTest = "func TestTick(t *testing.T) {\n\tif a := testing.AllocsPerRun(10, func() { Tick() }); a != 0 { t.Fatal(a) }\n}\n"
	half := maxHotAllocDecls / 2
	for _, tc := range []struct {
		name      string
		sources   map[string]string
		wantTrunc bool
	}{
		{"prod decls at cap", map[string]string{"p.go": buildRepeatedGoFuncs("package p\n\n", "F", "()", 0, maxHotAllocDecls)}, false},
		{"prod decls over cap", map[string]string{"p.go": buildRepeatedGoFuncs("package p\n\n", "F", "()", 0, maxHotAllocDecls+1)}, true},
		{"test decls at cap", map[string]string{"hot.go": hotFile, "a_test.go": buildRepeatedGoFuncs(testHdr+tickTest, "TestF", "(t *testing.T)", 0, maxHotAllocDecls-2)}, false},
		{"test decls over cap", map[string]string{"hot.go": hotFile, "a_test.go": buildRepeatedGoFuncs(testHdr+tickTest, "TestF", "(t *testing.T)", 0, maxHotAllocDecls-1)}, true},
		{"test funcs across files at cap", map[string]string{
			"hot.go":    hotFile,
			"a_test.go": buildRepeatedGoFuncs(testHdr+tickTest, "TestA", "(t *testing.T)", 0, half-1),
			"b_test.go": buildRepeatedGoFuncs(testHdr, "TestB", "(t *testing.T)", 0, half) + "func plainHelper() {}\n",
		}, false},
		{"test funcs across files over cap", map[string]string{
			"hot.go":    hotFile,
			"a_test.go": buildRepeatedGoFuncs(testHdr+tickTest, "TestA", "(t *testing.T)", 0, half),
			"b_test.go": buildRepeatedGoFuncs(testHdr, "TestB", "(t *testing.T)", 0, half),
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := runHotAllocSourcesReport(t, tc.sources)
			if rep.Truncated != tc.wantTrunc {
				t.Fatalf("rep.Truncated = %v, want %v", rep.Truncated, tc.wantTrunc)
			}
		})
	}
}

// countHotViolationsInFile counts HISS-03 violations recorded against wantFile.
func countHotViolationsInFile(rep *ScanReport, wantFile string) int {
	count := 0
	for i := 0; i < len(rep.Violations) && i < maxHotAllocFiles; i++ {
		if rep.Violations[i].RuleID == hotAllocRuleID && rep.Violations[i].FilePath == wantFile {
			count++
		}
	}
	return count
}

// writeUnreadableFirstDirTree builds two sibling directories where a/hot_test.go cannot be read as a file.
func writeUnreadableFirstDirTree(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a test directory needs its execute bit; 0o750 is the directory rule's bound
	if err := os.MkdirAll(filepath.Join(aDir, "hot_test.go"), 0o750); err != nil {
		t.Fatal(err)
	}
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a test directory needs its execute bit; 0o750 is the directory rule's bound
	if err := os.MkdirAll(bDir, 0o750); err != nil {
		t.Fatal(err)
	}
	aHot := filepath.Join(aDir, "hot.go")
	bHot := filepath.Join(bDir, "hot.go")
	if err := os.WriteFile(aHot, []byte("package a\n\n//hiss:hot tick\nfunc Tick() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bHot, []byte("package b\n\n//hiss:hot step\nfunc Step() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, []string{aHot, filepath.Join(aDir, "hot_test.go"), bHot}
}

// TestGoHotAlloc_DirErrorContinuesToLaterDirs verifies one directory failure marks the report incomplete while later directories still report HISS-03.
func TestGoHotAlloc_DirErrorContinuesToLaterDirs(t *testing.T) {
	t.Run("unparsable test file in first dir via Scan", func(t *testing.T) {
		violations := scanHotSourcesIncomplete(t, map[string]string{
			"a/hot.go":      "package a\n\n//hiss:hot tick\nfunc Tick() {}\n",
			"a/hot_test.go": "package a\nfunc TestBroken( {",
			"b/hot.go":      "package b\n\n//hiss:hot step\nfunc Step() {}\n",
		}, "b/hot.go")
		if violations != 1 {
			t.Fatalf("b/hot.go HISS-03 violations = %d, want 1", violations)
		}
	})
	t.Run("unreadable test file in first dir via checkHotAllocations", func(t *testing.T) {
		root, paths := writeUnreadableFirstDirTree(t)
		rep := newScanReport(10)
		if err := checkHotAllocations(context.Background(), rep, root, paths); err != nil {
			t.Fatalf("checkHotAllocations err = %v, want nil", err)
		}
		if !rep.Incomplete() || rep.CompletionError() == nil {
			t.Fatalf("expected incomplete report after unreadable dir, got skips=%+v", rep.Skips)
		}
		if got := countHotViolationsInFile(rep, "b/hot.go"); got != 1 {
			t.Fatalf("b/hot.go HISS-03 violations = %d, want 1", got)
		}
	})
}

// scanHotSourcesIncomplete runs Scan on sources, asserts the report is incomplete, and counts HISS-03 findings in wantFile.
func scanHotSourcesIncomplete(t *testing.T, sources map[string]string, wantFile string) int {
	t.Helper()
	dir := t.TempDir()
	for name, body := range sources {
		path := filepath.Join(dir, filepath.FromSlash(name))
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a test directory needs its execute bit; 0o750 is the directory rule's bound
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Scan(context.Background(), dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Incomplete() || rep.CompletionError() == nil {
		t.Fatalf("expected incomplete report, got skips=%+v", rep.Skips)
	}
	return countHotViolationsInFile(rep, wantFile)
}

// hotAllocIdentResolutionCases builds cases resolving AllocsPerRun against testing imports.
func hotAllocIdentResolutionCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "local AllocsPerRun function not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc AllocsPerRun(n int, f func()) float64 { f(); return 0 }\nfunc TestStep(t *testing.T) {\n\tif a := AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "unrelated package AllocsPerRun selector not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport (\n\t\"testing\"\n\t\"example.com/other\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := other.AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "aliased tt.AllocsPerRun credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport tt \"testing\"\n\nfunc TestStep(t *tt.T) {\n\tif a := tt.AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "dot-imported testing AllocsPerRun credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport (\n\t\"testing\"\n\t. \"testing\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_AllocsPerRunImportResolution verifies only testing.AllocsPerRun counts as a measurement.
func TestGoHotAlloc_AllocsPerRunImportResolution(t *testing.T) {
	for _, tc := range hotAllocIdentResolutionCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// hotAllocRunsArgCases builds negative and positive measurement-count cases for AllocsPerRun.
func hotAllocRunsArgCases() []hotAllocTestCase {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	return []hotAllocTestCase{
		{
			name: "runs -1 not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(-1, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "parenthesized unary minus (-n) not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tn := 10\n\tif a := testing.AllocsPerRun((-n), func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "runs 0 not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(0, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "runs 0x0 not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(0x0, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "overflowing integer literal not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(99999999999999999999999999999, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control runs 100 credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_RunsArgValidation verifies non-positive, unary-minus and unparsable run counts are rejected.
func TestGoHotAlloc_RunsArgValidation(t *testing.T) {
	for _, tc := range hotAllocRunsArgCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// hotAllocQualifiedCalleeCases builds external and internal package-qualified callee cases.
func hotAllocQualifiedCalleeCases() []hotAllocTestCase {
	const subHot = "package sub\n\n//hiss:hot step\nfunc Step() {}\n"
	const goMod = "module example.com/mod\n\ngo 1.27\n"
	return []hotAllocTestCase{
		{
			name: "external test calling unrelated otherpkg.Step not credited",
			sources: map[string]string{
				"go.mod":          goMod,
				"sub/hot.go":      subHot,
				"sub/hot_test.go": "package sub_test\n\nimport (\n\t\"testing\"\n\totherpkg \"example.com/other\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { otherpkg.Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "external test calling same-basename foreign import not credited",
			sources: map[string]string{
				"go.mod":          goMod,
				"sub/hot.go":      subHot,
				"sub/hot_test.go": "package sub_test\n\nimport (\n\t\"testing\"\n\t\"other.com/sub\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { sub.Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "internal test calling package-qualified Step not credited",
			sources: map[string]string{
				"go.mod":          goMod,
				"sub/hot.go":      subHot,
				"sub/hot_test.go": "package sub\n\nimport (\n\t\"testing\"\n\tprodpkg \"example.com/mod/sub\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { prodpkg.Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "external test without go.mod fails closed",
			sources: map[string]string{
				"sub/hot.go":      subHot,
				"sub/hot_test.go": "package sub_test\n\nimport (\n\t\"testing\"\n\t\"example.com/mod/sub\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { sub.Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control external test calling prodpkg.Step credited",
			sources: map[string]string{
				"go.mod":          goMod,
				"sub/hot.go":      subHot,
				"sub/hot_test.go": "package sub_test\n\nimport (\n\t\"testing\"\n\tprodpkg \"example.com/mod/sub\"\n)\n\nfunc TestStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { prodpkg.Step() }); a != 0 { t.Fatal(a) }\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_QualifiedCalleeResolution verifies package-qualified calls credit local functions only via the production import path in external tests.
func TestGoHotAlloc_QualifiedCalleeResolution(t *testing.T) {
	for _, tc := range hotAllocQualifiedCalleeCases() {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// hotAllocConditionalAssertionCases builds cases where the zero-allocation assertion sits under a
// conditional, looping or async frame between the measurement and the check.
func hotAllocConditionalAssertionCases(hotSrc string) []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "zero check inside if branch not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tverbose := false\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif verbose {\n\t\tif allocs != 0 { t.Fatal(allocs) }\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "zero check inside switch case not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tmode := 0\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tswitch mode {\n\tcase 1:\n\t\tif allocs != 0 { t.Fatal(allocs) }\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "zero check inside for loop not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tn := 0\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tfor i := 0; i < n; i++ {\n\t\tif allocs != 0 { t.Fatal(allocs) }\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "zero check inside go or defer literal not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tgo func() {\n\t\tif allocs != 0 { t.Fatal(allocs) }\n\t}()\n\tdefer func() {\n\t\tif allocs != 0 { t.Fatal(allocs) }\n\t}()\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "conditional rebinding before top-level zero check not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tverbose := true\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif verbose { allocs = 0 }\n\tif allocs != 0 { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 1,
		},
	}
}

// hotAllocConditionalFatalCases builds cases where the failure call inside a zero-check body sits
// under a conditional, looping or async frame.
func hotAllocConditionalFatalCases(hotSrc string) []hotAllocTestCase {
	return []hotAllocTestCase{
		{
			name: "t.Fatal reachable only under switch case not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tmode := 0\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 {\n\t\tswitch mode {\n\t\tcase 1:\n\t\t\tt.Fatal(allocs)\n\t\t}\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "t.Fatal reachable only under nested if not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tstrict := false\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 {\n\t\tif strict { t.Fatal(allocs) }\n\t}\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "t.Fatal inside select or defer not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tch := make(chan struct{})\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif allocs != 0 {\n\t\tselect {\n\t\tcase <-ch:\n\t\t\tt.Fatal(allocs)\n\t\tdefault:\n\t\t}\n\t\tdefer t.Fatal(allocs)\n\t}\n}\n",
			},
			wantCount: 1,
		},
	}
}

// hotAllocConditionalHelperAndControlCases builds conditional helper-call positive cases and
// unconditional controls.
func hotAllocConditionalHelperAndControlCases(hotSrc string) []hotAllocTestCase {
	const helperDecl = "func checkStep(t *testing.T) {\n\tif a := testing.AllocsPerRun(100, func() { Step() }); a != 0 { t.Fatal(a) }\n}\n"
	return []hotAllocTestCase{
		{
			name: "helper call inside if branch not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\n" + helperDecl + "func TestStep(t *testing.T) {\n\tverbose := false\n\tif verbose { checkStep(t) }\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "helper call inside switch for go or defer not credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\n" + helperDecl + "func TestStep(t *testing.T) {\n\tmode, n := 0, 0\n\tswitch mode { case 1: checkStep(t) }\n\tfor i := 0; i < n; i++ { checkStep(t) }\n\tgo checkStep(t)\n\tdefer checkStep(t)\n}\n",
			},
			wantCount: 1,
		},
		{
			name: "control non-rebinding if before top-level zero check credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\nfunc TestStep(t *testing.T) {\n\tverbose := false\n\tallocs := testing.AllocsPerRun(100, func() { Step() })\n\tif verbose { t.Log(allocs) }\n\tif allocs != 0 { t.Fatal(allocs) }\n}\n",
			},
			wantCount: 0,
		},
		{
			name: "control unconditional helper call inside t.Run credited",
			sources: map[string]string{
				"hot.go":      hotSrc,
				"hot_test.go": "package p\n\nimport \"testing\"\n\n" + helperDecl + "func TestStep(t *testing.T) {\n\tt.Run(\"sub\", func(t *testing.T) { checkStep(t) })\n}\n",
			},
			wantCount: 0,
		},
	}
}

// TestGoHotAlloc_ConditionalAndAsyncFrames verifies zero assertions, failure calls and helper calls
// earn credit only when every frame between the measurement or entry and the call is unconditional.
func TestGoHotAlloc_ConditionalAndAsyncFrames(t *testing.T) {
	const hotSrc = "package p\n\n//hiss:hot step\nfunc Step() {}\n"
	cases := append(hotAllocConditionalAssertionCases(hotSrc), hotAllocConditionalFatalCases(hotSrc)...)
	cases = append(cases, hotAllocConditionalHelperAndControlCases(hotSrc)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runHotAllocTestCase(t, tc) })
	}
}

// TestGoHotAlloc_Boundary_OwnComplexityWithinHISS04 checks that go_hot_alloc.go stays within HISS-04 caps.
func TestGoHotAlloc_Boundary_OwnComplexityWithinHISS04(t *testing.T) {
	assertFileComplexityWithinHISS04(t, filepath.Join("go_hot_alloc.go"))
}
