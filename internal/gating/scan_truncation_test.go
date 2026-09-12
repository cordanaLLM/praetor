package gating

import (
	"context"
	"errors"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHissStageRejectsTruncatedScanBeforeBaseline(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc bad() {" + strings.Repeat("panic(1);", 1001) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "overflow.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := runHissStage(context.Background(), &stageConfig{repoDir: root})
	if !errors.Is(err, hiss.ErrScanTruncated) || report != "" {
		t.Fatalf("capped scan must fail before loading even a missing baseline: report=%q err=%v", report, err)
	}
}
