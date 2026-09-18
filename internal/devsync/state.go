package devsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// stateVersion is the schema version of the local push state file.
	stateVersion = 1
	// maxStateBytes bounds the state file read before decoding.
	maxStateBytes = 16 << 20
	// configDirName is praetor's folder below the user config and data directories.
	configDirName = "praetor"
)

// pushState remembers, per remote archive path, the fingerprint of the last upload.
type pushState struct {
	Version  int                    `json:"version"`
	Archives map[string]fingerprint `json:"archives"`
}

// DefaultStatePath is where push records what it uploaded: <user config dir>/praetor/devsync-state.json.
func DefaultStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, configDirName, "devsync-state.json"), nil
}

// DefaultPullDir is where pull unpacks when no target is given: <user data dir>/praetor/devsync.
// The data directory is $XDG_DATA_HOME or ~/.local/share on Linux and other Unix systems,
// %LOCALAPPDATA% on Windows and ~/Library/Application Support on macOS.
func DefaultPullDir() (string, error) {
	dir, err := userDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configDirName, "devsync"), nil
}

func userDataDir() (string, error) {
	switch {
	case runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") != "":
		return os.Getenv("LOCALAPPDATA"), nil
	case runtime.GOOS == "darwin":
		return os.UserConfigDir()
	case os.Getenv("XDG_DATA_HOME") != "":
		return os.Getenv("XDG_DATA_HOME"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.Join(errors.New("resolve user data directory"), err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

// loadState reads the push state; a missing file is an empty state.
func loadState(path string) (*pushState, error) {
	state := &pushState{Version: stateVersion, Archives: map[string]fingerprint{}}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > maxStateBytes {
		return nil, fmt.Errorf("push state %s exceeds %d bytes", path, maxStateBytes)
	}
	data, err := util.ReadFileNoFollow(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("push state %s is unreadable (delete it to upload everything again): %w", path, err)
	}
	if state.Version != stateVersion {
		return nil, fmt.Errorf("push state %s has version %d, want %d (delete it to upload everything again)", path, state.Version, stateVersion)
	}
	if state.Archives == nil {
		state.Archives = map[string]fingerprint{}
	}
	return state, nil
}

// save writes the state owner-only.
func (s *pushState) save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := util.MkdirSecure(filepath.Dir(path), util.SecureDirPerm); err != nil {
		return err
	}
	return util.WriteFileNoFollow(path, data, util.SecureFilePerm)
}
