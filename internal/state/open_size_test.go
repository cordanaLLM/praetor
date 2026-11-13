// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive, negative and byte boundaries use short lines so the file-size guard,
// rather than the independent task-line bound, decides each outcome.
func TestOpenLedgerSizeSync(t *testing.T) {
	for _, size := range []int{800*1024 - 1, 800 * 1024, 800*1024 + 1, 900 * 1024, 1 << 20, 1<<20 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			testOpenLedgerSizeSync(t, size)
		})
	}
}

func testOpenLedgerSizeSync(t *testing.T, size int) {
	t.Helper()
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("padding\n", size/8) + strings.Repeat(" ", size%8)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), body)
	var warnings bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, nil)))
	defer slog.SetDefault(previous)
	snap, err := SyncState(testsupport.DeadlineContext(t), root, "size fixture")
	if size > 1<<20 {
		if !errors.Is(err, util.ErrFileTooLarge) || !strings.Contains(err.Error(), "praetorctl state task archive") {
			t.Fatalf("oversize error lacks archive remedy: %v", err)
		}
		return
	}
	if err != nil || snap == nil {
		t.Fatalf("sync below read limit: snapshot=%v error=%v", snap, err)
	}
	assertOpenSizeWarning(t, warnings.String(), size)
}

func assertOpenSizeWarning(t *testing.T, warning string, size int) {
	t.Helper()
	if size <= 800*1024 {
		if warning != "" {
			t.Fatalf("threshold itself warned: %s", warning)
		}
		return
	}
	for _, want := range []string{"WARN", fmt.Sprint(size), "1048576", "praetorctl state task archive"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning lacks %q: %s", want, warning)
		}
	}
}

func TestOpenLedgerSizeWarningUnusableContext(t *testing.T) {
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(testsupport.DeadlineContext(t))
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if warning, err := OpenLedgerSizeWarning(ctx, root); err == nil || warning != "" {
			t.Fatalf("unusable context: warning=%q error=%v", warning, err)
		}
	}
}
