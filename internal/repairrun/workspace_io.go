package repairrun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func openDirectory(path string) (*os.Root, error) {
	if !cleanAbsolute(path) {
		return nil, errors.New("directory must be clean and absolute")
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, openErr := openChild(root, part)
		closeErr := root.Close()
		if err := errors.Join(openErr, closeErr); err != nil {
			if next != nil {
				return nil, errors.Join(err, next.Close())
			}
			return nil, err
		}
		root = next
	}
	return root, nil
}

func openChild(root *os.Root, path string) (*os.Root, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("directory must not be a link or special file")
	}
	child, err := root.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := child.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.Join(errors.New("directory changed while opening"), err, child.Close())
	}
	return child, nil
}

func readPrivate(ctx context.Context, path string, limit int64) ([]byte, error) {
	root, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	data, readErr := readFile(ctx, root, filepath.Base(path), limit, true)
	return data, errors.Join(readErr, root.Close())
}

func readFile(ctx context.Context, root *os.Root, name string, limit int64, private bool) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !readableFile(before, limit, private) {
		return nil, errors.New("input must be a bounded regular file with required privacy")
	}
	file, err := openRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return nil, errors.New("input changed while opening")
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !stableFile(before, after, int64(len(data)), limit) {
		return nil, errors.New("input changed during bounded read")
	}
	return data, ctx.Err()
}

func readableFile(info os.FileInfo, limit int64, private bool) bool {
	return info.Mode().IsRegular() && info.Size() <= limit && (!private || info.Mode().Perm()&0o077 == 0)
}

func stableFile(before, after os.FileInfo, size, limit int64) bool {
	return os.SameFile(before, after) && before.Size() == size && after.Size() == size && before.ModTime().Equal(after.ModTime()) && size <= limit
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("repair metadata exceeded 1 MiB")
	}
	return writeNew(root, name, append(data, '\n'))
}

func writeNew(root *os.Root, name string, data []byte) (err error) {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
