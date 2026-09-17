package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const registerTestSource = "# Sample Agent Harness\n\nRun verification before concluding any turn.\n"

func writeRegisterFixture(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

func readRegisterFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSyncRegisterBlockPositive(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	writeRegisterFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n  evidence:\n    inline_max_lines: 40\n")
	agents := writeRegisterFixture(t, root, "AGENTS.md",
		registerTestSource+"\n"+config.RegisterSectionPrefix+config.RegisterBlockStart+"\nstale\n"+config.RegisterBlockEnd+"\n\n## After\n")

	changed, err := SyncRegisterBlock(ctx, root, agents, true)
	if err != nil || !changed {
		t.Fatalf("first sync: changed=%v err=%v", changed, err)
	}
	source := readRegisterFixture(t, agents)
	if strings.Contains(source, "stale") || !strings.Contains(source, "ci_debugging") || !strings.Contains(source, "Evidence above 40 lines") {
		t.Fatalf("block was not rendered from the manifest:\n%s", source)
	}
	if !strings.HasSuffix(source, config.RegisterBlockEnd+"\n\n## After\n") {
		t.Fatalf("content after the block must survive:\n%s", source)
	}

	tr := NewTranspiler()
	res, err := tr.CompileContext(ctx, agents)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, file := range res.Files {
		if strings.Count(file.Content, config.RegisterBlockStart) != 1 || strings.Count(file.Content, config.RegisterBlockHeading) != 1 {
			t.Errorf("%s must carry the block exactly once", file.RelativePath)
		}
	}
	if err := tr.WriteOutputsContext(ctx, res, root); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
	if err := tr.VerifyContext(ctx, agents, root); err != nil {
		t.Fatalf("verify after sync: %v", err)
	}
	if changed, err = SyncRegisterBlock(ctx, root, agents, false); err != nil || changed {
		t.Fatalf("verifying sync after a write must be clean: changed=%v err=%v", changed, err)
	}
}

func TestSyncRegisterBlockNegative(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown label fails before any write", func(t *testing.T) {
		root := t.TempDir()
		writeRegisterFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
		agents := writeRegisterFixture(t, root, "AGENTS.md", registerTestSource)
		_, err := SyncRegisterBlock(ctx, root, agents, true)
		if err == nil || !strings.Contains(err.Error(), `register task "deploy_prod" is not a declared target_tasks label`) {
			t.Fatalf("error = %v, want the undeclared label", err)
		}
		if got := readRegisterFixture(t, agents); got != registerTestSource {
			t.Fatalf("source was written despite the error:\n%s", got)
		}
	})

	t.Run("stale block is drift when verifying", func(t *testing.T) {
		root := t.TempDir()
		stale := registerTestSource + "\n" + config.RegisterSectionPrefix + config.RegisterBlockStart + "\nhand edit\n" + config.RegisterBlockEnd + "\n"
		agents := writeRegisterFixture(t, root, "AGENTS.md", stale)
		changed, err := SyncRegisterBlock(ctx, root, agents, false)
		if !errors.Is(err, ErrRegisterBlockOutOfSync) || !changed {
			t.Fatalf("changed=%v err=%v, want %v", changed, err, ErrRegisterBlockOutOfSync)
		}
		if got := readRegisterFixture(t, agents); got != stale {
			t.Fatal("a verifying sync must never write the source")
		}
	})

	t.Run("unterminated marker", func(t *testing.T) {
		root := t.TempDir()
		agents := writeRegisterFixture(t, root, "AGENTS.md", registerTestSource+config.RegisterBlockStart+"\nrest of the file\n")
		if _, err := SyncRegisterBlock(ctx, root, agents, true); !errors.Is(err, util.ErrMarkedBlockUnbalanced) {
			t.Fatalf("error = %v, want %v", err, util.ErrMarkedBlockUnbalanced)
		}
	})

	t.Run("missing source and cancelled context", func(t *testing.T) {
		root := t.TempDir()
		if _, err := SyncRegisterBlock(ctx, root, filepath.Join(root, "AGENTS.md"), true); err == nil {
			t.Fatal("a missing AGENTS.md must be an error")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := LoadRegisterBlock(cancelled, root); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled context: error = %v", err)
		}
	})
}

func TestSyncRegisterBlockBoundary(t *testing.T) {
	ctx := context.Background()

	t.Run("no manifest and no routing renders the defaults and appends once", func(t *testing.T) {
		root := t.TempDir()
		agents := writeRegisterFixture(t, root, "AGENTS.md", registerTestSource)
		if changed, err := SyncRegisterBlock(ctx, root, agents, true); err != nil || !changed {
			t.Fatalf("append: changed=%v err=%v", changed, err)
		}
		block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy())
		if err != nil {
			t.Fatalf("render defaults: %v", err)
		}
		if want := registerTestSource + "\n" + config.RegisterSectionPrefix + block + "\n"; readRegisterFixture(t, agents) != want {
			t.Fatalf("appended source:\n%s\nwant:\n%s", readRegisterFixture(t, agents), want)
		}
		if changed, err := SyncRegisterBlock(ctx, root, agents, true); err != nil || changed {
			t.Fatalf("second sync must change nothing: changed=%v err=%v", changed, err)
		}
	})

	t.Run("line budget", func(t *testing.T) {
		block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy())
		if err != nil {
			t.Fatalf("render defaults: %v", err)
		}
		section := strings.Count(config.RegisterSectionPrefix+block, "\n") + 1
		for extra, wantErr := range map[int]error{0: nil, 1: util.ErrMarkedBlockBudget} {
			root := t.TempDir()
			// The source plus one separating blank line plus the section lands on the budget.
			source := strings.Repeat("line\n", MaxLineBudget-section-1+extra)
			agents := writeRegisterFixture(t, root, "AGENTS.md", source)
			if _, err := SyncRegisterBlock(ctx, root, agents, true); !errors.Is(err, wantErr) {
				t.Fatalf("budget +%d: error = %v, want %v", extra, err, wantErr)
			}
		}
	})
}

