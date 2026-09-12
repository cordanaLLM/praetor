package harvester

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type cancelOnBundleWrite struct{ cancel context.CancelFunc }

func (w cancelOnBundleWrite) Write(data []byte) (int, error) { w.cancel(); return len(data), nil }

func TestBundleStreamCancellationDuringCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), MaxCopyBuffer*3), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	written, err := copyFileStream(ctx, cancelOnBundleWrite{cancel: cancel}, file)
	if !errors.Is(err, context.Canceled) || written != MaxCopyBuffer {
		t.Fatalf("copy failed to stop after cancellation: bytes=%d err=%v", written, err)
	}
}

func TestBundleStreamExactAndEmptyFiles(t *testing.T) {
	for _, size := range []int{0, MaxCopyBuffer, MaxCopyBuffer + 1} {
		path := filepath.Join(t.TempDir(), "source")
		data := bytes.Repeat([]byte("x"), size)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var copied bytes.Buffer
		written, copyErr := copyFileStream(context.Background(), &copied, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != int64(size) || !bytes.Equal(data, copied.Bytes()) {
			t.Fatalf("size %d: bytes=%d copy=%v close=%v", size, written, copyErr, closeErr)
		}
	}
}

func TestBundleStreamMetadataFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if written, err := copyFileStream(context.Background(), io.Discard, file); err == nil || written != 0 {
		t.Fatalf("closed input accepted: %d %v", written, err)
	}
}
