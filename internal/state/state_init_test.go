// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// delayedDeadlineContext models a scheduling pause while an operation consults
// its caller's deadline. Fake time makes the pause deterministic and immediate.
type delayedDeadlineContext struct {
	context.Context
	once sync.Once
}

func (c *delayedDeadlineContext) Deadline() (time.Time, bool) {
	c.once.Do(func() { time.Sleep(11 * time.Second) })
	return c.Context.Deadline()
}

func TestInitWorkingDirContext_PreservesCallerBudget(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		slow := &delayedDeadlineContext{Context: ctx}
		if err := InitWorkingDirContext(slow, dir); err != nil {
			t.Fatalf("initialization must retain the caller's minute after an 11s pause: %v", err)
		}
		for _, name := range ledgerFileNames() {
			data, err := os.ReadFile(filepath.Join(dir, WorkingDirName, name))
			if err != nil || len(data) == 0 {
				t.Fatalf("initialized %s = %q, %v", name, data, err)
			}
		}
	})
}
