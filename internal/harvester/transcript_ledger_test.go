// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// Record shapes taken from real agent session files; every content value is a placeholder.
const (
	ledgerStopHookSummary = `{"parentUuid":"u6","isSidechain":false,"type":"system","subtype":"stop_hook_summary","hookCount":1,"hookInfos":[{"command":"x","durationMs":1}],"hookErrors":[],"preventedContinuation":false,"level":"suggestion","timestamp":"2026-10-01T10:07:00.000Z","uuid":"u7","toolUseID":"toolu_2","sessionId":"sess-1","gitBranch":"feat/x"}`
	ledgerAssistant       = `{"parentUuid":"u2","isSidechain":false,"message":{"model":"m-1","id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":40}},"requestId":"req_1","type":"assistant","uuid":"u3","timestamp":"2026-10-01T10:03:00.000Z","sessionId":"sess-1","gitBranch":"feat/x"}`
	ledgerHuman           = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"x"}]},"origin":{"kind":"human"},"isSidechain":false,"sessionId":"sess-1","gitBranch":"feat/x"}`
	ledgerMeta            = `{"type":"user","message":{"role":"user","content":"x"},"isMeta":true,"turnCompanion":true,"sessionId":"sess-1"}`
	ledgerCompact         = `{"type":"user","message":{"role":"user","content":"x"},"isCompactSummary":true,"isVisibleInTranscriptOnly":true,"sessionId":"sess-1"}`
	ledgerTaskNotice      = `{"type":"user","message":{"role":"user","content":"x"},"origin":{"kind":"task-notification"},"queueSkipAttachments":true,"sessionId":"sess-1"}`
	ledgerPeer            = `{"type":"user","message":{"role":"user","content":"x"},"origin":{"kind":"peer"},"sessionId":"sess-1"}`
	ledgerSidechain       = `{"type":"user","message":{"role":"user","content":"x"},"origin":{"kind":"human"},"isSidechain":true,"sessionId":"sess-1"}`
)

func TestDecodeLedgerLine_Positive_RealShapes(t *testing.T) {
	record, err := DecodeLedgerLine([]byte(ledgerAssistant))
	if err != nil {
		t.Fatal(err)
	}
	if record.GitBranch != "feat/x" || record.RequestID != "req_1" || record.Message == nil {
		t.Fatalf("assistant decoded wrong: %+v", record)
	}
	if record.Message.ID != "msg_1" || record.Message.Model != "m-1" {
		t.Fatalf("message decoded wrong: %+v", record.Message)
	}
	want := LedgerUsage{InputTokens: 10, CacheCreationInputTokens: 100, CacheReadInputTokens: 1000, OutputTokens: 40}
	if record.Message.Usage == nil || *record.Message.Usage != want {
		t.Fatalf("usage: %+v", record.Message.Usage)
	}
}

func TestDecodeLedgerLine_Positive_FlagsAndOrigins(t *testing.T) {
	kind := func(want string) func(*LedgerRecord) bool {
		return func(r *LedgerRecord) bool { return r.Origin != nil && r.Origin.Kind == want }
	}
	flags := map[string]func(*LedgerRecord) bool{
		ledgerMeta:       func(r *LedgerRecord) bool { return r.IsMeta },
		ledgerCompact:    func(r *LedgerRecord) bool { return r.IsCompactSummary },
		ledgerSidechain:  func(r *LedgerRecord) bool { return r.IsSidechain },
		ledgerPeer:       kind("peer"),
		ledgerTaskNotice: kind("task-notification"),
		ledgerHuman:      kind("human"),
	}
	for line, check := range flags {
		decoded, err := DecodeLedgerLine([]byte(line))
		if err != nil || !check(decoded) {
			t.Fatalf("%s: %+v %v", line, decoded, err)
		}
	}
}

// The system record spells its key toolUseID; the ingest path of main accepts it, so the
// ledger decoder and the ingest path must both accept it.
func TestDecodeLedgerLine_Positive_StopHookSummaryAndIngestParity(t *testing.T) {
	if _, err := DecodeLedgerLine([]byte(ledgerStopHookSummary)); err != nil {
		t.Fatalf("ledger rejects real stop_hook_summary: %v", err)
	}
	source := TranscriptSource{Format: TranscriptFormatClaudeCode, SHA256: "s"}
	events, err := parseTranscript(context.Background(), []byte(ledgerStopHookSummary+"\n"), source)
	if err != nil || len(events) != 1 || !events[0].metadataRecord {
		t.Fatalf("ingest regressed on stop_hook_summary: %v %+v", err, events)
	}
}

func TestDecodeLedgerLine_Negative(t *testing.T) {
	cases := map[string]string{
		"duplicate key":        `{"type":"user","type":"assistant"}`,
		"invalid utf8":         "{\"type\":\"user\",\"x\":\"\xff\"}",
		"unpaired surrogate":   `{"type":"user","x":"\ud800"}`,
		"noncanonical spell":   `{"type":"user","gitBranch":"a","GitBranch":"b"}`,
		"message not object":   `{"type":"user","message":"text"}`,
		"message alias":        `{"type":"user","message":{"ID":"a","id":"b"}}`,
		"not an object":        `[1]`,
		"invalid json":         `{"type":`,
		"usage wrong type":     `{"type":"assistant","message":{"usage":{"output_tokens":"x"}}}`,
		"origin wrong type":    `{"type":"user","origin":"human"}`,
		"flag wrong type":      `{"type":"user","isMeta":"yes"}`,
		"noncanonical origin":  `{"type":"user","origin":{"kind":"human"},"Origin":{"kind":"peer"}}`,
		"noncanonical session": `{"type":"user","sessionId":"a","SessionID":"b"}`,
	}
	for name, line := range cases {
		if _, err := DecodeLedgerLine([]byte(line)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestDecodeLedgerLine_Boundary_NoMessageAndNullMessage(t *testing.T) {
	for _, line := range []string{`{"type":"queue-operation"}`, `{"type":"attachment","message":null}`, `{}`} {
		record, err := DecodeLedgerLine([]byte(line))
		if err != nil || record.Message != nil {
			t.Fatalf("%s: %+v %v", line, record, err)
		}
	}
}

func TestScanTranscriptLines_Positive_VisitsLinesWithNumbers(t *testing.T) {
	var got []string
	err := ScanTranscriptLines(context.Background(), strings.NewReader("a\nb\n\nc"), ScanLimits{MaxRecords: 10}, func(line int, raw []byte) error {
		got = append(got, string(raw))
		return nil
	})
	if err != nil || strings.Join(got, "|") != "a|b||c" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestScanTranscriptLines_Negative_Overflows(t *testing.T) {
	visit := func(int, []byte) error { return nil }
	if err := ScanTranscriptLines(context.Background(), strings.NewReader("a\nb\nc\n"), ScanLimits{MaxRecords: 2}, visit); err == nil || !strings.Contains(err.Error(), "record bound") {
		t.Fatalf("record bound: %v", err)
	}
	if err := ScanTranscriptLines(context.Background(), strings.NewReader("aaaa\nbbbb\n"), ScanLimits{MaxRecords: 10, MaxBytes: 6}, visit); err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("byte bound: %v", err)
	}
	long := bytes.Repeat([]byte("a"), MaxTranscriptLineBytes+10)
	if err := ScanTranscriptLines(context.Background(), bytes.NewReader(long), ScanLimits{MaxRecords: 10}, visit); err == nil || !strings.Contains(err.Error(), "scan transcript") {
		t.Fatalf("line bound: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ScanTranscriptLines(ctx, strings.NewReader("a\n"), ScanLimits{MaxRecords: 10}, visit); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	boom := errors.New("boom")
	if err := ScanTranscriptLines(context.Background(), strings.NewReader("a\n"), ScanLimits{MaxRecords: 10}, func(int, []byte) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("visit error: %v", err)
	}
}

func TestScanTranscriptLines_Boundary_ExactBounds(t *testing.T) {
	visit := func(int, []byte) error { return nil }
	if err := ScanTranscriptLines(context.Background(), strings.NewReader("a\nb\n"), ScanLimits{MaxRecords: 2, MaxBytes: 4}, visit); err != nil {
		t.Fatalf("exact bounds must pass: %v", err)
	}
	line := bytes.Repeat([]byte("a"), MaxTranscriptLineBytes)
	if err := ScanTranscriptLines(context.Background(), bytes.NewReader(line), ScanLimits{MaxRecords: 1}, visit); err != nil {
		t.Fatalf("8 MiB line must pass: %v", err)
	}
}
