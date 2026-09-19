package repairrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func providerToken(ctx context.Context, cfg ProviderConfig) (token string, err error) {
	data, err := providerHelperSnapshot(cfg)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "praetor-credential-helper-")
	if err != nil {
		return "", errors.New("repair credential snapshot creation failed")
	}
	defer func() {
		if os.RemoveAll(dir) != nil {
			token, err = "", errors.New("repair credential snapshot cleanup failed")
		}
	}()
	if err := providerWriteHelper(dir, data); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, err = util.WithCommandEnvironment(ctx, []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"})
	if err != nil {
		return "", errors.New("repair credential helper environment failed")
	}
	result, runErr := util.RunCommandBytes(ctx, dir, filepath.Join(dir, "helper"), 4096)
	if runErr != nil || len(result.Stderr) != 0 {
		return "", errors.New("repair credential helper failed")
	}
	token = strings.TrimSuffix(string(result.Stdout), "\n")
	if !providerValidToken(token) {
		return "", errors.New("repair credential helper returned an invalid credential")
	}
	return token, nil
}

func providerWriteHelper(dir string, data []byte) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("repair credential snapshot directory open failed")
	}
	defer func() {
		if root.Close() != nil {
			err = errors.New("repair credential snapshot directory close failed")
		}
	}()
	file, err := root.OpenFile("helper", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.New("repair credential snapshot file creation failed")
	}
	_, writeErr := file.Write(data)
	if closeErr := file.Close(); writeErr != nil || closeErr != nil {
		return errors.New("repair credential snapshot write failed")
	}
	return nil
}

func providerHelperSnapshot(cfg ProviderConfig) (data []byte, err error) {
	if err := providerHelperParents(cfg.TokenCommand); err != nil {
		return nil, err
	}
	file, err := providerOpenHelper(cfg.TokenCommand)
	if err != nil {
		return nil, errors.New("repair credential helper open failed")
	}
	defer func() {
		if file.Close() != nil {
			data, err = nil, errors.New("repair credential helper close failed")
		}
	}()
	info, err := file.Stat()
	if err != nil || !providerHelperInfo(info) {
		return nil, errors.New("repair credential helper must be a private owned regular executable")
	}
	data, err = io.ReadAll(io.LimitReader(file, providerPromptLimit+1))
	if err != nil || len(data) != int(info.Size()) {
		return nil, errors.New("repair credential helper read failed or changed")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != cfg.TokenCommandSHA256 {
		return nil, errors.New("repair credential helper digest changed")
	}
	return data, nil
}

// providerHelperParents checks the helper executable itself and its immediate parent
// directory: nothing else in the path is this call's threat surface.
//
// The previous version walked every ancestor up to the filesystem root and rejected the
// first symlink it found. On macOS that ancestor is always present and always benign --
// /var is a symlink to /private/var, so any TokenCommand under the platform's own TMPDIR,
// including every t.TempDir() fixture in this package's own tests, failed here before the
// helper was ever read. That took down the whole repairrun package on the macOS leg of the
// portability matrix (#135), the same shape #109 already fixed once for directory creation:
// the operator's filesystem above where praetor's own writes and reads happen is not
// praetor's threat surface, and an attacker who controls /var does not need a symlink to
// defeat this check.
//
// What the check still refuses, because both are this function's actual job: the helper
// itself resolving through a symlink (an attacker substituting what gets executed), and its
// immediate parent being a symlink (an attacker redirecting where "the helper's directory"
// actually is). Both are exercised in TestProviderHelperRejectsChangedOrNonPrivateSource.
func providerHelperParents(path string) error {
	leaf, err := os.Lstat(path)
	if err != nil || leaf.Mode()&os.ModeSymlink != 0 {
		return errors.New("repair credential helper path contains an unavailable or symlink component")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("repair credential helper path contains an unavailable or symlink component")
	}
	if !parent.IsDir() {
		return errors.New("repair credential helper parent is not a directory")
	}
	return nil
}

func providerValidToken(token string) bool {
	if !strings.HasPrefix(token, "sk-") || len(token) < 4 || len(token) > 4095 {
		return false
	}
	for i := 0; i < len(token) && i < 4096; i++ {
		ch := token[i]
		if !providerTokenCharacter(ch) {
			return false
		}
	}
	return true
}

func providerTokenCharacter(ch byte) bool {
	return ch == '-' || ch == '_' || ch == '.' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}
