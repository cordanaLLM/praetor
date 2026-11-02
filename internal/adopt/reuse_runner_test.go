// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestReuseWorkflowRunsRepositoryRunnerAndPropagatesFailure(t *testing.T) {
	workflow := mustReuseWorkflow(t, "main")
	start := strings.Index(workflow, "          if [ -f tools/reuse-lint.sh ]; then")
	if start < 0 {
		t.Fatal("hosted REUSE workflow omits the repository's licence coverage runner")
	}
	if !strings.Contains(workflow[:start], "--require-hashes -r .config/reuse/requirements.txt") {
		t.Fatal("repository runner must follow the hash-pinned environment install")
	}
	for _, runner := range []bool{true, false} {
		root := t.TempDir()
		venv := filepath.Join(root, "praetor-reuse", "bin")
		mustWrite(t, filepath.Join(venv, "reuse"), "#!/bin/sh\nprintf direct > result\n")
		if err := os.Chmod(filepath.Join(venv, "reuse"), 0o700); err != nil {
			t.Fatal(err)
		}
		if runner {
			mustWrite(t, filepath.Join(root, "tools/reuse-lint.sh"), "#!/bin/sh\nprintf coverage > result\nexit 7\n")
		}
		t.Setenv("RUNNER_TEMP", root)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		out, err := util.RunCommandBytes(ctx, root, "bash", 4096, "-c", workflow[start:])
		cancel()
		if (err != nil) != runner {
			t.Fatalf("runner=%v err=%v output=%s", runner, err, out.Stderr)
		}
		want := "direct"
		if runner {
			want = "coverage"
		}
		if got := mustRead(t, filepath.Join(root, "result")); got != want {
			t.Fatalf("runner=%v result=%q want=%q", runner, got, want)
		}
	}
}
