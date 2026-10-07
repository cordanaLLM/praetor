// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"go/ast"
	"strings"
	"testing"
)

// The stored cancel functions, the shared logger proof and the spread calls of #841.

// hookSource wraps one hook (its OnStart and OnStop bodies) in a constructor that holds a cancel
// variable and a holder struct, the two places a stored cancel function lives.
func hookSource(start, stop string) string {
	return lifecycleSource(`"go.uber.org/fx"`,
		"\tvar cancel context.CancelFunc",
		"\th := &holder{}",
		"\tlc.Append(fx.Hook{",
		"\t\tOnStart: func(context.Context) error {",
		start,
		"\t\t\treturn nil",
		"\t\t},",
		"\t\tOnStop: func(stopCtx context.Context) error {",
		stop,
		"\t\t\treturn nil",
		"\t\t},",
		"\t})",
	) + "\ntype holder struct{ drainCancel, other context.CancelFunc }\n"
}

const (
	storeVar   = "\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n\t\t\tcancel = runCancel\n\t\t\tgo mgr.Start(runCtx)"
	storeField = "\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n\t\t\th.drainCancel = runCancel\n\t\t\tgo mgr.Start(runCtx)"
)

// Negative: a cancel function stored in a variable or a struct field, an alias of an alias, a
// deferred call, and a call that follows a return inside a closure are all cancelled by OnStop.
func TestGoIOLifecycle_Negative_StoredCancelFunctionsAreCalled(t *testing.T) {
	cases := map[string][2]string{
		"variable":        {storeVar, "\t\t\tcancel()"},
		"field":           {storeField, "\t\t\th.drainCancel()"},
		"deferred":        {storeField, "\t\t\tdefer h.drainCancel()"},
		"after func":      {storeVar, "\t\t\tfunc() { return }()\n\t\t\tcancel()"},
		"nil guard":       {storeVar, "\t\t\tif cancel == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tcancel()"},
		"nil guard first": {storeField, "\t\t\tif nil == h.drainCancel {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tdefer h.drainCancel()"},
		"alias chain": {
			"\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n\t\t\tcancel = runCancel\n\t\t\th.drainCancel = cancel\n\t\t\tgo mgr.Start(runCtx)",
			"\t\t\th.drainCancel()",
		},
	}
	for name, c := range cases {
		if rep := scanGoIO(t, "svc.go", hookSource(c[0], c[1])); hiss02Count(rep) != 0 {
			t.Errorf("%s: a context whose stored cancel OnStop calls must be accepted: %+v", name, rep.Violations)
		}
	}
}

// Positive: the near misses stay reported. OnStop never calls the place the cancel function was
// stored in, calls another field, calls it after a return an error can take, rebinds or shadows
// it first, or the place was overwritten (or its holder reassigned) after the store.
func TestGoIOLifecycle_Positive_StoredCancelNearMisses(t *testing.T) {
	cases := map[string][2]string{
		"never called":    {storeVar, "\t\t\t_ = mgr.Wait(stopCtx)"},
		"other field":     {storeField, "\t\t\th.other()"},
		"conditional ret": {storeVar, "\t\t\tif err := mgr.Wait(stopCtx); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tcancel()"},
		"cancel rebound":  {storeVar, "\t\t\tcancel = func() {}\n\t\t\tcancel()"},
		"holder shadowed": {storeField, "\t\t\th := &holder{}\n\t\t\th.drainCancel()"},
		"holder reassign": {
			"\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n\t\t\th.drainCancel = runCancel\n\t\t\th = &holder{}\n\t\t\tgo mgr.Start(runCtx)",
			"\t\t\th.drainCancel()",
		},
		"store overwritten": {
			"\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n\t\t\tcancel = runCancel\n\t\t\tcancel = func() {}\n\t\t\tgo mgr.Start(runCtx)",
			"\t\t\tcancel()",
		},
		"guard on other":    {storeVar, "\t\t\tif h.other == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tcancel()"},
		"guard not nil":     {storeVar, "\t\t\tif cancel != nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tcancel()"},
		"guard does more":   {storeVar, "\t\t\tif cancel == nil {\n\t\t\t\tmgr.Wait(stopCtx)\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tcancel()"},
		"guard with init":   {storeVar, "\t\t\tif err := mgr.Wait(stopCtx); cancel == nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tcancel()"},
		"cancel in closure": {storeVar, "\t\t\tgo func() { cancel() }()"},
	}
	for name, c := range cases {
		if rep := scanGoIO(t, "svc.go", hookSource(c[0], c[1])); hiss02Count(rep) != 1 {
			t.Errorf("%s: the context must stay reported once: %+v", name, rep.Violations)
		}
	}
}

