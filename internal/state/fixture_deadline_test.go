// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// unboundedStateFixture matches a test that hands SyncState or the ledger bootstrap a context
// without a deadline: the bootstrap then applies its own fallback (defaultBootstrapTimeout), which
// cut three agenthook fixtures on a loaded Windows runner (#1810).
var unboundedStateFixture = regexp.MustCompile(`\b(?:state\.)?(?:SyncState|InitWorkingDirIfAbsentContext)\(\s*context\.(?:Background|TODO)\(\)`)

// maxScannedFiles bounds the source walk of TestStateFixturesCarryTheTestDeadline (HISS-02).
const maxScannedFiles = 20000

// skippedSourceDirs are folders that hold no tracked engine source: nested worktrees, private state
// and dependency trees.
var skippedSourceDirs = map[string]bool{".git": true, ".claude": true, ".standards": true, ".workingdir": true,
	"node_modules": true, "third_party": true, "vendor": true, "testdata": true}

// unboundedStateFixtures lists "path:line" for every Go test file below root that passes a context
// without a deadline to SyncState or the ledger bootstrap.
func unboundedStateFixtures(root string) ([]string, error) {
	var found []string
	seen := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return skipSourceDir(root, path, entry.Name())
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if seen++; seen > maxScannedFiles {
			return errors.New("more test files than the scan bound")
		}
		hits, err := unboundedLines(path)
		for i := 0; i < len(hits); i++ {
			found = append(found, strings.TrimPrefix(path, root+string(filepath.Separator))+":"+strconv.Itoa(hits[i]))
		}
		return err
	})
	return found, err
}

// skipSourceDir skips a folder below root that holds no tracked engine source.
func skipSourceDir(root, path, name string) error {
	if path != root && skippedSourceDirs[name] {
		return filepath.SkipDir
	}
	return nil
}

// unboundedLines returns the 1-based line numbers of path that match unboundedStateFixture.
func unboundedLines(path string) ([]int, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- paths come from walking the module's own tree
	if err != nil {
		return nil, err
	}
	var hits []int
	for no, line := range strings.Split(string(data), "\n") {
		if unboundedStateFixture.MatchString(line) {
			hits = append(hits, no+1)
		}
	}
	return hits, nil
}

// moduleRoot is the engine module's root, two folders above this file.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller found no source path")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// The engine's tests hand state fixtures the test's own deadline (testsupport.DeadlineContext), so
// the bootstrap's 10 s fallback never cuts fixture setup on a slow runner (#1810). Positive: the
// tracked tests hold no unbounded call. Negative, planted: a Background and a TODO call are found,
// a DeadlineContext call is not.
func TestStateFixturesCarryTheTestDeadline(t *testing.T) {
	found, err := unboundedStateFixtures(moduleRoot(t))
	if err != nil || len(found) != 0 {
		t.Fatalf("tests pass an unbounded context to the state bootstrap: %v %v", found, err)
	}
	planted := t.TempDir()
	call := "SyncState(context." // split so this file does not match itself
	body := "package x\n" +
		"func a() { state." + call + "Background(), root, \"x\") }\n" +
		"func b() { " + call + "TODO(), root, \"x\") }\n" +
		"func c() { state.SyncState(testsupport.DeadlineContext(t), root, \"x\") }\n"
	if err := os.WriteFile(filepath.Join(planted, "planted_test.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err = unboundedStateFixtures(planted)
	if err != nil || strings.Join(found, ",") != "planted_test.go:2,planted_test.go:3" {
		t.Fatalf("planted unbounded calls: got %v %v", found, err)
	}
}

// slowInitialize stands for a ledger initialization on a loaded runner: it needs 200 ms and gives
// up when its context ends first.
func slowInitialize(ctx context.Context, _ *os.Root) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// Planted slow bootstrap (#1810): with a fallback shorter than the initialization, a context
// without a deadline is cut (the failure the agenthook fixtures met), while the test's own
// deadline context keeps the fallback out and the bootstrap completes.
func TestBootstrapFallbackOnlyCutsAnUnboundedContext(t *testing.T) {
	deps := bootstrapDeps{fallback: 20 * time.Millisecond, initialize: slowInitialize}
	if _, err := bootstrapWorkingDir(context.Background(), t.TempDir(), deps); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an unbounded context must meet the fallback: %v", err)
	}
	if _, err := bootstrapWorkingDir(testsupport.DeadlineContext(t), t.TempDir(), deps); err != nil {
		t.Fatalf("the test deadline context must keep the fallback out: %v", err)
	}
}
