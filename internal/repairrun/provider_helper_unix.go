//go:build unix

package repairrun

import (
	"os"
	"syscall"
)

func providerOpenHelper(path string) (*os.File, error) {
	// #nosec G304 -- path is an explicit clean absolute helper path; nofollow and
	// nonblock reject symlink/special-file substitution, and bytes are SHA256-pinned.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func providerHelperInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > providerPromptLimit {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(owner.Uid) == int64(os.Geteuid()) && info.Mode().Perm()&0o077 == 0 && info.Mode().Perm()&0o100 != 0
}
