// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package secrets_test

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGate_EventPipeRejectsPromptly ensures a named pipe cannot block event processing.
func TestGate_EventPipeRejectsPromptly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	start := time.Now()
	out, code := runGateCLIEnvironment(t, t.TempDir(), "", []string{"GITHUB_EVENT_NAME=pull_request", "GITHUB_EVENT_PATH=" + path})
	if code != 2 || !strings.Contains(out, "GITHUB_EVENT_PATH") || time.Since(start) > 30*time.Second {
		t.Fatalf("pipe: exit %d: %s", code, out)
	}
}