const (
	withCancel = "\t\t\trunCtx, runCancel := context.WithCancel(context.Background())\n"
	guardCall  = "\t\t\tif cancel == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\tcancel()"
)

// Positive: pairing is by binding and by top-level statement. A cancel place the start literal
// declares itself is another variable than the one OnStop calls, with or without the nil guard;
// a store inside a nested block or an uncalled closure may never run, so the guard would turn
// the leak into a silent return; and a conditional reassignment in OnStop may replace the function.
func TestGoIOLifecycle_Positive_BindingAndTopLevelOnly(t *testing.T) {
	cases := map[string][2]string{
		"shadow guarded":    {"\t\t\trunCtx, cancel := context.WithCancel(context.Background())\n\t\t\tgo mgr.Start(runCtx)", guardCall},
		"shadow plain":      {"\t\t\trunCtx, cancel := context.WithCancel(context.Background())\n\t\t\tgo mgr.Start(runCtx)", "\t\t\tcancel()"},
		"define alias":      {withCancel + "\t\t\tcancel := runCancel\n\t\t\tgo mgr.Start(runCtx)", guardCall},
		"var alias":         {withCancel + "\t\t\tvar cancel = runCancel\n\t\t\tgo mgr.Start(runCtx)", guardCall},
		"local holder":      {withCancel + "\t\t\th := &holder{}\n\t\t\th.drainCancel = runCancel\n\t\t\tgo mgr.Start(runCtx)", "\t\t\th.drainCancel()"},
		"local param":       {withCancel + "\t\t\tfunc(cancel context.CancelFunc) { cancel = runCancel }(nil)\n\t\t\tgo mgr.Start(runCtx)", "\t\t\tcancel()"},
		"conditional store": {withCancel + "\t\t\tif mgr == nil {\n\t\t\t\tcancel = runCancel\n\t\t\t}\n\t\t\tgo mgr.Start(runCtx)", guardCall},
		"conditional plain": {withCancel + "\t\t\tif mgr == nil {\n\t\t\t\tcancel = runCancel\n\t\t\t}\n\t\t\tgo mgr.Start(runCtx)", "\t\t\tcancel()"},
		"closure store":     {withCancel + "\t\t\tsetter := func() { cancel = runCancel }\n\t\t\t_ = setter\n\t\t\tgo mgr.Start(runCtx)", guardCall},
		"stop reassigns":    {storeVar, "\t\t\tif mgr.Up() {\n\t\t\t\tcancel = func() {}\n\t\t\t}\n\t\t\tcancel()"},
		"stop field reset":  {storeField, "\t\t\tif mgr.Up() {\n\t\t\t\th = &holder{}\n\t\t\t}\n\t\t\th.drainCancel()"},
	}
	for name, c := range cases {
		if rep := scanGoIO(t, "svc.go", hookSource(c[0], c[1])); hiss02Count(rep) != 1 {
			t.Errorf("%s: the context must stay reported once: %+v", name, rep.Violations)
		}
	}
}

// Negative: the adopter shapes. An outer variable or an outer struct field assigned by a
// top-level statement of the start literal, directly or through a local alias of the cancel
// function, and called by OnStop (guarded or not) is accepted.
func TestGoIOLifecycle_Negative_OuterPlaceAssignedAtTopLevel(t *testing.T) {
	cases := map[string][2]string{
		"outer var":      {storeVar, guardCall},
		"outer field":    {storeField, "\t\t\tif h.drainCancel == nil {\n\t\t\t\treturn nil\n\t\t\t}\n\t\t\th.drainCancel()"},
		"through alias":  {withCancel + "\t\t\tc2 := runCancel\n\t\t\tcancel = c2\n\t\t\tgo mgr.Start(runCtx)", "\t\t\tcancel()"},
		"other shadowed": {storeVar, "\t\t\tx := func() { cancel := 1; _ = cancel }\n\t\t\t_ = x\n\t\t\tcancel()"},
	}
	for name, c := range cases {
		if rep := scanGoIO(t, "svc.go", hookSource(c[0], c[1])); hiss02Count(rep) != 0 {
			t.Errorf("%s: an outer place stored at top level and called by OnStop must be accepted: %+v", name, rep.Violations)
		}
	}
}

