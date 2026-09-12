//go:build !unix

package router

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func openRoutingInput(path string) (file *os.File, err error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("routing input reads require Unix or Windows")
	}
	// Windows roots reject reserved devices and cannot address Unix FIFOs.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
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
