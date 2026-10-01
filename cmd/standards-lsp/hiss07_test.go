package main

import "testing"

// The editor's HISS-07 panic diagnostic asks hiss.AbortsHTTPResponse whether a panic is the
// net/http abort sentinel (issue #331), so it agrees with `praetorctl audit` on every shape.

func TestHISS07_Negative_HTTPAbortSentinelIsNotDiagnosed(t *testing.T) {
	sources := map[string]string{
		"plain": "package p\n\nimport \"net/http\"\n\nfunc F() { panic(http.ErrAbortHandler) }\n",
		"alias": "package p\n\nimport web \"net/http\"\n\nfunc F() { (panic)((web.ErrAbortHandler)) }\n",
		"dot":   "package p\n\nimport . \"net/http\"\n\nfunc F() { panic(ErrAbortHandler) }\n",
		"guarded": "package p\n\nimport (\n\t\"errors\"\n\t\"net/http\"\n)\n\n" +
			"func F() {\n\tdefer func() {\n\t\tif err, ok := recover().(error); ok && errors.Is(err, http.ErrAbortHandler) {\n" +
			"\t\t\tpanic(http.ErrAbortHandler)\n\t\t}\n\t}()\n}\n",
	}
	for name, src := range sources {
		if n := countDiagnostics(analyze(t, "file:///sentinel.go", src), "HISS-07", "panic"); n != 0 {
			t.Errorf("%s: panic(http.ErrAbortHandler) must not be diagnosed, got %d", name, n)
		}
	}
}

func TestHISS07_Positive_OtherPanicsStayDiagnosed(t *testing.T) {
	sources := map[string]string{
		"literal":   "package p\n\nfunc F() { panic(\"boom\") }\n",
		"lookalike": "package p\n\nimport \"errors\"\n\nfunc F() {\n\tErrAbortHandler := errors.New(\"x\")\n\tpanic(ErrAbortHandler)\n}\n",
		"foreign":   "package p\n\nimport http \"example.com/fake/http\"\n\nfunc F() { panic(http.ErrAbortHandler) }\n",
		"unrelated": "package p\n\nimport \"net/http\"\n\nfunc F() { panic(http.ErrServerClosed) }\n",
		"parens":    "package p\n\nfunc F() { (panic)(1) }\n",
	}
	for name, src := range sources {
		if n := countDiagnostics(analyze(t, "file:///other.go", src), "HISS-07", "panic"); n != 1 {
			t.Errorf("%s: want one panic diagnostic, got %d", name, n)
		}
	}
}

func TestHISS07_Boundary_ShadowIsScopedToItsFunctionDeclaration(t *testing.T) {
	src := "package p\n\nimport \"net/http\"\n\ntype carrier struct{ ErrAbortHandler error }\n\n" +
		"func Shadowed(http carrier) { panic(http.ErrAbortHandler) }\n" +
		"func Unshadowed(c carrier) { panic(http.ErrAbortHandler) }\n" +
		"var Late = func() { panic(http.ErrAbortHandler) }\n"
	diags := analyze(t, "file:///scope.go", src)
	if n := countDiagnostics(diags, "HISS-07", "panic"); n != 1 {
		t.Fatalf("only the shadowed function's panic is diagnosed, got %d: %+v", n, diags)
	}
	for _, d := range diags {
		if d.Code == "HISS-07" && d.Range.Start.Line != 6 {
			t.Errorf("diagnostic on line %d, want the shadowed function on line 7 (0-based 6)", d.Range.Start.Line)
		}
	}
}