// Boundary: the pairing follows at most maxCancelAliases places; the store past it is not
// followed, so a stop function that calls only that last place leaves the context reported.
func TestGoIOLifecycle_Boundary_AliasBound(t *testing.T) {
	for _, extra := range []int{0, 1} {
		count := maxCancelAliases - 1 + extra
		names := make([]string, 0, count)
		stores := []string{"\t\t\trunCtx, runCancel := context.WithCancel(context.Background())"}
		for i := 1; i <= count; i++ {
			names = append(names, fmt.Sprintf("a%d", i))
			stores = append(stores, fmt.Sprintf("\t\t\ta%d = runCancel", i))
		}
		stores = append(stores, "\t\t\tgo mgr.Start(runCtx)")
		src := lifecycleSource(`"go.uber.org/fx"`,
			"\tvar "+strings.Join(names, ", ")+" context.CancelFunc",
			"\tlc.Append(fx.Hook{",
			"\t\tOnStart: func(context.Context) error {",
			strings.Join(stores, "\n"),
			"\t\t\treturn nil",
			"\t\t},",
			"\t\tOnStop: func(context.Context) error { "+names[count-1]+"(); return nil },",
			"\t})",
		)
		want := extra
		if got := hiss02Count(scanGoIO(t, "svc.go", src)); got != want {
			t.Errorf("%d places followed: findings = %d, want %d", count+1, got, want)
		}
	}
}

// loggerFile is a Go file of package p that imports context, log/slog and time, with a params
// struct the way a dependency-injection constructor receives its collaborators.
func loggerFile(lines ...string) string {
	return strings.Join(append([]string{
		"package p", // 1
		"",          // 2
		"import (",  // 3
		"\t\"context\"",
		"\t\"log/slog\"",
		"\t\"time\"",
		")", // 7
		"",  // 8
		"type Params struct {",
		"\tLogger *slog.Logger",
		"\tShip   Shipper",
		"}",
		"",
	}, append(lines, "")...), "\n")
}

// Negative: a callee that only logs the context is accepted whether its logger is a parameter, a
// local, a derived logger or a field of a parameter struct, at any depth of the proof.
func TestGoIOCallee_Negative_LoggerShapesShareOneProof(t *testing.T) {
	src := loggerFile(
		"func Start(p Params, l *slog.Logger) {", // 14
		"\tviaParam(l, context.Background())",
		"\tviaLocal(context.Background())",
		"\tviaField(p, context.Background())",
		"\tviaPointer(&p, context.Background())",
		"\tviaChain(p, context.Background())",
		"\tviaDerived(l, context.Background())",
		"}",
		"",
		"func viaParam(l *slog.Logger, ctx context.Context) { l.ErrorContext(ctx, \"x\") }",
		"func viaLocal(ctx context.Context) {",
		"\tl := slog.Default().WithGroup(\"g\")",
		"\tl.InfoContext(ctx, \"x\")",
		"}",
		"func viaField(p Params, ctx context.Context) { p.Logger.WarnContext(ctx, \"x\") }",
		"func viaPointer(p *Params, ctx context.Context) { p.Logger.WarnContext(ctx, \"x\") }",
		"func viaChain(p Params, ctx context.Context) { viaField(p, ctx) }",
		"func viaDerived(l *slog.Logger, ctx context.Context) { l.With(\"a\", 1).DebugContext(ctx, \"x\") }",
	)
	if rep := scanGoIO(t, "p.go", src); hiss02Count(rep) != 0 {
		t.Fatalf("a logger proven the same way in every function must be accepted: %+v", rep.Violations)
	}
}

