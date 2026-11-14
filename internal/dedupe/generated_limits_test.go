package dedupe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// foreignLimitScope builds an inventory with generated paths before eligible paths.
func foreignLimitScope(generated, eligible int) sourceScope {
	scope := sourceScope{linguistGenerated: make(map[string]struct{}, generated)}
	for i := 0; i < generated+eligible && i < maxScopeEntries; i++ {
		path := fmt.Sprintf("file%06d.py", i)
		scope.add("python", path)
		if i < generated {
			scope.linguistGenerated[path] = struct{}{}
		}
	}
	return scope
}

// TestForeignEligibleLimit checks generated prefixes, the exact cap and fail-closed overflow.
func TestForeignEligibleLimit(t *testing.T) {
	for _, tc := range []struct {
		name                string
		generated, eligible int
		wantError           bool
	}{
		{"generated_prefix", maxForeignScanFiles, 1, false},
		{"at_limit", 1, maxForeignScanFiles, false},
		{"overflow", 0, maxForeignScanFiles + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := foreignLimitScope(tc.generated, tc.eligible)
			files, skipped, err := collectForeignFiles(t.Context(), ".", ".", nil, scope, false)
			if tc.wantError {
				if err == nil || files != nil {
					t.Fatalf("overflow returned partial scan: files=%d err=%v", len(files), err)
				}
				return
			}
			if err != nil || len(files) != tc.eligible || skipped != tc.generated {
				t.Fatalf("files/skipped/error = %d/%d/%v, want %d/%d/nil", len(files), skipped, err, tc.eligible, tc.generated)
			}
		})
	}
}

// TestForeignAcrossEligibleLimit checks the combined cap after filtering an extra root.
func TestForeignAcrossEligibleLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "extra.py"), []byte("pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := foreignLimitScope(1, maxForeignScanFiles)
	files, _, err := collectForeignFiles(t.Context(), ".", ".", []rootMapping{{raw: root, abs: root}}, scope, true)
	if err == nil || files != nil {
		t.Fatalf("cross-root overflow returned partial scan: files=%d err=%v", len(files), err)
	}
}

// TestForeignScopeOverflow checks an oversized generated-only scope fails instead of truncating skips.
func TestForeignScopeOverflow(t *testing.T) {
	scope := sourceScope{
		foreignFiles:      make([]string, maxScopeEntries+1),
		linguistGenerated: map[string]struct{}{"": {}},
	}
	files, _, err := collectForeignFiles(t.Context(), ".", ".", nil, scope, false)
	if err == nil || files != nil {
		t.Fatalf("oversized scope returned partial scan: files=%d err=%v", len(files), err)
	}
}

// TestLinguistAttributeBatches checks a valid response larger than one probe's cap is complete.
func TestLinguistAttributeBatches(t *testing.T) {
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.py linguist-generated=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, maxForeignScanFiles+1)
	for i := 0; i < len(paths) && i < maxScopeEntries; i++ {
		paths[i] = fmt.Sprintf("%s%06d.py", strings.Repeat("a", 64), i)
	}
	calls := 0
	realProbe := linguistGitProbe // the production check-attr reader this test wraps
	SetLinguistGitProbeForTest(t, func(ctx context.Context, dir string, cap int, args ...string) (util.CommandBytes, error) {
		calls++
		result, err := realProbe(ctx, dir, cap, args...)
		if err != nil && len(result.Stdout) == cap {
			t.Error("ordinary attribute batch exceeded the output cap")
		}
		return result, err
	})
	generated, err := linguistGeneratedPaths(t.Context(), root, paths)
	if err != nil || len(generated) != len(paths) || calls < 2 {
		t.Fatalf("generated/calls/error = %d/%d/%v, want %d/multiple/nil", len(generated), calls, err, len(paths))
	}
}

// TestLinguistBatchFailure checks later failures discard partial classification and name the root.
func TestLinguistBatchFailure(t *testing.T) {
	paths := make([]string, maxLinguistBatchPaths+1)
	for i := 0; i < len(paths) && i < maxScopeEntries; i++ {
		paths[i] = "a.py"
	}
	calls := 0
	SetLinguistGitProbeForTest(t, func(_ context.Context, _ string, _ int, _ ...string) (util.CommandBytes, error) {
		calls++
		if calls == 1 {
			return util.CommandBytes{Stdout: []byte("a.py\x00linguist-generated\x00set\x00")}, nil
		}
		return util.CommandBytes{}, fmt.Errorf("later batch failed")
	})
	generated, err := linguistGeneratedPaths(t.Context(), "fixture-root", paths)
	if err == nil || generated != nil || !strings.Contains(err.Error(), "fixture-root") || !strings.Contains(err.Error(), "linguist-generated") {
		t.Fatalf("partial classification/error = %v/%v", generated, err)
	}
}

// TestLinguistBatchOverflow checks arbitrary values cause bounded retries and single-path failure.
func TestLinguistBatchOverflow(t *testing.T) {
	for _, failAlways := range []bool{false, true} {
		t.Run(fmt.Sprint(failAlways), func(t *testing.T) {
			checkLinguistOverflow(t, failAlways)
		})
	}
}

// checkLinguistOverflow exercises output-cap recovery without concealing single-record failure.
func checkLinguistOverflow(t *testing.T, failAlways bool) {
	t.Helper()
	calls := 0
	SetLinguistGitProbeForTest(t, func(_ context.Context, _ string, cap int, _ ...string) (util.CommandBytes, error) {
		calls++
		if calls == 1 || failAlways {
			return util.CommandBytes{Stdout: make([]byte, cap)}, fmt.Errorf("command output exceeds cap")
		}
		return util.CommandBytes{Stdout: []byte("a.py\x00linguist-generated\x00set\x00")}, nil
	})
	generated, err := linguistGeneratedPaths(t.Context(), "fixture-root", []string{"a.py", "a.py"})
	if failAlways {
		if err == nil || generated != nil || calls != 2 {
			t.Fatalf("single-path failure: generated=%v err=%v calls=%d", generated, err, calls)
		}
		return
	}
	if err != nil || len(generated) != 1 || calls != 3 {
		t.Fatalf("overflow recovery: generated=%v err=%v calls=%d", generated, err, calls)
	}
}

// TestLinguistScopeBoundaries checks empty and oversized inventories never issue a probe.
func TestLinguistScopeBoundaries(t *testing.T) {
	SetLinguistGitProbeForTest(t, func(_ context.Context, _ string, _ int, _ ...string) (util.CommandBytes, error) {
		t.Fatal("unexpected attribute lookup")
		return util.CommandBytes{}, nil
	})
	if generated, err := linguistGeneratedPaths(t.Context(), ".", nil); err != nil || len(generated) != 0 {
		t.Fatalf("empty scope = %v/%v", generated, err)
	}
	if _, err := linguistGeneratedPaths(t.Context(), ".", make([]string, maxScopeEntries+1)); err == nil {
		t.Fatal("oversized scope accepted")
	}
}
