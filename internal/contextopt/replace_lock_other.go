//go:build !unix

package contextopt

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

const (
	// directoryClaimName is the file whose exclusive creation claims a snapshot directory.
	directoryClaimName = ".praetor-write.lock"
	// maxClaimBytes bounds the read of a claim, which holds one process id.
	maxClaimBytes = 64
)

// tryLockSnapshotDirectory makes one attempt to claim the directory root pins by creating
// directoryClaimName, and reports busy while the name exists. The claim records this
// process's id, so a waiting writer can name the holder. A writer that was interrupted leaves
// its claim behind for review, and LockDirectory then fails after its budget naming that
// process; removing the claim once that process is gone frees the directory.
func tryLockSnapshotDirectory(root *os.Root) (release func() error, busy bool, err error) {
	file, err := root.OpenFile(directoryClaimName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim snapshot directory: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return nil, false, errors.Join(fmt.Errorf("record snapshot directory claim: %w", err), file.Close(), root.Remove(directoryClaimName))
	}
	return func() error { return errors.Join(file.Close(), root.Remove(directoryClaimName)) }, false, nil
}

// directoryLockHolder names the process whose id the directory's claim records, or says why
// it names none.
func directoryLockHolder(root *os.Root) string {
	file, err := root.Open(directoryClaimName)
	if err != nil {
		return "another process (" + err.Error() + ")"
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxClaimBytes))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "another process (" + err.Error() + ")"
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return describeLockHolders(nil, "claim "+directoryClaimName+" records no process id")
	}
	return describeLockHolders([]int{pid}, "") + " (claim " + directoryClaimName + ")"
}
