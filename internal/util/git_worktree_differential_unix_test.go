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
			// git before 2.56 reports a FIFO .git as a discovery failure and walks on;
			// 2.56 dies with "not a regular file", which is the behavior mirrored here.
			if !gitVersionAtLeast(t, 2, 56) {
				t.Skip("git before 2.56 skips a FIFO .git entry; the mirrored behavior is 2.56")
			}
			if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o600); err != nil {
				t.Skipf("mkfifo unavailable: %v", err)
			}
		})},
	}
}
