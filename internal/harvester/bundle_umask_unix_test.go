//go:build unix

package harvester

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestBundlePreservesUmask(t *testing.T) {
	if root := os.Getenv("PRAETOR_TEST_BUNDLE_UMASK_ROOT"); root != "" {
		// Only the isolated child changes umask. Restore its original mask before
		// the testing runtime writes coverage metadata during process shutdown.
		previous := syscall.Umask(0o777)
		defer syscall.Umask(previous)
		_, err := BundleWorkstation(context.Background(), BundleOptions{Roots: testClientRoots(filepath.Join(root, "home")), HomeDir: filepath.Join(root, "home"), OutputDir: filepath.Join(root, "out")})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "home", ".gemini", "config", "mcp_config.json"), `{"env":{"TOKEN":"secret"}}`)
	mustMkdirAll(t, filepath.Join(root, "out", "agent-configs"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmdEnv := append(os.Environ(), "PRAETOR_TEST_BUNDLE_UMASK_ROOT="+root)
	cmdCtx, envErr := util.WithCommandEnvironment(ctx, cmdEnv)
	if envErr != nil {
		t.Fatal(envErr)
	}
	if output, err := util.RunCommand(cmdCtx, "", executable, "-test.run=^TestBundlePreservesUmask$", "-test.count=1"); err != nil {
		t.Fatalf("umask child failed: %v: %s", err, output)
	}
	for _, relative := range []string{"manifest.json", "agent-configs/mcp_config.json"} {
		info, err := os.Stat(filepath.Join(root, "out", relative))
		if err != nil || info.Mode().Perm() != 0 {
			t.Fatalf("umask widened for %s: %v, %v", relative, info, err)
		}
	}
}
