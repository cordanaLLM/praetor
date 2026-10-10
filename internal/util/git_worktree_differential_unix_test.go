//go:build unix

package util

import (
	"path/filepath"
	"syscall"
	"testing"
)

func platformDifferentialFixtures() []gitFixture {
	return []gitFixture{
		{"fifo", here(func(t *testing.T, dir string) {
			if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o600); err != nil {
				t.Skipf("mkfifo unavailable: %v", err)
			}
		})},
	}
}
