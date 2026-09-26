package adopt

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

func TestScanLegacyDebtRejectsTruncationBeforeRecording(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc bad() {" + strings.Repeat("panic(1);", maxInfractionsCap+1) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "overflow.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	base := &baseline.Baseline{Version: 1}
	report := &AdoptReport{DebtBreakdown: map[string]int{}}
	err := scanLegacyDebt(context.Background(), root, base, report, hiss.DefaultMaxFuncLOC)
	if !errors.Is(err, hiss.ErrScanTruncated) {
		t.Fatalf("capped legacy scan must fail: %v", err)
	}
	if len(base.Infractions) != 0 || base.TotalInfractions != 0 || len(report.DebtBreakdown) != 0 {
		t.Fatal("incomplete scan mutated debt records")
	}
}
