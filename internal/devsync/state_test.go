package devsync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	state, err := loadState(path)
	if err != nil || len(state.Archives) != 0 {
		t.Fatalf("missing state = %+v, %v", state, err)
	}
	state.Archives["r:h/dev/a.tar.gz"] = fingerprint{Files: 2, NewestMtime: 7, Bytes: 9}
	if err := state.save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path)
	if err != nil || loaded.Archives["r:h/dev/a.tar.gz"] != (fingerprint{Files: 2, NewestMtime: 7, Bytes: 9}) {
		t.Fatalf("reloaded state = %+v, %v", loaded, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode = %v, %v", info.Mode(), err)
		}
	}
}

func TestStateRejectsDamage(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"corrupt": "{", "version": `{"version": 9, "archives": {}}`} {
		path := filepath.Join(dir, name+".json")
		writeTestFile(t, path, content)
		if _, err := loadState(path); err == nil || !strings.Contains(err.Error(), "delete it") {
			t.Errorf("%s state accepted or unexplained: %v", name, err)
		}
	}
	empty := filepath.Join(dir, "empty-archives.json")
	writeTestFile(t, empty, `{"version": 1}`)
	if state, err := loadState(empty); err != nil || state.Archives == nil {
		t.Fatalf("state without archives = %+v, %v", state, err)
	}
}

func TestStateRefusesLink(t *testing.T) {
	requireSymlinks(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	writeTestFile(t, real, `{"version": 1, "archives": {}}`)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(link); err == nil {
		t.Fatal("state read through a symbolic link")
	}
	if err := (&pushState{Version: stateVersion}).save(link); err == nil {
		t.Fatal("state written through a symbolic link")
	}
}

func TestDefaultPaths(t *testing.T) {
	statePath, err := DefaultStatePath()
	if err != nil || filepath.Base(statePath) != "devsync-state.json" || filepath.Base(filepath.Dir(statePath)) != configDirName {
		t.Fatalf("DefaultStatePath = %q, %v", statePath, err)
	}
	pullDir, err := DefaultPullDir()
	if err != nil || !strings.HasSuffix(pullDir, filepath.Join(configDirName, "devsync")) {
		t.Fatalf("DefaultPullDir = %q, %v", pullDir, err)
	}
}

func TestDefaultPullDirHonoursXDG(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG_DATA_HOME applies to Linux and other Unix systems only")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	if dir, err := DefaultPullDir(); err != nil || dir != filepath.Join(data, configDirName, "devsync") {
		t.Fatalf("DefaultPullDir = %q, %v", dir, err)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	if _, err := DefaultPullDir(); err == nil {
		t.Fatal("unresolvable home accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := DefaultStatePath(); err == nil {
		t.Fatal("unresolvable config directory accepted")
	}
}
