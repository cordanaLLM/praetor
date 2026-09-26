// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// failingReader yields data once, then fails, like a connection reset mid-body.
type failingReader struct {
	data []byte
	done bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("connection reset\x1b[2J")
	}
	r.done = true
	return copy(p, r.data), nil
}

func TestBodyPreview_Positive_PlainBodyPasses(t *testing.T) {
	if got := BodyPreview([]byte("  plain message  ")); got != "plain message" {
		t.Fatalf("unexpected preview: %q", got)
	}
	if got := BodyPreview([]byte("line one\nline\ttwo\r")); got != "line one line two" {
		t.Fatalf("line breaks and tabs must become spaces: %q", got)
	}
}

func TestBodyPreview_Negative_ControlBytesStripped(t *testing.T) {
	got := BodyPreview([]byte("a\x1b[31mb\x07c\x00d\x9be\xff"))
	if strings.ContainsAny(got, "\x1b\x07\x00") || strings.ContainsRune(got, '\u009b') {
		t.Fatalf("control characters survived the preview: %q", got)
	}
	if got != "a.[31mb.c.d.e." {
		t.Fatalf("unexpected sanitized preview: %q", got)
	}
}

func TestBodyPreview_Boundary_CapMarksTruncation(t *testing.T) {
	atCap := BodyPreview([]byte(strings.Repeat("x", MaxErrorBodyPreview)))
	if len(atCap) != MaxErrorBodyPreview || strings.Contains(atCap, "truncated") {
		t.Fatalf("a body exactly at the cap must pass whole, got %d bytes: %q", len(atCap), atCap)
	}
	over := BodyPreview([]byte(strings.Repeat("x", MaxErrorBodyPreview+1)))
	if !strings.HasSuffix(over, "... (truncated)") || len(over) != MaxErrorBodyPreview+len("... (truncated)") {
		t.Fatalf("a body over the cap must be cut and marked, got %d bytes", len(over))
	}
	if got := BodyPreview(nil); got != "" {
		t.Fatalf("expected an empty preview for an empty body, got %q", got)
	}
}

func TestReadErrorBody_Positive_PlainBody(t *testing.T) {
	if got := ReadErrorBody(strings.NewReader("  Not Found\n")); got != "Not Found" {
		t.Fatalf("unexpected excerpt: %q", got)
	}
}

func TestReadErrorBody_Negative_EscapesStrippedAndReadFailureReported(t *testing.T) {
	got := ReadErrorBody(strings.NewReader("denied \x1b]0;pwned\x07\x1b[2J"))
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("terminal escape sequences reached the error excerpt: %q", got)
	}
	failed := ReadErrorBody(&failingReader{data: []byte("partial")})
	if !strings.HasPrefix(failed, "partial [reading the response body failed: connection reset") {
		t.Fatalf("read failure not reported: %q", failed)
	}
	if strings.Contains(failed, "\x1b") {
		t.Fatalf("read failure text must be sanitized too: %q", failed)
	}
}

func TestReadErrorBody_Boundary_LargeBodyBoundedAndMarked(t *testing.T) {
	huge := strings.Repeat("E", 2*MaxErrorBodyBytes)
	got := ReadErrorBody(io.LimitReader(strings.NewReader(huge), int64(len(huge))))
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("a body over the preview cap must be marked truncated: %q", got[len(got)-32:])
	}
	if len(got) > MaxErrorBodyPreview+len("... (truncated)") {
		t.Fatalf("excerpt not bounded by the preview cap: %d bytes", len(got))
	}
	exact := ReadErrorBody(strings.NewReader(strings.Repeat("y", MaxErrorBodyPreview)))
	if len(exact) != MaxErrorBodyPreview || strings.Contains(exact, "truncated") {
		t.Fatalf("a body at the cap must pass whole: %d bytes", len(exact))
	}
}
