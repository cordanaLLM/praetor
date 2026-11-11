// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scanHotSources(t *testing.T, sources map[string]string) []InvariantViolation {
	t.Helper()
	dir := t.TempDir()
	for name, body := range sources {
		path := filepath.Join(dir, filepath.FromSlash(name))
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

func TestGoHotAlloc_Table(t *testing.T) {
	for _, tc := range hotAllocTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			runHotAllocTestCase(t, tc)
		})
	}
}

func TestCheckHotAllocations_Boundary_ParseErrorNotSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(path, []byte("package bad\nfunc ( {"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := newScanReport(10)
	err := checkDirHotAlloc(rep, dir, []string{path})
	if err == nil {
		t.Fatal("checkDirHotAlloc on malformed Go source succeeded, want parse error")
	}
}
