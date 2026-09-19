package agenthook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// recordingGetenv answers dir for RecordDirEnv and empty for everything else.
func recordingGetenv(dir string) func(string) string {
	return func(key string) string {
		if key == RecordDirEnv {
			return dir
		}
		return ""
	}
}

func TestRecordModeWritesABoundedZeroSixHundredFile(t *testing.T) {
	root := repository(t, true)
	dir := t.TempDir()
	payload := []byte(`{"tool_input":{"command":"rm -rf .git/hooks"}}`) // would deny if judged
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-tool", Stdin: bytes.NewReader(payload),
		Getenv: recordingGetenv(dir), WorkDir: root, Policy: policy(t),
	})
	if response.ExitCode != 0 || len(response.Stdout) != 0 {
		t.Fatalf("record mode did not answer the claude allow shape: %+v", response)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("record mode wrote %d files: %v", len(entries), err)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	// util.WriteFileSecure (record.go) requests recordFilePerm (0o600) through the portable
	// os.OpenFile API; that part is proven by internal/util's own WriteFileSecure tests.
	// What this assertion can actually check differs by platform: os.FileInfo.Mode() on
	// Windows is synthesised from the read-only attribute alone, so an ordinary (non-read-only)
	// file always reports 0o666 there regardless of what permission was requested at creation
	// -- there is no POSIX permission bit to read back. Asserting Perm() == 0o600
	// unconditionally therefore fails on every Windows run (#135; the class is enumerated in
	// #132), not because the file is any less private, but because Windows has no mode to
	// disagree with. Confinement on Windows is the file's ACL, not its Mode() report.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("recorded file mode: %v", info.Mode().Perm())
	}
	if !strings.HasPrefix(entries[0].Name(), "claude-pre-tool-") || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Errorf("recorded file name: %s", entries[0].Name())
	}
	written, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || !bytes.Equal(written, payload) {
		t.Errorf("recorded payload: %s (err %v)", written, err)
	}
}

func TestRecordModeIsDialectAgnostic(t *testing.T) {
	root := repository(t, true)
	dir := t.TempDir()
	payload := commandPayload(t, map[string]any{"executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true})
	response := Run(context.Background(), Invocation{
		Client: "agy", Event: "stop", Stdin: bytes.NewReader(payload),
		Getenv: recordingGetenv(dir), WorkDir: root, Policy: policy(t),
	})
	if response.ExitCode != 0 || strings.TrimSpace(string(response.Stdout)) != `{}` {
		t.Fatalf("record mode did not answer the agy stop allow shape: %+v", response)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "agy-stop-") {
		t.Fatalf("record mode wrote: %v (err %v)", entries, err)
	}
}

func TestRecordModeSkipsReadingStdinForTheEnvironmentEvent(t *testing.T) {
	root := repository(t, true)
	dir := t.TempDir()
	response := Run(context.Background(), Invocation{
		Client: "lefthook", Event: "environment", Getenv: recordingGetenv(dir), WorkDir: root, Policy: policy(t),
	})
	if response.ExitCode != 0 {
		t.Errorf("environment record mode: %+v", response)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("environment event recorded a file: %v (err %v)", entries, err)
	}
}

func TestRecordModeDeniesClosedOnAWriteFailure(t *testing.T) {
	root := repository(t, true)
	// Point the record dir at a path a regular file already occupies, so MkdirSecure
	// fails and the call must deny rather than silently lose the payload.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-tool", Stdin: bytes.NewReader([]byte(`{"tool_input":{"command":"git status"}}`)),
		Getenv: recordingGetenv(blocked), WorkDir: root, Policy: policy(t),
	})
	if response.ExitCode == 0 {
		t.Errorf("record mode swallowed a write failure: %+v", response)
	}
}

// TestRecordModeIsOffByDefault also covers a nil Invocation.Getenv taking the plain
// evaluation path instead of panicking in recordDirOf: TestRunFailsClosedOnMissingCollaborators
// (hook_test.go) already runs that exact case end to end through Run.
func TestRecordModeIsOffByDefault(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "claude", "pre-tool", root, []byte(`{"tool_input":{"command":"git status"}}`))
	if response.ExitCode != 0 {
		t.Errorf("plain allow regressed: %+v", response)
	}
}
