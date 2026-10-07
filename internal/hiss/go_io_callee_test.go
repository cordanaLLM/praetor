// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"strings"
	"testing"
)

// Callee-bounded calls (#841, pattern 3): a deadline-free context handed to a function of the
// same module is accepted when that function bounds it before anything else uses it.

// calleeFile is a Go file of package p that imports context and time.
func calleeFile(lines ...string) string {
	return strings.Join(append([]string{
		"package p", // 1
		"",          // 2
		"import (",  // 3
		"\t\"context\"",
		"\t\"time\"",
		")", // 6
		"",  // 7
	}, append(lines, "")...), "\n")
}

// Negative: a callee that rebinds its parameter to a timeout first, one that derives a new name
// and never uses the parameter again, unnamed and blank parameters, a helper that only selects
// on ctx.Done(), a method of the receiver, a two-step chain, and a callee in another package of
// the module stay silent.
func TestGoIOCallee_Negative_CalleesThatBoundTheirContext(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.27\n")
	writeFixture(t, root, "p/p.go", calleeFile(
		"type S struct{ timeout time.Duration }",
		"",
		"func (s *S) Calls(ch chan int) {",
		"\tconnect(context.Background(), time.Second)",
		"\tfresh(context.Background())",
		"\tunnamed(context.Background())",
		"\tblank(context.Background())",
		"\t_ = send(context.Background(), ch, 1)",
		"\ts.start(context.Background())",
		"\toutside(context.Background())",
		"}",
		"",
		"func connect(ctx context.Context, d time.Duration) error {",
		"\tctx, cancel := context.WithTimeout(ctx, d)",
		"\tdefer cancel()",
		"\treturn dial(ctx)",
		"}",
		"",
		"func fresh(ctx context.Context) error {",
		"\tbounded, cancel := context.WithDeadline(ctx, time.Now().Add(time.Second))",
		"\tdefer cancel()",
		"\treturn dial(bounded)",
		"}",
		"",
		"func unnamed(context.Context) {}",
		"",
		"func blank(_ context.Context) {}",
		"",
		"func send(ctx context.Context, ch chan<- int, v int) error {",
		"\tselect {",
		"\tcase ch <- v:",
		"\t\treturn nil",
		"\tcase <-ctx.Done():",
		"\t\treturn ctx.Err()",
		"\t}",
		"}",
		"",
		"func (s *S) start(ctx context.Context) { s.connect(ctx) }",
		"",
		"func (s *S) connect(ctx context.Context) {",
		"\tctx, cancel := context.WithTimeout(ctx, s.timeout)",
		"\tdefer cancel()",
		"\tdial(ctx)",
		"}",
	))
	writeFixture(t, root, "p/imports.go", strings.Join([]string{
		"package p",
		"",
		"import (",
		"\t\"context\"",
		"",
		"\tq \"example.com/app/store\"",
		")",
		"",
		"func outside(ctx context.Context) { q.Open(ctx) }",
		"",
	}, "\n"))
	writeFixture(t, root, "store/store.go", calleeFile(
		"func Open(ctx context.Context) error {",
		"\tctx, cancel := context.WithTimeout(ctx, time.Minute)",
		"\tdefer cancel()",
		"\treturn dial(ctx)",
		"}",
	))
	if rep := scanFixture(t, root, ScanOptions{}); hiss02Count(rep) != 0 {
		t.Fatalf("callees that bound their context must be accepted: %+v", rep.Violations)
	}
}

