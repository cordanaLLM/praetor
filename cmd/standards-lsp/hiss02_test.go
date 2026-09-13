package main

import (
	"strings"
	"testing"
)

func hiss02Messages(t *testing.T, src string) []string {
	t.Helper()
	srv := NewServer(strings.NewReader(""), &strings.Builder{}, "")
	diags, err := srv.AnalyzeGoSource("file:///sample.go", src)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	var messages []string
	for _, d := range diags {
		if d.Code == "HISS-02" {
			messages = append(messages, d.Message)
		}
	}
	return messages
}

func containsSubstring(messages []string, want string) bool {
	for _, m := range messages {
		if strings.Contains(m, want) {
			return true
		}
	}
	return false
}

func TestHISS02_Positive_FlagsConditionOnlyAndValuelessRangeLoops(t *testing.T) {
	src := `package sample

import "time"

func CondOnly(done bool) {
	for !done {
		_ = done
	}
}

func Ticker(t *time.Ticker) {
	for range t.C {
		_ = t
	}
}
`
	messages := hiss02Messages(t, src)
	if !containsSubstring(messages, "Condition-only loop") {
		t.Errorf("expected a condition-only loop to be reported, got %v", messages)
	}
	if !containsSubstring(messages, "Range loop without key or value") {
		t.Errorf("expected `for range ch` to be reported, got %v", messages)
	}
}

func TestHISS02_Negative_BoundedLoopsAndContextualIOAreNotFlagged(t *testing.T) {
	src := `package sample

import (
	"context"
	"os/exec"
)

func Bounded(items []string, ctx context.Context) {
	for i := 0; i < 100; i++ {
		_ = i
	}
	for i, item := range items {
		_ = i
		_ = item
		_ = exec.CommandContext(ctx, "git", "status")
	}
}
`
	messages := hiss02Messages(t, src)
	if len(messages) != 0 {
		t.Errorf("bounded loops with context-carrying I/O must produce no HISS-02 diagnostics, got %v", messages)
	}
}

func TestHISS02_Positive_ContextFreeIOInsideLoopIsFlagged(t *testing.T) {
	src := `package sample

import (
	"net/http"
	"os/exec"
)

func Fetch(urls []string) {
	for _, u := range urls {
		_, _ = http.Get(u)
		_ = exec.Command("git", "status")
	}
}
`
	messages := hiss02Messages(t, src)
	if !containsSubstring(messages, "http.Get") {
		t.Errorf("expected http.Get inside a loop to be reported, got %v", messages)
	}
	if !containsSubstring(messages, "exec.CommandContext") {
		t.Errorf("expected exec.Command to be reported with its context-carrying alternative, got %v", messages)
	}
}

func TestHISS02_Boundary_EmptyFileAndUnboundedFor(t *testing.T) {
	if messages := hiss02Messages(t, "package sample\n"); len(messages) != 0 {
		t.Errorf("an empty file must produce no HISS-02 diagnostics, got %v", messages)
	}

	// The fixture is assembled from fragments so the repository's own line-based HISS
	// scanner does not read this test's literal as an unbounded loop in production code.
	src := "package sample\n\nfunc Forever() {\n\tfor " + "{\n\t\t_ = 1\n\t}\n}\n"
	if messages := hiss02Messages(t, src); !containsSubstring(messages, "Unbounded loop construct") {
		t.Errorf("expected `for {}` to stay reported, got %v", messages)
	}
}

func TestReadHeaderLine_3D(t *testing.T) {
	srv := NewServer(strings.NewReader("Content-Length: 2\r\n"), &strings.Builder{}, "")
	line, err := srv.readHeaderLine()
	if err != nil {
		t.Fatalf("readHeaderLine: %v", err)
	}
	if strings.TrimRight(line, "\r") != "Content-Length: 2" {
		t.Errorf("unexpected header line %q", line)
	}

	// Negative: a stream that never sends a newline must be rejected at the byte bound
	// instead of buffering the whole stream.
	flood := strings.Repeat("x", maxLSPHeaderLineBytes+10)
	srv = NewServer(strings.NewReader(flood), &strings.Builder{}, "")
	if _, err := srv.readHeaderLine(); err == nil {
		t.Error("expected an unterminated header line to be rejected")
	}

	// Boundary: an empty stream reports EOF rather than an empty line.
	srv = NewServer(strings.NewReader(""), &strings.Builder{}, "")
	if _, err := srv.readHeaderLine(); err == nil {
		t.Error("expected EOF on an empty stream")
	}
}