// A Windows checkout holds AGENTS.md with CRLF endings. The splice must neither report
// that as drift nor mix line endings when it writes.
func TestSyncRegisterBlockIsLineEndingNeutral(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	agents := writeRegisterFixture(t, root, "AGENTS.md", strings.ReplaceAll(registerTestSource, "\n", "\r\n"))
	if changed, err := SyncRegisterBlock(ctx, root, agents, true); err != nil || !changed {
		t.Fatalf("append to a CRLF source: changed=%v err=%v", changed, err)
	}
	spliced := readRegisterFixture(t, agents)
	if strings.Count(spliced, "\n") != strings.Count(spliced, "\r\n") || !strings.Contains(spliced, config.RegisterBlockStart+"\r\n") {
		t.Fatalf("a CRLF source must stay CRLF throughout:\n%q", spliced)
	}
	if changed, err := SyncRegisterBlock(ctx, root, agents, false); err != nil || changed {
		t.Fatalf("an in-sync CRLF source must verify: changed=%v err=%v", changed, err)
	}
	// The same block with LF endings is equally in sync: only content counts.
	writeRegisterFixture(t, root, "AGENTS.md", strings.ReplaceAll(spliced, "\r\n", "\n"))
	if changed, err := SyncRegisterBlock(ctx, root, agents, false); err != nil || changed {
		t.Fatalf("an in-sync LF source must verify: changed=%v err=%v", changed, err)
	}
	// A stale block is still drift under CRLF.
	writeRegisterFixture(t, root, "AGENTS.md", strings.Replace(spliced, config.RegisterBlockStart+"\r\n", config.RegisterBlockStart+"\r\nhand edit\r\n", 1))
	if _, err := SyncRegisterBlock(ctx, root, agents, false); !errors.Is(err, ErrRegisterBlockOutOfSync) {
		t.Fatalf("stale CRLF block: error = %v, want %v", err, ErrRegisterBlockOutOfSync)
	}
}

func TestLoadRegisterBlock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	policy, block, err := LoadRegisterBlock(ctx, root)
	if err != nil || policy.Resolve(config.SurfaceAgent, "ci_debugging").Register != config.TextRegisterInternal {
		t.Fatalf("defaults: %+v, %v", policy, err)
	}
	if !strings.HasPrefix(block, config.RegisterBlockStart) || !strings.HasSuffix(block, config.RegisterBlockEnd) {
		t.Fatalf("block must be delimited by its markers:\n%s", block)
	}

	// A repository that declares its own labels is validated against them, and its
	// default rows are not an error even though that routing file never names them.
	writeRegisterFixture(t, root, ".config/models/routing.yaml", `version: 1
tiers:
  only:
    description: "single tier"
    target_tasks: [deploy_prod]
    models:
      - {id: "m", family: "local", rpm_limit: 1, tpm_limit: 1, cost_per_m_in: 0, cost_per_m_out: 0}
    fallback_tier: ""
governance:
  max_concurrent_same_model: 1
  exhaustion_threshold_percent: 90
  orthogonal_audit_required: false
`)
	writeRegisterFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: {register: social, max_tokens: 512}\n")
	policy, _, err = LoadRegisterBlock(ctx, root)
	if err != nil {
		t.Fatalf("own label set: %v", err)
	}
	if got := policy.Resolve(config.SurfaceAgent, "deploy_prod"); got.Register != config.TextRegisterSocial || got.MaxTokens != 512 {
		t.Fatalf("resolution = %+v, want social with 512 tokens", got)
	}

	writeRegisterFixture(t, root, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n")
	if _, _, err = LoadRegisterBlock(ctx, root); err == nil || !strings.Contains(err.Error(), `"ci_debugging"`) {
		t.Fatalf("a default-router label is undeclared once routing.yaml exists: %v", err)
	}
	writeRegisterFixture(t, root, ".standards.yaml", "version: 1\nregsiter: {}\n")
	if _, _, err = LoadRegisterBlock(ctx, root); err == nil {
		t.Fatal("an invalid manifest must be an error, not a silent default")
	}
}

func TestLoadRegisterBlockRequiresContext(t *testing.T) {
	var nilContext context.Context
	if _, _, err := LoadRegisterBlock(nilContext, t.TempDir()); err == nil {
		t.Fatal("a nil context must be an error")
	}
}
