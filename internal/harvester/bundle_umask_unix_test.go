//go:build unix

package harvester

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBundlePreservesUmask(t *testing.T) {
	if root := os.Getenv("PRAETOR_TEST_BUNDLE_UMASK_ROOT"); root != "" {
		// Only the isolated child changes umask. Restore its original mask before
		// the testing runtime writes coverage metadata during process shutdown.
		previous := syscall.Umask(0o777)
		defer syscall.Umask(previous)
		_, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: filepath.Join(root, "home"), OutputDir: filepath.Join(root, "out")})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "home", ".gemini", "config", "mcp_config.json"), "secret")
	mustMkdirAll(t, filepath.Join(root, "out", "agent-configs"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestBundlePreservesUmask$", "-test.count=1")
	command.Env = append(os.Environ(), "PRAETOR_TEST_BUNDLE_UMASK_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("umask child failed: %v: %s", err, output)
	}
	for _, relative := range []string{"manifest.json", "agent-configs/mcp_config.json"} {
		info, err := os.Stat(filepath.Join(root, "out", relative))
		if err != nil || info.Mode().Perm() != 0 {
			t.Fatalf("umask widened for %s: %v, %v", relative, info, err)
		}
	}
}
