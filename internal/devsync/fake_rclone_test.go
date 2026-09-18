package devsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The test binary doubles as a fake rclone: with fakeStoreEnv set it serves the subset of
// rclone commands devsync uses against a plain directory, so every platform runs the push,
// pull and list logic without rclone installed and without touching any real remote.
const (
	fakeStoreEnv = "PRAETOR_DEVSYNC_FAKE_STORE"
	fakeFailEnv  = "PRAETOR_DEVSYNC_FAKE_FAIL"
)

func TestMain(m *testing.M) {
	if store := os.Getenv(fakeStoreEnv); store != "" {
		os.Exit(fakeRclone(store, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// newFakeRclone returns a context whose subprocesses run as the fake, a Rclone that executes
// it, and the fake's store. failVerb makes that one rclone command fail.
func newFakeRclone(t *testing.T, failVerb string) (context.Context, Rclone, string) {
	t.Helper()
	store := t.TempDir()
	ctx, rclone := fakeRcloneAt(t, store, failVerb)
	return ctx, rclone, store
}

// fakeRcloneAt serves an existing store, so a failing fake and a working one share a remote.
func fakeRcloneAt(t *testing.T, store, failVerb string) (context.Context, Rclone) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The race runtime sleeps a second at exit by default; the fake runs once per rclone call.
	env := []string{fakeStoreEnv + "=" + store, fakeFailEnv + "=" + failVerb, "GOCOVERDIR=" + t.TempDir(), "GORACE=atexit_sleep_ms=0"}
	ctx, err := util.WithCommandEnvironment(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, Rclone{Binary: binary, Config: filepath.Join(store, "rclone.conf")}
}

func fakeRclone(store string, args []string) int {
	if len(args) >= 2 && args[0] == "--config" {
		args = args[2:]
	}
	if len(args) == 0 {
		return 2
	}
	if args[0] == os.Getenv(fakeFailEnv) {
		fmt.Fprintln(os.Stderr, "fake failure")
		return 1
	}
	if err := fakeCommand(store, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var code fakeExit
		if errors.As(err, &code) {
			return int(code)
		}
		return 1
	}
	return 0
}

type fakeExit int

func (e fakeExit) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func fakePath(store, arg string) string {
	_, rest, found := strings.Cut(arg, ":")
	if !found {
		rest = arg
	}
	return filepath.Join(store, "data", filepath.FromSlash(rest))
}

func fakeCommand(store string, args []string) error {
	last := args[len(args)-1]
	switch args[0] {
	case "listremotes":
		return fakeListRemotes(store)
	case "config":
		return fakeConfigCreate(store, args)
	case "rcat":
		return fakeWrite(fakePath(store, last), os.Stdin)
	case "moveto":
		return os.Rename(fakePath(store, args[1]), fakePath(store, args[2]))
	case "deletefile":
		if err := os.Remove(fakePath(store, last)); errors.Is(err, fs.ErrNotExist) {
			return fakeExit(rcloneFileNotFound)
		}
		return nil
	case "cat":
		return fakeCat(fakePath(store, last))
	case "lsjson":
		return fakeList(fakePath(store, last))
	}
	return fmt.Errorf("fake rclone: unsupported command %q", args[0])
}

func fakeListRemotes(store string) error {
	data, err := os.ReadFile(filepath.Join(store, "remotes.json"))
	if errors.Is(err, fs.ErrNotExist) {
		data = []byte("[]")
	} else if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func fakeConfigCreate(store string, args []string) error {
	if err := os.WriteFile(filepath.Join(store, "config-create.json"), mustJSON(args), 0o600); err != nil {
		return err
	}
	remotes := mustJSON([]map[string]string{{"name": args[2]}})
	if err := os.WriteFile(filepath.Join(store, "remotes.json"), remotes, 0o600); err != nil {
		return err
	}
	_, err := fmt.Fprint(os.Stdout, `{"State":"","Option":null,"Error":"","Result":""}`)
	return err
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte("null")
	}
	return data
}

func fakeWrite(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, r)
	return errors.Join(err, file.Close())
}

func fakeCat(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fakeExit(rcloneDirNotFound)
	}
	if err != nil {
		return err
	}
	_, err = io.Copy(os.Stdout, file)
	return errors.Join(err, file.Close())
}

func fakeList(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return fakeExit(rcloneDirNotFound)
	}
	type entry struct {
		Path    string
		Size    int64
		ModTime string
	}
	entries := []entry{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		entries = append(entries, entry{filepath.ToSlash(rel), info.Size(), info.ModTime().Format("2006-01-02T15:04:05Z07:00")})
		return err
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(mustJSON(entries))
	return err
}
