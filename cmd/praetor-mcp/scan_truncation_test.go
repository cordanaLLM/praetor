package main

import (
	"context"
	"errors"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditBaselineRatchetRejectsTruncatedScan(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc bad() {" + strings.Repeat("panic(1);", hiss.MaxInfractionsCap+1) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "overflow.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".standards-baseline.json")
	if err := baseline.SaveBaseline(path, &baseline.Baseline{Version: 1}); err != nil {
		t.Fatal(err)
	}
	report, err := auditBaselineRatchet(context.Background(), root, path)
	if !errors.Is(err, hiss.ErrScanTruncated) || report != "" {
		t.Fatalf("capped scan must not reach ratchet success: report=%q err=%v", report, err)
	}
}
