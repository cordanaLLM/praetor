package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRequiredStatusContextsCompleteInventoryBounds(t *testing.T) {
	root := t.TempDir()
	if names, err := RequiredStatusContexts(t.Context(), root); err != nil || len(names) != 0 {
		t.Fatalf("no workflows: %v %v", names, err)
	}
	for i := 0; i < maxWorkflowFiles; i++ {
		writeWorkflowFixture(t, root, fmt.Sprintf("%02d.yml", i), fmt.Sprintf("on: pull_request\njobs:\n  check_%d: {}\n", i))
	}
	if names, err := RequiredStatusContexts(t.Context(), root); err != nil || len(names) != maxWorkflowFiles {
		t.Fatalf("complete maximum inventory: %d %v", len(names), err)
	}
	writeWorkflowFixture(t, root, "overflow.yml", "on: pull_request\njobs: {}\n")
	if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
		t.Fatal("incomplete directory returned successful or partial contexts")
	}
	root = t.TempDir()
	var jobs strings.Builder
	jobs.WriteString("on: pull_request\njobs:\n")
	for i := 0; i <= maxJobsPerFile; i++ {
		fmt.Fprintf(&jobs, "  check_%d: {}\n", i)
	}
	writeWorkflowFixture(t, root, "large.yml", jobs.String())
	if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
		t.Fatal("incomplete job inventory returned successful or partial contexts")
	}
}

func TestRequiredStatusContextsRejectsUnsafeSources(t *testing.T) {
	for _, kind := range []string{"file-symlink", "directory-symlink", "oversize", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
			var err error
			switch kind {
			case "file-symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink(writeWorkflowFixture(t, t.TempDir(), "ci.yml", "jobs: {}"), path)
				}
			case "directory-symlink":
				root = t.TempDir()
				err = os.Symlink(filepath.Dir(filepath.Dir(path)), filepath.Join(root, ".github"))
			case "oversize":
				err = os.WriteFile(path, []byte(strings.Repeat(" ", (1<<20)+1)), 0o600)
			case "malformed":
				err = os.WriteFile(path, []byte("jobs: ["), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if names, err := RequiredStatusContexts(t.Context(), root); err == nil || names != nil {
				t.Fatalf("accepted unsafe %s workflow", kind)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RequiredStatusContexts(ctx, t.TempDir()); err == nil {
		t.Fatal("cancelled workflow read succeeded")
	}
}
