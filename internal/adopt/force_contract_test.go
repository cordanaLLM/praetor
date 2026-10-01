package adopt

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestForceCommand (#502): the command names the lock source it is given, with any flags between
// --force and --lock-source-root (positive); without a source it names the placeholder rather than
// an empty --lock-source-root= or none at all, since --force with no lock source always stops at
// the lock step (negative); a source holding spaces or a drive letter is passed through as given,
// and no flags leave no gap (boundary).
func TestForceCommand(t *testing.T) {
	cases := []struct {
		name, source string
		flags        []string
		want         string
	}{
		{"positive: source", "/src/praetor", nil, "praetorctl adopt --force --lock-source-root=/src/praetor"},
		{"positive: flags before the source", "/src/praetor", []string{"--dry-run"},
			"praetorctl adopt --force --dry-run --lock-source-root=/src/praetor"},
		{"negative: no source names the placeholder", "", nil,
			"praetorctl adopt --force --lock-source-root=" + LockSourcePlaceholder},
		{"boundary: a Windows path with a space", `C:\src\my praetor`, []string{},
			`praetorctl adopt --force --lock-source-root=C:\src\my praetor`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ForceCommand(tc.source, tc.flags...); got != tc.want {
				t.Fatalf("ForceCommand(%q, %q) = %q, want %q", tc.source, tc.flags, got, tc.want)
			}
		})
	}
	if s := (&adoptSession{opts: AdoptOptions{LockSourceRoot: "/run/source"}}); s.forceCommand() != ForceCommand("/run/source") {
		t.Fatalf("a session names %q, not the lock source it was given", s.forceCommand())
	}
}

// forceRemedyPattern matches a message telling the operator to re-run adopt with --force: "adopt
// --force", "adopt with --force", "use --force to" and "--force regenerates|restores".
var forceRemedyPattern = regexp.MustCompile(`(?i)\badopt (with )?--force\b|\buse --force\b|--force (regenerates|restores)\b`)

// maxRemedySourceFiles bounds the Go files one remedy scan reads (HISS-02).
const maxRemedySourceFiles = 8192

// forceRemedyFindings returns, for the Go file src at path, every string literal that tells the
// operator to re-run adopt with --force without naming --lock-source-root. A literal that says what
// happens without such a run ("without adopt --force") is no remedy.
func forceRemedyFindings(t *testing.T, path string, src []byte) []string {
	t.Helper()
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var findings []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		statement := strings.ReplaceAll(text, "without adopt --force", "")
		if forceRemedyPattern.MatchString(statement) && !strings.Contains(text, "--lock-source-root") {
			findings = append(findings, files.Position(literal.Pos()).String()+": "+strconv.Quote(text))
		}
		return true
	})
	return findings
}

// engineGoSources returns every non-test Go file of the engine's internal/ and cmd/ trees, the
// code whose messages reach an operator.
func engineGoSources(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, dir := range []string{"internal", "cmd"} {
		root := filepath.Join("..", "..", dir)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if len(paths) >= maxRemedySourceFiles {
				return errors.New("more Go files than the remedy scan reads")
			}
			if entry.Type().IsRegular() && util.IsGoNonTestSource(path) {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("list Go sources under %s: %v", dir, err)
		}
	}
	return paths
}

// TestForceRemedies (#502): --force needs a lock source, so a remedy that names a forced re-run
// without --lock-source-root sends the operator into a run that stops at the lock step. No string
// literal in the engine's operator-facing code does (positive); the remedies this unit routed
// through ForceCommand, in the form they had before, are each found (negative); a statement about
// a run without --force, and a remedy that names the lock source, pass (boundary).
func TestForceRemedies(t *testing.T) {
	sources := engineGoSources(t)
	if len(sources) < 100 {
		t.Fatalf("the scan found only %d Go sources; it is not reading the engine tree", len(sources))
	}
	var findings []string
	for _, path := range sources {
		data, err := os.ReadFile(path) //nolint:gosec // a source file of the engine under test
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		findings = append(findings, forceRemedyFindings(t, path, data)...)
	}
	if len(findings) != 0 {
		t.Fatalf("remedies name adopt --force without --lock-source-root; build them with ForceCommand:\n%s",
			strings.Join(findings, "\n"))
	}
	cases := []struct {
		literal string
		remedy  bool
	}{
		{`"%s managed attribute block was edited; review it and rerun adopt --force"`, true},
		{`". Re-run adopt with --force to merge them; every other key is kept"`, true},
		{`"review its commands against the verification plan or use --force to refresh a recognized harness boundary."`, true},
		{`"preserved, not verified (--force regenerates it)"`, true},
		{`"differs from the copy the binary carries, and praetorctl adopt --force restores it"`, true},
		{`"re-pin its catalog, without adopt --force"`, false},
		{`"  praetorctl adopt --force --dry-run --lock-source-root=%s --path=%s\n"`, false},
		{`"kept, --force included"`, false},
	}
	for _, tc := range cases {
		src := []byte("package fixture\n\nvar message = " + tc.literal + "\n")
		if got := forceRemedyFindings(t, "fixture.go", src); (len(got) == 1) != tc.remedy {
			t.Errorf("literal %s: findings %q, want a finding %t", tc.literal, got, tc.remedy)
		}
	}
}
