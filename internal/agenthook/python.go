package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The two checkpoint evaluators stay Python after H4 (3.3 of the rollout spec); this file is
// their Go-side caller. It resolves an interpreter from the operator's candidate list, runs
// `.config/lefthook/scripts/{checkpoint,checkpoint_scope}.py` directly (no Lefthook hop), and
// reads back the marker line each script already prints for its one existing caller, the
// Lefthook job. Neither script changes: the marker contract in checkpoint.py's own adapter
// (`.config/agent/hooks/checkpoint.py`, deleted in H4) is replicated here because Go is now
// the caller that contract was written for.

// checkpointScriptDir is where an adopted repository vendors the two evaluators.
const checkpointScriptDir = ".config/lefthook/scripts"

// checkpointOutputLimit mirrors the Python scripts' own LIMIT (1 MiB).
const checkpointOutputLimit = 1 << 20

// Marker lines the scripts print on stdout; see checkpoint.py's MARKER and
// checkpoint_scope.py's PASSED.
const (
	checkpointResultMarker = "PRAETOR_CHECKPOINT_RESULT="
	checkpointScopeMarker  = "PRAETOR_CHECKPOINT_SCOPE_OK"
)

// ErrNoInterpreter reports that no candidate in the search order resolved on this host.
var ErrNoInterpreter = errors.New("no Python interpreter")

// ResolveInterpreter tries each candidate's command in order and returns the first one that
// resolves, with its own literal arguments appended ahead of the caller's. getenv is injected,
// as every other environment read in this package, so resolution never touches process state
// itself and a test can point PATH at a directory of stub executables.
func ResolveInterpreter(getenv func(string) string, candidates [][]string) ([]string, error) {
	if getenv == nil {
		return nil, errors.New("interpreter resolution requires an environment reader")
	}
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		path, err := lookPath(getenv, candidate[0])
		if err != nil {
			continue
		}
		argv := append([]string{path}, candidate[1:]...)
		return argv, nil
	}
	return nil, ErrNoInterpreter
}

// lookPath resolves name against the injected PATH, mirroring os/exec.LookPath's rule that a
// name already containing a separator is used as-is, without reading the real process
// environment (Getenv is the one source of truth here, as in policy.go's Environment check).
func lookPath(getenv func(string) string, name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return tryExecutable(getenv, name)
	}
	for _, dir := range filepath.SplitList(getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		if path, err := tryExecutable(getenv, filepath.Join(dir, name)); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, ErrNoInterpreter)
}

func tryExecutable(getenv func(string) string, path string) (string, error) {
	if runtime.GOOS == "windows" {
		return windowsExecutable(getenv, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not an executable regular file", path)
	}
	return path, nil
}

// windowsExecutable matches CreateProcess's own PATHEXT search: an exact match against a
// listed extension, or the first listed extension that exists. Untested on this leg (HISS-21):
// the runner is Linux; the logic is exercised by TestResolveInterpreter's injected-PATH cases
// on every OS, PATHEXT matching itself only by a Windows CI leg.
func windowsExecutable(getenv func(string) string, path string) (string, error) {
	if ext := filepath.Ext(path); ext != "" && hasPathExt(getenv, ext) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	for _, ext := range pathExtList(getenv) {
		if info, err := os.Stat(path + ext); err == nil && !info.IsDir() {
			return path + ext, nil
		}
	}
	return "", fmt.Errorf("%s: no matching executable extension", path)
}

func pathExtList(getenv func(string) string) []string {
	raw := getenv("PATHEXT")
	if raw == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	list := make([]string, 0, 4)
	for _, ext := range strings.Split(raw, ";") {
		if ext != "" {
			list = append(list, ext)
		}
	}
	return list
}

func hasPathExt(getenv func(string) string, ext string) bool {
	for _, candidate := range pathExtList(getenv) {
		if strings.EqualFold(candidate, ext) {
			return true
		}
	}
	return false
}

// checkpointScript is the absolute path of one evaluator script vendored under a governed
// repository root.
func checkpointScript(root, name string) string {
	return filepath.Join(root, checkpointScriptDir, name)
}

// runInterpreter executes argv (a resolved interpreter plus the candidate's own arguments)
// against script and scriptArgs, in dir, with stdin delivered verbatim when non-empty. -B (no
// .pyc writes) is always passed, matching every existing Lefthook job registration for these
// two scripts.
func runInterpreter(ctx context.Context, argv []string, dir, script string, scriptArgs []string, stdin []byte) (util.CommandBytes, error) {
	args := make([]string, 0, len(argv)-1+2+len(scriptArgs))
	args = append(args, argv[1:]...)
	args = append(args, "-B", script)
	args = append(args, scriptArgs...)
	if len(stdin) > 0 {
		var err error
		ctx, err = util.WithCommandStdin(ctx, stdin)
		if err != nil {
			return util.CommandBytes{}, err
		}
	}
	return util.RunCommandBytes(ctx, dir, argv[0], checkpointOutputLimit, args...)
}

// extractMarker finds the exactly-one stdout line starting with marker, matching the
// contract the Python adapter enforced on the Lefthook job's output ("shared checkpoint job
// returned no unique execution result"). Zero or more than one match is an error: this is
// what "missing or duplicate marker denies" means in the H2 test table.
func extractMarker(stdout []byte, marker string) (string, error) {
	found, count := "", 0
	for _, line := range strings.Split(string(stdout), "\n") {
		if rest, ok := strings.CutPrefix(line, marker); ok {
			count++
			found = rest
		}
	}
	if count != 1 {
		return "", fmt.Errorf("expected exactly one %s line, found %d", strings.TrimSuffix(marker, "="), count)
	}
	return found, nil
}

// checkpointReport is the subset of checkpoint.py's JSON result this evaluator reads. Shape
// validation replicates `.config/agent/hooks/checkpoint.py:checkpoint()`'s checks (schema
// version, due implies actions); Go is now that function's caller.
type checkpointReport struct {
	SchemaVersion int      `json:"schema_version"`
	Enabled       bool     `json:"enabled"`
	Due           bool     `json:"due"`
	Actions       []string `json:"actions"`
	Error         string   `json:"error"`
}

func decodeCheckpointReport(data string) (checkpointReport, error) {
	var report checkpointReport
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return checkpointReport{}, fmt.Errorf("checkpoint result: %w", err)
	}
	if report.SchemaVersion != 1 {
		return checkpointReport{}, errors.New("checkpoint result requires schema_version 1")
	}
	if report.Error != "" {
		return checkpointReport{}, fmt.Errorf("checkpoint evaluator: %s", report.Error)
	}
	if report.Due && len(report.Actions) == 0 {
		return checkpointReport{}, errors.New("due checkpoint has no actionable disposition")
	}
	return report, nil
}

// checkpointFailureReason renders one bounded diagnostic from whichever of a run error, a
// marker error or the child's stderr is available, in that order of usefulness.
func checkpointFailureReason(runErr, markerErr error, result util.CommandBytes) string {
	if runErr != nil {
		return runErr.Error()
	}
	if stderr := strings.TrimSpace(string(result.Stderr)); stderr != "" {
		return stderr
	}
	return markerErr.Error()
}
