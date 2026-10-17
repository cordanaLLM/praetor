package decisions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adr"
)

func TestPinCheckMissingAndMismatchedBinary(t *testing.T) {
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	if err := PinCheck(t.Context()); err == nil || !strings.Contains(err.Error(), Version) || !strings.Contains(err.Error(), DecidedMCPHash[:12]) || !strings.Contains(err.Error(), ReleaseURL) {
		t.Fatalf("missing binary error = %v", err)
	}
	name := "decided-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	wrong := filepath.Join(pathDir, name)
	if err := os.WriteFile(wrong, []byte("wrong release"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PinCheck(t.Context()); err == nil || !strings.Contains(err.Error(), DecidedMCPHash[:12]) {
		t.Fatalf("mismatched binary error = %v", err)
	}
}

func TestPinCheckReleaseBinaryWhenAvailable(t *testing.T) {
	release := os.Getenv("PRAETOR_DECIDED_MCP_TEST_BINARY")
	if release == "" {
		t.Skip("PRAETOR_DECIDED_MCP_TEST_BINARY not set; release binary unavailable")
	}
	data, err := os.ReadFile(release)
	if err != nil {
		t.Fatalf("read release binary: %v", err)
	}
	dir := t.TempDir()
	name := "decided-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := PinCheck(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRunChildPassesStreamsAndExitCode(t *testing.T) {
	t.Setenv("PRAETOR_DECISION_HELPER_MODE", "copy")
	t.Setenv("PRAETOR_DECISION_HELPER_EXIT", "0")
	stdin := strings.NewReader("request\n")
	var stdout, stderr bytes.Buffer
	err := runChild(t.Context(), os.Args[0], []string{"-test.run=TestDecisionChildHelper"}, stdin, &stdout, &stderr)
	if err != nil || stdout.String() != "request\n" || stderr.String() != "child stderr\n" {
		t.Fatalf("child streams stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
	}
	t.Setenv("PRAETOR_DECISION_HELPER_EXIT", "23")
	err = runChild(t.Context(), os.Args[0], []string{"-test.run=TestDecisionChildHelper"}, strings.NewReader(""), io.Discard, io.Discard)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child exit error = %v, want exit 23", err)
	}
}

func TestDecisionChildHelper(t *testing.T) {
	if os.Getenv("PRAETOR_DECISION_HELPER_MODE") == "" {
		return
	}
	value := os.Getenv("PRAETOR_DECISION_HELPER_EXIT")
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		os.Exit(24)
	}
	if _, err := io.WriteString(os.Stderr, "child stderr\n"); err != nil {
		os.Exit(24)
	}
	if value != "0" {
		os.Exit(23)
	}
	// Exit before the testing framework prints PASS and, under -cover, a coverage line: the
	// parent compares stdout exactly.
	os.Exit(0)
}

func TestPinCheckCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := PinCheck(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled pin check = %v", err)
	}
}

func TestServeExportsChecksPinThenRunsWithCorpusRoot(t *testing.T) {
	repo := t.TempDir()
	mustWriteDecision(t, filepath.Join(repo, filepath.FromSlash(adr.RecordDirectory), "0001-choice.md"), "# Choice\n")
	// The runner must start exactly the file the pin check verified, never a second PATH lookup.
	verified := filepath.Join(t.TempDir(), "verified-decided-mcp")
	t.Setenv("PATH", t.TempDir())
	checked := false
	runnerCalled := false
	wantErr := errors.New("child failed")
	err := serve(t.Context(), repo, "", func(context.Context) (string, error) {
		checked = true
		return verified, nil
	}, func(_ context.Context, binary string, args []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		runnerCalled = true
		if !checked || binary != verified || len(args) != 2 || args[0] != "--root" {
			t.Fatalf("runner called before pin or with wrong argv: %q %v", binary, args)
		}
		if _, err := os.Stat(filepath.Join(args[1], "decisions", "adr", "adr-0001-choice.md")); err != nil {
			t.Fatalf("export missing before server start: %v", err)
		}
		return wantErr
	})
	if !runnerCalled || !errors.Is(err, wantErr) {
		t.Fatalf("serve runner=%v err=%v", runnerCalled, err)
	}
}

func TestServeStopsWhenPinCheckFails(t *testing.T) {
	repo := t.TempDir()
	want := errors.New("pin mismatch")
	runnerCalled := false
	err := serve(t.Context(), repo, "", func(context.Context) (string, error) { return "", want },
		func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			runnerCalled = true
			return nil
		})
	if !errors.Is(err, want) || runnerCalled {
		t.Fatalf("serve err=%v runner=%v", err, runnerCalled)
	}
}