// Positive: the lookalikes stay reported, in the caller and in the callee: another type's method
// of the same name, a field that is not a *slog.Logger, a parameter of an unknown type, a shadowed
// logger, a type parameter, and a field of a struct declared twice.
func TestGoIOCallee_Positive_LoggerLookalikes(t *testing.T) {
	src := loggerFile(
		"func Start(p Params, s Shipper) {", // 14
		"\tshipParam(s, context.Background())",
		"\tshipField(p, context.Background())",
		"\tshadow(s, context.Background())",
		"\tgeneric[Shipper](s, context.Background())",
		"}",
		"",
		"type Shipper interface{ ErrorContext(ctx context.Context, msg string) }",
		"func shipParam(s Shipper, ctx context.Context) { s.ErrorContext(ctx, \"x\") }",
		"func shipField(p Params, ctx context.Context) { p.Ship.ErrorContext(ctx, \"x\") }",
		"func shadow(s Shipper, ctx context.Context) {",
		"\tl := slog.Default()",
		"\tfor _, l := range []Shipper{s} {",
		"\t\tl.ErrorContext(ctx, \"x\")",
		"\t}",
		"\t_ = l",
		"}",
		"func generic[Params Shipper](p Params, ctx context.Context) { p.Logger.ErrorContext(ctx, \"x\") }",
	)
	assertViolations(t, scanGoIO(t, "p.go", src), []expectedViolation{
		{"HISS-02", "p.go", 15}, {"HISS-02", "p.go", 16}, {"HISS-02", "p.go", 17}, {"HISS-02", "p.go", 18},
	})
}

// Boundary: a plain assignment proves a logger only in the scope it occurs in. The variable is
// declared as another type, so after the block it may hold something that is no logger; inside
// the block it holds one. The main walk and the callee walk decide alike.
func TestGoIOLogger_Boundary_PlainAssignmentProvesItsOwnScope(t *testing.T) {
	src := loggerFile(
		"type Shipper interface{ InfoContext(ctx context.Context, msg string) }",
		"func After(flag bool, s Shipper) {",
		"	ctx := context.Background()",
		"	var l Shipper = s",
		"	if flag {",
		"		l = slog.Default()",
		"	}",
		"	l.InfoContext(ctx, \"x\")",
		"}",
		"func Inside(flag bool, s Shipper) {",
		"	ctx := context.Background()",
		"	var l Shipper = s",
		"	if flag {",
		"		l = slog.Default()",
		"		l.InfoContext(ctx, \"x\")",
		"	}",
		"}",
		"func Callee(flag bool, s Shipper) { viaFlag(context.Background(), flag, s) }",
		"func viaFlag(ctx context.Context, flag bool, s Shipper) {",
		"	var l Shipper = s",
		"	if flag {",
		"		l = slog.Default()",
		"	}",
		"	l.InfoContext(ctx, \"x\")",
		"}",
	)
	rep := scanGoIO(t, "p.go", src)
	if got := hiss02Count(rep); got != 2 {
		t.Fatalf("the use after the block (main walk and callee walk) must be reported, the one inside not: %+v", rep.Violations)
	}
}

// Boundary: the main walk and the callee walk decide alike. The same sink call is accepted when
// the logger is a parameter of the function that holds the context, and rejected when it is not.
func TestGoIO_Boundary_MainWalkAndCalleeWalkAgree(t *testing.T) {
	src := loggerFile(
		"func Main(p Params, l *slog.Logger, s Shipper) {", // 14
		"\tctx := context.Background()",
		"\tl.InfoContext(ctx, \"x\")",
		"\tp.Logger.InfoContext(ctx, \"x\")",
		"\ts.InfoContext(ctx, \"x\")",
		"}",
		"type Shipper interface{ InfoContext(ctx context.Context, msg string) }",
	)
	assertViolations(t, scanGoIO(t, "p.go", src), []expectedViolation{{"HISS-02", "p.go", 18}})
}

