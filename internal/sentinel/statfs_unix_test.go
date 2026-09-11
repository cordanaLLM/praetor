//go:build !windows

package sentinel

import (
	"syscall"
	"testing"
)

func TestMockStatfs_Boundary(t *testing.T) {
	oldStatfs := statfsFunc
	defer func() { statfsFunc = oldStatfs }()

	statfsFunc = func(path string, buf *syscall.Statfs_t) error {
		buf.Bsize = 4096
		buf.Blocks = 1000000
		buf.Bavail = 500000
		return nil
	}

	total, free, err := readHostDisk("dummy")
	if err != nil {
		t.Fatalf("unexpected readHostDisk error: %v", err)
	}
	if total != 1000000*4096 {
		t.Errorf("expected total %d, got %d", 1000000*4096, total)
	}
	if free != 500000*4096 {
		t.Errorf("expected free %d, got %d", 500000*4096, free)
	}
}
