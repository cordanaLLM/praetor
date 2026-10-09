package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// recordRel is the record's path below the root.
func recordRel() string {
	return filepath.Join(filepath.FromSlash(OffloadDir), offloadRecordName)
}

// readRecord returns the digests the server recorded as written, oldest first; a missing
// record is empty. Lines that are not digests are dropped: a hand-edited record can only
// lose entries that way, never gain one.
func (o Offloader) readRecord() ([]string, error) {
	data, err := util.ReadConfinedLimited(o.Root, recordRel(), offloadRecordMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: offload record", ErrOffloadUnreadable)
	}
	lines := strings.Split(string(data), "\n")
	digests := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if offloadDigestPattern.MatchString(lines[i]) {
			digests = append(digests, lines[i])
		}
	}
	return digests, nil
}

// recordWritten adds digest to the record of files this server wrote, keeping the newest
// offloadRecordKeep*maxFiles entries (HISS-02). Two servers on one root can lose an entry to
// a race; the loser's file is then refused on read-back until the tool reruns and stores it
// again, which fails closed.
func (o Offloader) recordWritten(digest string) error {
	digests, err := o.readRecord()
	if err != nil {
		return err
	}
	if slices.Contains(digests, digest) {
		return nil
	}
	digests = append(digests, digest)
	if keep := offloadRecordKeep * o.maxFiles(); len(digests) > keep {
		digests = digests[len(digests)-keep:]
	}
	full, err := util.ConfinePath(o.Root, recordRel())
	if err != nil {
		return fmt.Errorf("mcp: resolve offload record: %w", err)
	}
	if err := util.WriteFileAtomic(full, []byte(strings.Join(digests, "\n")+"\n"), util.SecureFilePerm); err != nil {
		return fmt.Errorf("mcp: write offload record: %w", err)
	}
	return nil
}

// wasWritten reports whether the record lists digest.
func (o Offloader) wasWritten(digest string) (bool, error) {
	digests, err := o.readRecord()
	if err != nil {
		return false, err
	}
	return slices.Contains(digests, digest), nil
}
