// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const gosecBuildTimeout = 2 * time.Minute

var (
	pinnedGosecBuiltMu sync.Mutex
	pinnedGosecBuilt   string
)

var cachedPinnedGosecDir = sync.OnceValues(func() (string, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	toolsMod := filepath.Join(repoRoot, "tools", "go", "go.mod")
	binDir, err := os.MkdirTemp("", "praetor-pinned-gosec-*")
	if err != nil {
		return "", fmt.Errorf("create gosec directory: %w", err)
	}
	pinnedGosecBuiltMu.Lock()
	pinnedGosecBuilt = binDir
	pinnedGosecBuiltMu.Unlock()

	outBinary := filepath.Join(binDir, ExecutableName("gosec"))
	ctx, cancel := context.WithTimeout(context.Background(), gosecBuildTimeout)
	defer cancel()
	cmdEnv := os.Environ()
	if modCache := systemGoModCache(); modCache != "" {
		cmdEnv = append(cmdEnv, "GOMODCACHE="+modCache)
	}
	cmdCtx, envErr := util.WithCommandEnvironment(ctx, cmdEnv)
	if envErr != nil {
		return "", fmt.Errorf("set gosec build environment: %w", envErr)
	}
	res, err := util.RunCommandBytes(cmdCtx, repoRoot, "go", util.MaxCommandOutputBytes,
		"build", "-modfile="+toolsMod, "-o", outBinary, "github.com/securego/gosec/v2/cmd/gosec")
	if err != nil {
		return "", util.CommandDiagnostic(fmt.Errorf("build pinned gosec: %w", err), res.Stderr)
	}
	return binDir, nil
})

func systemGoModCache() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := util.RunCommand(ctx, "", "go", "env", "GOMODCACHE")
	if err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// findRepoRoot locates the root of the repository holding tools/go/go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for i := 0; i < 10; i++ {
		target := filepath.Join(dir, "tools", "go", "go.mod")
		if _, statErr := os.Stat(target); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Abs(filepath.Join("..", ".."))
}

// PinnedGosecDir returns the directory containing the built pinned gosec binary,
// building it once if not already cached.
func PinnedGosecDir(t testing.TB) string {
	t.Helper()
	dir, err := cachedPinnedGosecDir()
	if err != nil {
		t.Fatalf("pinned gosec unavailable: %v", err)
	}
	return dir
}

// CleanupPinnedGosec removes the temporary directory containing the built pinned gosec, if any.
func CleanupPinnedGosec() error {
	pinnedGosecBuiltMu.Lock()
	dir := pinnedGosecBuilt
	pinnedGosecBuilt = ""
	pinnedGosecBuiltMu.Unlock()
	if dir != "" {
		return os.RemoveAll(dir)
	}
	return nil
}
