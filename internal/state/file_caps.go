package state

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Live ledger limits are inclusive. Legacy excess is ratcheted, never raised.
const (
	OpenStateEntryCap    = 150
	OpenStateByteCap     = 64 << 10
	BacklogStateEntryCap = 50
	stateFileCapsName    = "state-file-caps.json"
	stateFileCapsVersion = 1
)

// stateFileBaseline is private state, persisted only by explicit mutating commands.
type stateFileBaseline struct {
	Version int                           `json:"version"`
	Files   map[string]LiveStateFileUsage `json:"files"`
}

// CheckStateFileCaps checks current usage against the recorded minimum. record creates or
// tightens the baseline; read-only callers pass false. Usage is returned even on cap failure.
func CheckStateFileCaps(ctx context.Context, rootPath string, record bool) ([]LiveStateFileUsage, error) {
	usage, err := MeasureLiveStateFiles(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, stateFileCapsName)
	baseline, before, exists, err := readStateFileBaseline(ctx, path)
	if err != nil {
		return usage, fmt.Errorf("read %s: %w", stateFileCapsName, err)
	}
	var failures []error
	for i := 0; i < len(usage) && i < liveStateFileCount; i++ {
		current := usage[i]
		if !current.Present {
			continue
		}
		previous, recorded := baseline.Files[current.Name]
		if !recorded {
			previous = current
		}
		failures = append(failures, checkLiveStateFile(current, previous))
		baseline.Files[current.Name] = shrinkStateFileBaseline(current, previous)
	}
	if err := errors.Join(failures...); err != nil {
		return usage, err
	}
	if record && len(baseline.Files) > 0 {
		return usage, writeStateFileBaseline(ctx, path, baseline, before, exists)
	}
	return usage, nil
}

// readStateFileBaseline refuses corrupt, incomplete, duplicate-key or unknown metadata.
func readStateFileBaseline(ctx context.Context, path string) (stateFileBaseline, []byte, bool, error) {
	baseline := stateFileBaseline{Version: stateFileCapsVersion, Files: map[string]LiveStateFileUsage{}}
	before, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil || !exists {
		return baseline, before, exists, err
	}
	baseline = stateFileBaseline{}
	if err := json.Unmarshal(before, &baseline, json.RejectUnknownMembers(true)); err != nil {
		return baseline, before, exists, fmt.Errorf("decode %s: %w", stateFileCapsName, err)
	}
	if baseline.Version != stateFileCapsVersion || len(baseline.Files) == 0 || len(baseline.Files) > liveStateFileCount {
		return baseline, before, exists, fmt.Errorf("%s requires version %d and 1 to %d live files", stateFileCapsName, stateFileCapsVersion, liveStateFileCount)
	}
	for name, value := range baseline.Files {
		if !isLiveStateFileName(name) || value.Name != name || !validStateFileBaseline(value) {
			return baseline, before, exists, fmt.Errorf("%s: invalid baseline for %s", stateFileCapsName, name)
		}
	}
	return baseline, before, exists, nil
}

// isLiveStateFileName checks whether name matches one of the pinned live state files.
func isLiveStateFileName(name string) bool {
	for _, liveName := range liveStateFileNames() {
		if name == liveName {
			return true
		}
	}
	return false
}

// validStateFileBaseline constrains metadata to the same scalar bounds as its source.
func validStateFileBaseline(value LiveStateFileUsage) bool {
	return value.Open >= 0 && value.Open <= maxScannedLines &&
		value.Completed >= 0 && value.Completed <= maxScannedLines &&
		value.Bytes >= 0 && value.Bytes <= contextopt.MaxSourceBytes
}

// shrinkStateFileBaseline retains independent minima, even when one dimension is unchanged.
func shrinkStateFileBaseline(current, previous LiveStateFileUsage) LiveStateFileUsage {
	return LiveStateFileUsage{Name: current.Name, Open: min(current.Open, previous.Open),
		Completed: min(current.Completed, previous.Completed), Bytes: min(current.Bytes, previous.Bytes)}
}

// checkLiveStateFile names every broken metric and the live file responsible.
func checkLiveStateFile(current, previous LiveStateFileUsage) error {
	var failures []error
	if current.Open > previous.Open {
		failures = append(failures, fmt.Errorf("%s: open entries %d exceed ratchet baseline %d", current.Name, current.Open, previous.Open))
	}
	if current.Bytes > previous.Bytes {
		failures = append(failures, fmt.Errorf("%s: bytes %d exceed ratchet baseline %d", current.Name, current.Bytes, previous.Bytes))
	}
	failures = append(failures, checkStateFileHardCaps(current, previous))
	return errors.Join(failures...)
}

// checkStateFileHardCaps selects the hard limits that each ledger has already reached.
func checkStateFileHardCaps(current, previous LiveStateFileUsage) error {
	switch current.Name {
	case openLedgerName:
		return checkOpenStateFile(current, previous)
	case backlogLedgerName:
		if previous.Open <= BacklogStateEntryCap && current.Open > BacklogStateEntryCap {
			return fmt.Errorf("%s: open entries %d exceed cap %d", current.Name, current.Open, BacklogStateEntryCap)
		}
	case bugLedgerName:
		if previous.Completed == 0 && current.Completed > 0 {
			return fmt.Errorf("%s: %d resolved rows remain live; run state task archive --apply", current.Name, current.Completed)
		}
	}
	return nil
}

// checkOpenStateFile activates each hard limit after that metric reaches it.
func checkOpenStateFile(current, previous LiveStateFileUsage) error {
	var failures []error
	if previous.Open <= OpenStateEntryCap && current.Open > OpenStateEntryCap {
		failures = append(failures, fmt.Errorf("%s: open entries %d exceed cap %d", current.Name, current.Open, OpenStateEntryCap))
	}
	if previous.Bytes <= OpenStateByteCap && current.Bytes > OpenStateByteCap {
		failures = append(failures, fmt.Errorf("%s: bytes %d exceed cap %d", current.Name, current.Bytes, OpenStateByteCap))
	}
	if previous.Open <= OpenStateEntryCap && previous.Bytes <= OpenStateByteCap && current.Completed > 0 {
		failures = append(failures, fmt.Errorf("%s: %d completed rows remain live; run state task archive --apply", current.Name, current.Completed))
	}
	return errors.Join(failures...)
}

// writeStateFileBaseline publishes deterministic metadata only when its bytes change.
func writeStateFileBaseline(ctx context.Context, path string, baseline stateFileBaseline, before []byte, exists bool) error {
	updated, err := json.Marshal(baseline, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode %s: %w", stateFileCapsName, err)
	}
	updated = append(updated, '\n')
	if bytes.Equal(updated, before) {
		return nil
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o600}
	if err := contextopt.ReplaceSnapshot(ctx, path, updated, options); err != nil {
		return fmt.Errorf("record %s: %w", stateFileCapsName, err)
	}
	return nil
}
