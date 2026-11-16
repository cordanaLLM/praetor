// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// goTestNetnsEntry is a valid go-test-netns entry for one module, entered on today.
func goTestNetnsEntry(today time.Time) Exception {
	return Exception{
		Rule:     "go-test-netns",
		Path:     "tools/knowledge-retrieval/go.mod",
		Reason:   "lifecycle tests run the production ssh-keygen owner check, which needs host root mapped",
		Category: "D",
		Entered:  today.Format(ExceptionDateLayout),
		Expires:  today.AddDate(0, 0, 120).Format(ExceptionDateLayout),
	}
}

// TestValidateExceptionsGoTestNetns pins the go-test-netns entry form: a module go.mod by path
// (root or nested) with a category is accepted, the 120-day category D interval holds and its next
// day is refused, and a missing category, a non-go.mod path, a glob, a tool, a fingerprint and a
// line range are each refused.
func TestValidateExceptionsGoTestNetns(t *testing.T) {
	today := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	valid := goTestNetnsEntry(today)
	if err := ValidateExceptions([]Exception{valid}, today); err != nil {
		t.Fatalf("valid go-test-netns entry refused: %v", err)
	}
	root := valid
	root.Path = "go.mod"
	if err := ValidateExceptions([]Exception{root}, today); err != nil {
		t.Fatalf("root module entry refused: %v", err)
	}
	pastInterval := valid
	pastInterval.Expires = today.AddDate(0, 0, 121).Format(ExceptionDateLayout)
	if err := ValidateExceptions([]Exception{pastInterval}, today); err == nil {
		t.Fatal("expiry one day past the category D interval accepted")
	}
	refused := []struct {
		want   string
		mutate func(*Exception)
	}{
		// Inside the uncategorized grace window the general rule accepts no category; this rule does not.
		{"requires a category", func(e *Exception) { e.Category, e.Entered, e.Expires = "", "", "2026-11-20" }},
		{"must name the go.mod of one module", func(e *Exception) { e.Path = "tools/knowledge-retrieval/lifecycle_test.go" }},
		{"must name the go.mod of one module", func(e *Exception) { e.Path = "notgo.mod" }},
		{"must name exactly one of path and glob", func(e *Exception) { e.Glob = "tools/*/go.mod" }},
		// A glob alone passes the shared path-or-glob check; this rule still names one module.
		{"must name the go.mod of one module", func(e *Exception) { e.Path, e.Glob = "", "tools/*/go.mod" }},
		{"must not set tool", func(e *Exception) { e.Tool = "bwrap" }},
		{"and nothing else", func(e *Exception) { e.Fingerprint = "0123456789abcdef" }},
		{"lines is only valid for rule HISS-19", func(e *Exception) { e.Lines = "1-2" }},
	}
	for index := 0; index < len(refused); index++ {
		entry := valid
		refused[index].mutate(&entry)
		err := ValidateExceptions([]Exception{entry}, today)
		if err == nil || !strings.Contains(err.Error(), refused[index].want) {
			t.Errorf("case %d (%s): got %v", index, refused[index].want, err)
		}
	}
}

// TestExceptionProblemComplexity pins the exception validator to the repository's
// cyclomatic ceiling, using the same measurement as the HISS scanner.
func TestExceptionProblemComplexity(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "exceptions.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "problem" {
			continue
		}
		metrics := hiss.MeasureFunc(fset, fn)
		t.Logf("Exception.problem cyclomatic=%d", metrics.Cyclomatic)
		if metrics.Cyclomatic > hiss.DefaultMaxCyclomatic {
			t.Fatalf("Exception.problem cyclomatic=%d exceeds %d", metrics.Cyclomatic, hiss.DefaultMaxCyclomatic)
		}
		return
	}
	t.Fatal("Exception.problem declaration missing")
}

// TestGoTestNetnsExpiryBoundary preserves structural acceptance of expired entries while
// pinning their runtime expiry to the day after expires, including a time late on that day.
func TestGoTestNetnsExpiryBoundary(t *testing.T) {
	today := time.Date(2026, 11, 16, 23, 59, 59, 0, time.UTC)
	for _, days := range []int{-1, 0, 1} {
		entry := goTestNetnsEntry(today.AddDate(0, 0, days-120))
		if err := ValidateExceptions([]Exception{entry}, today); err != nil {
			t.Fatalf("entry expiring %+d days refused structurally: %v", days, err)
		}
		if got := entry.Expired(today); got != (days < 0) {
			t.Errorf("entry expiring %+d days: expired=%t", days, got)
		}
	}
}
