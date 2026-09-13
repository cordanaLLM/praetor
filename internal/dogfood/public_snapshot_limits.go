package dogfood

import "fmt"

// SnapshotLimits bounds one public snapshot. Nil limits select historical defaults.
type SnapshotLimits struct {
	MaxEntries   int   `json:"max_entries"`
	MaxFileBytes int64 `json:"max_file_bytes"`
	MaxTreeBytes int64 `json:"max_tree_bytes"`
}

// SnapshotReport describes the input admitted before completion or failure.
type SnapshotReport struct {
	Status          string `json:"status"`
	EntriesObserved int    `json:"entries_observed"`
	FilesObserved   int    `json:"files_observed"`
	BytesObserved   int64  `json:"bytes_observed"`
	OffendingPath   string `json:"offending_path,omitempty"`
	Limit           string `json:"limit,omitempty"`
}

func NormalizeSnapshotLimits(input *SnapshotLimits) (SnapshotLimits, error) {
	if input == nil {
		return SnapshotLimits{MaxEntries: maxPublicTreeEntries, MaxFileBytes: maxPublicFileBytes, MaxTreeBytes: maxPublicTreeBytes}, nil
	}
	if input.MaxEntries <= 0 || input.MaxFileBytes <= 0 || input.MaxTreeBytes <= 0 {
		return SnapshotLimits{}, fmt.Errorf("snapshot limits must be positive")
	}
	if input.MaxEntries > maxPublicTreeEntries {
		return SnapshotLimits{}, fmt.Errorf("snapshot max entries exceeds %d", maxPublicTreeEntries)
	}
	if input.MaxFileBytes > 256<<20 || input.MaxTreeBytes > 1<<30 {
		return SnapshotLimits{}, fmt.Errorf("snapshot byte limits exceed configured ceiling")
	}
	if input.MaxFileBytes > input.MaxTreeBytes {
		return SnapshotLimits{}, fmt.Errorf("snapshot max file bytes exceeds max tree bytes")
	}
	return *input, nil
}
