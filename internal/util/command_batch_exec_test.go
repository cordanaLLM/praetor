package util

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// batchHelperEnv selects TestBatchFileHelper inside the re-executed test binary.
const batchHelperEnv = "PRAETOR_BATCH_FILE_TEST"

// batchHostileArgs are nine arguments cmd.exe would reinterpret if they reached it bare:
// a caret range, command separators, a pipe, grouping and redirection with spaces, a variable
// reference, a delayed-expansion reference, an empty argument, a trailing backslash, and
// delimiters with a run of carets. Nine, so %1 through %9 forward every one.
var batchHostileArgs = []string{"lib@^5.7.3", "a&b|c", "(x) <y> z", "%PATH%", "!USERNAME!", "",
	`C:\dir with space\`, ",;=^^", "a\tb"}

// skipOffWindows skips an exec-level batch-file test where no batch file can run.
func skipOffWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("only Windows runs .bat/.cmd files through cmd.exe; the command line they get is " +
			"covered on every platform by the TestBatchCommandLine cases")
	}
}

// writeBatchShim writes a batch file called name into dir that runs this test binary's
// TestBatchFileHelper with forward (such as %* or %1 %2) as its arguments, the way an npm
// shim hands %* to node. The run flag is quoted, so cmd.exe keeps its caret.
func writeBatchShim(t *testing.T, dir, name, forward string) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, name)
	body := "@\"" + binary + "\" \"-test.run=^TestBatchFileHelper$\" -- " + forward + "\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return shim
}

// metacharacterDirectory creates a directory whose name holds cmd.exe metacharacters and no
// space, which os/exec left unquoted in argv[0] (#538). It holds no ';', which would split it
// as a PATH entry.
func metacharacterDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Tom&Jerry(1)^!%PATH%,=")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// batchHelperContext returns a context whose commands run TestBatchFileHelper in argv mode.
func batchHelperContext(t *testing.T) context.Context {
	t.Helper()
	ctx, err := WithCommandEnvironment(t.Context(), append(os.Environ(), batchHelperEnv+"=argv", "GOCOVERDIR="+t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// runBatchHelper runs name with args through RunCommandBytes in dir and returns the argv
// TestBatchFileHelper received.
func runBatchHelper(t *testing.T, dir, name string, args []string) ([]string, error) {
	t.Helper()
	result, err := RunCommandBytes(batchHelperContext(t), dir, name, 1<<16, args...)
	if err != nil {
		return nil, err
	}
	var argv []string
	if err := json.Unmarshal(result.Stdout, &argv); err != nil {
		t.Fatalf("helper output %q (stderr %q): %v", result.Stdout, result.Stderr, err)
	}
	return argv, nil
}

// A batch file in a directory named with cmd.exe metacharacters runs, and whether it forwards
// %*, %1..%9 or "%~1".."%~8", the program behind it receives exactly the arguments given:
// the result does not depend on how the batch file re-reads them (#538). The "%~n" batch file
// gets the arguments without a percent sign, because Wine's cmd.exe (11.18) expands a percent
// sign again inside the value of %~n; the %* and %n forms, which it expands once, carry the
// percent case.
func TestRunCommandBytes_Positive_BatchFileForwardsArgvUnchanged(t *testing.T) {
	skipOffWindows(t)
	dir := metacharacterDirectory(t)
	withoutPercent := slices.DeleteFunc(slices.Clone(batchHostileArgs), func(arg string) bool { return strings.Contains(arg, "%") })
	cases := []struct {
		shim, forward string
		args          []string
	}{
		{"star.cmd", "%*", batchHostileArgs},
		{"digits.cmd", "%1 %2 %3 %4 %5 %6 %7 %8 %9", batchHostileArgs},
		{"tilde.bat", `"%~1" "%~2" "%~3" "%~4" "%~5" "%~6" "%~7" "%~8"`, withoutPercent},
	}
	for _, tc := range cases {
		shim := writeBatchShim(t, dir, tc.shim, tc.forward)
		got, err := runBatchHelper(t, "", shim, tc.args)
		if err != nil || !slices.Equal(got, tc.args) {
			t.Errorf("%s: argv = %q, %v; want %q", tc.shim, got, err, tc.args)
		}
	}
}

// An argument no quoting carries through cmd.exe is refused before the batch file starts.
func TestRunCommandBytes_Negative_BatchFileRefusesQuoteAndLineBreak(t *testing.T) {
	skipOffWindows(t)
	shim := writeBatchShim(t, metacharacterDirectory(t), "star.cmd", "%*")
	for _, arg := range []string{`lib"x`, "a\nb", "a\rb"} {
		result, err := RunCommandBytes(batchHelperContext(t), "", shim, 1<<16, "update", arg)
		if !errors.Is(err, ErrBatchFileArgument) || len(result.Stdout) != 0 {
			t.Errorf("%q: stdout %q, err %v; want a refusal before the shim runs", arg, result.Stdout, err)
		}
	}
}

// A bare name found through PATHEXT, a path relative to the working directory, an upper-case
// extension, and a call without arguments all reach the batch file the same way.
func TestRunCommandBytes_Boundary_BatchFileResolution(t *testing.T) {
	skipOffWindows(t)
	dir := metacharacterDirectory(t)
	writeBatchShim(t, dir, "argvshim.CMD", "%*")
	t.Setenv("PATH", dir)
	got, err := runBatchHelper(t, "", "argvshim", []string{"lib@^1.0.0"})
	if err != nil || !slices.Equal(got, []string{"lib@^1.0.0"}) {
		t.Errorf("bare name: argv = %q, %v", got, err)
	}
	relative := filepath.Join(filepath.Base(dir), "argvshim.CMD")
	got, err = runBatchHelper(t, filepath.Dir(dir), relative, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("relative path, no arguments: argv = %q, %v", got, err)
	}
}

// TestBatchFileHelper is the program behind the test batch files: it prints the arguments
// after "--" as a JSON array and exits.
func TestBatchFileHelper(t *testing.T) {
	if os.Getenv(batchHelperEnv) != "argv" {
		return
	}
	args := flag.Args()
	if args == nil {
		args = []string{}
	}
	if err := json.NewEncoder(os.Stdout).Encode(args); err != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
