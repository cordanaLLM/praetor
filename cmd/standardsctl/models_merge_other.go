//go:build !unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// openMergeInput uses a Windows root to refuse device paths; other platforms fail explicitly.
func openMergeInput(path string) (file *os.File, resultErr error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("review input reads require Unix or Windows")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open review root: %w", err)
	}
	file, err = root.Open(filepath.Base(path))
	if closeErr := root.Close(); closeErr != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
			file = nil
		}
		err = errors.Join(err, closeErr)
	}
	return file, err
}
