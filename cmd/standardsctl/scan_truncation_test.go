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

func TestScanConsumersRejectTruncationBeforeRatchetOrBaselineWrite(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc bad() {" + strings.Repeat("panic(1);", hiss.MaxInfractionsCap+1) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "overflow.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".standards-baseline.json")
	previous := &baseline.Baseline{Version: 1, Infractions: []baseline.Infraction{}}
	if err := baseline.SaveBaseline(path, previous); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = auditBaselineAndInvariants(context.Background(), &auditOptions{rootDir: root, baselinePath: path})
	if !errors.Is(err, hiss.ErrScanTruncated) {
		t.Fatalf("audit must reject capped scan before evaluating ratchet: %v", err)
	}
	err = recordBaseline(path, previous, baseline.RecordOptions{AllowIncrease: true, Rationale: "test cannot authorize incomplete scan"})
	if !errors.Is(err, hiss.ErrScanTruncated) {
		t.Fatalf("record must reject capped scan even with increase authorized: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("incomplete scan changed baseline: %v", err)
	}
}

func TestRecordBaselineRejectsASTDepthTruncationWithoutWriting(t *testing.T) {
	root := t.TempDir()
	source := "package fixture\nfunc f(){" + strings.Repeat("if true {", 1100) + "panic(1)" + strings.Repeat("}", 1100) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "depth.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".standards-baseline.json")
	previous := &baseline.Baseline{Version: 1}
	if err := baseline.SaveBaseline(path, previous); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordBaseline(path, previous, baseline.RecordOptions{}); !errors.Is(err, hiss.ErrScanTruncated) {
		t.Fatalf("hidden violations must not be certified as zero debt: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("depth-limited scan changed baseline: %v", err)
	}
}
