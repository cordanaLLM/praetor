// SPDX-License-Identifier: EUPL-1.2

package editor

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type editorIOContextKey struct{}

func TestPublishPreparedEditorFilesUsesOneBoundedContextPerWrite(t *testing.T) {
	pending := []pendingEditorWrite{
		{path: "one", content: "1", write: true, result: WriteResult{Path: "one", Outcome: WriteCreated}},
		{path: "present", write: false, result: WriteResult{Path: "present", Outcome: WritePresent}},
		{path: "two", content: "2", write: true, result: WriteResult{Path: "two", Outcome: WriteCreated}},
	}
	created, cancelled := 0, 0
	newContext := func() (context.Context, context.CancelFunc) {
		created++
		ctx := context.WithValue(context.Background(), editorIOContextKey{}, created)
		return ctx, func() { cancelled++ }
	}
	written := 0
	writer := func(ctx context.Context, _, _ string) error {
		written++
		if got := ctx.Value(editorIOContextKey{}); got != written {
			t.Fatalf("write %d received context marker %v", written, got)
		}
		return nil
	}

	report, err := publishPreparedEditorFiles(pending, newContext, writer)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if created != 2 || cancelled != 2 || written != 2 {
		t.Fatalf("per-write contexts: created=%d cancelled=%d written=%d", created, cancelled, written)
	}
	if len(report.Files) != len(pending) {
		t.Fatalf("reported %d files, want %d", len(report.Files), len(pending))
	}
	ctx, cancel := newEditorIOContext()
	defer cancel()
	if _, bounded := ctx.Deadline(); !bounded {
		t.Fatal("production editor I/O context has no deadline")
	}
}

func TestPublishPreparedEditorFilesCancelsFailedWrite(t *testing.T) {
	want := errors.New("write failed")
	cancelled := 0
	newContext := func() (context.Context, context.CancelFunc) {
		return context.Background(), func() { cancelled++ }
	}
	_, err := publishPreparedEditorFiles([]pendingEditorWrite{{path: "one", write: true}}, newContext,
		func(context.Context, string, string) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if cancelled != 1 {
		t.Fatalf("cancelled %d contexts, want 1", cancelled)
	}
}

func TestPublishPreparedEditorFilesEmptySetCreatesNoContext(t *testing.T) {
	created := 0
	report, err := publishPreparedEditorFiles(nil, func() (context.Context, context.CancelFunc) {
		created++
		return context.Background(), func() {}
	}, func(context.Context, string, string) error {
		t.Fatal("empty set called writer")
		return nil
	})
	if err != nil {
		t.Fatalf("empty publish: %v", err)
	}
	if created != 0 || len(report.Files) != 0 {
		t.Fatalf("empty publish created=%d reports=%d", created, len(report.Files))
	}
}

func TestEditorIOBoundaryMaxFilesKeepsIndependentBudgets(t *testing.T) {
	set := &EditorConfigSet{Editors: []string{EditorUniversal}, Files: make([]GeneratedFile, 0, maxFilesToGenerate)}
	for i := 0; i < maxFilesToGenerate; i++ {
		set.Files = append(set.Files, GeneratedFile{
			Path:    fmt.Sprintf(".generated/editor-%02d.conf", i),
			Content: fmt.Sprintf("entry-%02d\n", i),
			Editor:  EditorUniversal,
		})
	}

	root := t.TempDir()
	report, err := WriteWithReport(set, root)
	if err != nil {
		t.Fatalf("write exact %d-file boundary: %v", maxFilesToGenerate, err)
	}
	if len(report.Files) != maxFilesToGenerate {
		t.Fatalf("write report has %d files, want %d", len(report.Files), maxFilesToGenerate)
	}
	verified, err := VerifyWithReport(set, root)
	if err != nil {
		t.Fatalf("verify exact %d-file boundary: %v", maxFilesToGenerate, err)
	}
	if len(verified.Verified) != maxFilesToGenerate {
		t.Fatalf("verify report has %d files, want %d", len(verified.Verified), maxFilesToGenerate)
	}
}
