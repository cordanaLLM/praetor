// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// fatalRecorder records Fatalf while preserving its goroutine-ending behavior.
type fatalRecorder struct {
	testing.TB
	mu      sync.Mutex
	message string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.message = fmt.Sprintf(format, args...)
	r.mu.Unlock()
	runtime.Goexit()
}

// RecordFatal runs build with a test that records Fatalf instead of failing its parent.
// It returns the diagnostic, or an empty string when build returns without Fatalf.
// Callers must bound build's work, just as they bound other fixture operations.
func RecordFatal(t testing.TB, build func(testing.TB)) string {
	t.Helper()
	recorder := &fatalRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		build(recorder)
	}()
	<-done
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.message
}