// Positive: the near-misses stay reported. The callee does I/O before deriving its timeout,
// derives it only on one branch, stores the context, hands it to a function outside the module,
// derives it by WithCancel, takes it variadically, or is declared twice in the package.
func TestGoIOCallee_Positive_CalleesThatDoNotBoundIt(t *testing.T) {
	src := calleeFile(
		"func Calls(s *S) {",                           // 8
		"\tioFirst(context.Background(), time.Second)", // 9
		"\tbranch(context.Background(), time.Second)",  // 10
		"\ts.keep(context.Background())",               // 11
		"\tforeign(context.Background())",              // 12
		"\tinherit(context.Background())",              // 13
		"\tspread(context.Background())",               // 14
		"}",                                            // 15
		"",                                             // 16
		"func ioFirst(ctx context.Context, d time.Duration) error {", // 17
		"\tif err := ping(ctx); err != nil {",                        // 18
		"\t\treturn err",                                             // 19
		"\t}",                                                        // 20
		"\tctx, cancel := context.WithTimeout(ctx, d)",               // 21
		"\tdefer cancel()",                                           // 22
		"\treturn ping(ctx)",                                         // 23
		"}",                                                          // 24
		"",                                                           // 25
		"func branch(ctx context.Context, d time.Duration) error {", // 26
		"\tif d > 0 {",                                  // 27
		"\t\tvar cancel context.CancelFunc",             // 28
		"\t\tctx, cancel = context.WithTimeout(ctx, d)", // 29
		"\t\tdefer cancel()",                            // 30
		"\t}",                                           // 31
		"\treturn ping(ctx)",                            // 32
		"}",                                             // 33
		"",                                              // 34
		"type S struct{ ctx context.Context }",          // 35
		"",                                              // 36
		"func (s *S) keep(ctx context.Context) { s.ctx = ctx }", // 37
		"", // 38
		"func foreign(ctx context.Context) { grpc.Dial(ctx) }", // 39
		"",                                    // 40
		"func inherit(ctx context.Context) {", // 41
		"\tctx, cancel := context.WithCancel(ctx)", // 42
		"\tdefer cancel()",                         // 43
		"\tping(ctx)",                              // 44
		"}",                                        // 45
		"",                                         // 46
		"func spread(ctxs ...context.Context) {}", // 47
	)
	rep := scanGoIO(t, "p.go", src)
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "p.go", 9}, {"HISS-02", "p.go", 10}, {"HISS-02", "p.go", 11},
		{"HISS-02", "p.go", 12}, {"HISS-02", "p.go", 13}, {"HISS-02", "p.go", 14},
	})
	root := t.TempDir()
	twice := calleeFile("func open(ctx context.Context) {", "\tctx, cancel := context.WithTimeout(ctx, time.Second)", "\tdefer cancel()", "}")
	writeFixture(t, root, "p/open_linux.go", twice)
	writeFixture(t, root, "p/open_other.go", strings.Replace(twice, "context.WithTimeout(ctx, time.Second)", "context.WithCancel(ctx)", 1))
	writeFixture(t, root, "p/call.go", "package p\n\nimport \"context\"\n\nfunc Call() { open(context.Background()) }\n")
	assertViolations(t, scanFixture(t, root, ScanOptions{}), []expectedViolation{{"HISS-02", "p/call.go", 5}})
}

// chainSource is a package whose Start passes context.Background() down a chain of depth
// callees, the last of which derives the timeout.
func chainSource(depth int) string {
	lines := []string{"func Start() { step1(context.Background()) }", ""}
	for i := 1; i < depth; i++ {
		lines = append(lines, fmt.Sprintf("func step%d(ctx context.Context) { step%d(ctx) }", i, i+1), "")
	}
	lines = append(lines,
		fmt.Sprintf("func step%d(ctx context.Context) {", depth),
		"\tctx, cancel := context.WithTimeout(ctx, time.Second)",
		"\tdefer cancel()",
		"\tdial(ctx)",
		"}",
	)
	return calleeFile(lines...)
}

// Boundary: a chain whose timeout is derived exactly maxCalleeDepth calls deep is accepted and
// one a call deeper is reported, a cycle between callees fails the proof, and a callee in a
// nested module, or in no module, is outside the module.
func TestGoIOCallee_Boundary_DepthCyclesAndModules(t *testing.T) {
	if rep := scanGoIO(t, "p.go", chainSource(maxCalleeDepth)); hiss02Count(rep) != 0 {
		t.Fatalf("a timeout %d calls deep is within the bound: %+v", maxCalleeDepth, rep.Violations)
	}
	assertViolations(t, scanGoIO(t, "p.go", chainSource(maxCalleeDepth+1)), []expectedViolation{{"HISS-02", "p.go", 8}})
	cycle := calleeFile(
		"func Start() { ping(context.Background()) }", // 8
		"", // 9
		"func ping(ctx context.Context) { pong(ctx) }", // 10
		"", // 11
		"func pong(ctx context.Context) { ping(ctx) }", // 12
	)
	assertViolations(t, scanGoIO(t, "p.go", cycle), []expectedViolation{{"HISS-02", "p.go", 8}, {"HISS-01", "p.go", 10}})
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/app\n")
	writeFixture(t, root, "nested/go.mod", "module example.com/app/nested\n")
	open := calleeFile("func Open(ctx context.Context) {", "\tctx, cancel := context.WithTimeout(ctx, time.Second)", "\tdefer cancel()", "}")
	writeFixture(t, root, "nested/nested.go", strings.Replace(open, "package p", "package nested", 1))
	writeFixture(t, root, "p/p.go", "package p\n\nimport (\n\t\"context\"\n\n\t\"example.com/app/nested\"\n)\n\nfunc Start() { nested.Open(context.Background()) }\n")
	assertViolations(t, scanFixture(t, root, ScanOptions{}), []expectedViolation{{"HISS-02", "p/p.go", 9}})
	bare := t.TempDir()
	writeFixture(t, bare, "store/store.go", strings.Replace(open, "package p", "package store", 1))
	writeFixture(t, bare, "p/p.go", "package p\n\nimport (\n\t\"context\"\n\n\t\"example.com/app/store\"\n)\n\nfunc Start() { store.Open(context.Background()) }\n")
	assertViolations(t, scanFixture(t, bare, ScanOptions{}), []expectedViolation{{"HISS-02", "p/p.go", 9}})
}