// Boundary: a call that spreads its last argument maps the fixed arguments one to one, so a
// callee that bounds its first parameter discharges the call and one that does I/O does not.
func TestGoIOCallee_Boundary_SpreadCallMapsFixedArguments(t *testing.T) {
	bounded := calleeFile(
		"func Start(opts []int) { Open(context.Background(), opts...) }", // 8
		"",
		"func Open(ctx context.Context, opts ...int) {",
		"\tctx, cancel := context.WithTimeout(ctx, time.Second)",
		"\tdefer cancel()",
		"\tdial(ctx)",
		"}",
	)
	if rep := scanGoIO(t, "p.go", bounded); hiss02Count(rep) != 0 {
		t.Fatalf("a spread call to a callee that bounds the fixed argument must be accepted: %+v", rep.Violations)
	}
	unbounded := strings.Replace(bounded, "\tctx, cancel := context.WithTimeout(ctx, time.Second)\n\tdefer cancel()\n", "", 1)
	assertViolations(t, scanGoIO(t, "p.go", unbounded), []expectedViolation{{"HISS-02", "p.go", 8}})
	nested := calleeFile(
		"func Start(opts []int) { Open(context.Background(), opts...) }", // 8
		"",
		"func Open(ctx context.Context, opts ...int) {",
		"\tinner(ctx, opts...)",
		"}",
		"",
		"func inner(ctx context.Context, opts ...int) {",
		"\tctx, cancel := context.WithTimeout(ctx, time.Second)",
		"\tdefer cancel()",
		"\tdial(ctx)",
		"}",
	)
	if rep := scanGoIO(t, "p.go", nested); hiss02Count(rep) != 0 {
		t.Fatalf("a spread call inside a callee maps its fixed argument too: %+v", rep.Violations)
	}
}

// TestSpreads_Boundary_OnlyTheSpreadPositionIsExcluded pins the helper: with an ellipsis only the
// last argument is spread, and without one no argument is.
func TestSpreads_Boundary_OnlyTheSpreadPositionIsExcluded(t *testing.T) {
	_, spread := parseCallee(t, "", "f(a, b, c...)")
	_, plain := parseCallee(t, "", "f(a, b, c)")
	call, spreadOK := spread.(*ast.CallExpr)
	plainCall, plainOK := plain.(*ast.CallExpr)
	if !spreadOK || !plainOK {
		t.Fatalf("parseCallee must return calls: %T, %T", spread, plain)
	}
	for i, want := range []bool{false, false, true} {
		if got := spreads(call, i); got != want {
			t.Errorf("spread call, argument %d: spreads = %v, want %v", i, got, want)
		}
		if spreads(plainCall, i) {
			t.Errorf("plain call, argument %d must not be spread", i)
		}
	}
}

// fanSource is a package whose Start hands two contexts to fan, which hands each to n callees
// that bound it, so one proof visits 2*(n+1) callees.
func fanSource(n int) string {
	lines := []string{"func Start() { fan(context.Background(), context.Background()) }", "", "func fan(a, b context.Context) {"}
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("\tbound%d(a)", i), fmt.Sprintf("\tbound%d(b)", i))
	}
	lines = append(lines, "}", "")
	for i := 0; i < n; i++ {
		lines = append(lines,
			fmt.Sprintf("func bound%d(ctx context.Context) {", i),
			"\tctx, cancel := context.WithTimeout(ctx, time.Second)",
			"\tdefer cancel()",
			"\tdial(ctx)",
			"}",
		)
	}
	return calleeFile(lines...)
}

// Boundary: maxCalleeNodes is the budget of one proof, shared by every argument the call hands a
// context to. Two arguments that each fit alone but exceed it together are reported.
func TestGoIOCallee_Boundary_NodeBudgetIsPerProof(t *testing.T) {
	fits := maxCalleeNodes/2 - 1
	if rep := scanGoIO(t, "p.go", fanSource(fits)); hiss02Count(rep) != 0 {
		t.Fatalf("%d callees over two arguments fit the budget of %d: %+v", 2*(fits+1), maxCalleeNodes, rep.Violations)
	}
	over := maxCalleeNodes/2 + 1
	assertViolations(t, scanGoIO(t, "p.go", fanSource(over)), []expectedViolation{
		{"HISS-02", "p.go", 8}, {"HISS-04", "p.go", 10},
	})
}

// Boundary: a swap reads both values before it rebinds either, so a deadline-free context stays
// tracked under the name it moved to.
func TestGoIO_Boundary_SwapReadsBothValuesFirst(t *testing.T) {
	src := calleeFile(
		"func F() {", // 8
		"\ta := context.Background()",
		"\tb, cancel := context.WithTimeout(context.Background(), time.Second)",
		"\tdefer cancel()",
		"\ta, b = b, a",
		"\tuse(b)", // 13 holds the deadline-free context now
		"\tuse(a)", // 14 holds the bounded one
		"}",
	)
	assertViolations(t, scanGoIO(t, "p.go", src), []expectedViolation{{"HISS-02", "p.go", 13}})
}
