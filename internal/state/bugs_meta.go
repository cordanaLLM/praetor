package state

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// bugMetaName is the sidecar that holds each bug's Context, CreatedAt and
// ResolvedAt, keyed by bug ID. BUGS.md rows keep only a short v2 reference, so
// the file an agent reads carries no Base64 metadata.
const (
	bugMetaName        = "bugs.meta.json"
	bugMetaPendingName = "bugs.meta.json.pending"
	bugMetaVersion     = 1
)

// bugMetaIndex maps a canonical bug ID to its metadata.
type bugMetaIndex map[string]bugMetadata

type bugMetaFile struct {
	Version int          `json:"version"`
	Bugs    bugMetaIndex `json:"bugs"`
}

// decodeBugMeta reads the sidecar strictly: the version, every ID and every
// metadata record must be valid, and the same bounds as the ledger apply.
func decodeBugMeta(data []byte) (bugMetaIndex, error) {
	if len(data) > maxBugLedgerBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", bugMetaName, maxBugLedgerBytes)
	}
	var wire *struct {
		Version *int                      `json:"version"`
		Bugs    map[string]jsontext.Value `json:"bugs"`
	}
	if err := json.Unmarshal(data, &wire, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", bugMetaName, err)
	}
	if wire == nil || wire.Version == nil || *wire.Version != bugMetaVersion || wire.Bugs == nil {
		return nil, fmt.Errorf("%s requires version %d and a bugs object", bugMetaName, bugMetaVersion)
	}
	if len(wire.Bugs) > maxBugEntries {
		return nil, fmt.Errorf("%s exceeds %d records", bugMetaName, maxBugEntries)
	}
	index := make(bugMetaIndex, len(wire.Bugs))
	for id, raw := range wire.Bugs {
		metadata, err := decodeBugMetaRecord(id, raw)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", bugMetaName, id, err)
		}
		index[id] = *metadata
	}
	return index, nil
}

func decodeBugMetaRecord(id string, raw jsontext.Value) (*bugMetadata, error) {
	if _, err := bugNumber(id); err != nil {
		return nil, err
	}
	metadata, err := decodeBugMetadataJSON(raw)
	if err != nil {
		return nil, err
	}
	return metadata, validateBugText(metadata.Context)
}

// encodeBugMeta writes the sidecar deterministically: IDs sorted, one line.
func encodeBugMeta(index bugMetaIndex) ([]byte, error) {
	if len(index) > maxBugEntries {
		return nil, fmt.Errorf("%s exceeds %d records", bugMetaName, maxBugEntries)
	}
	data, err := json.Marshal(bugMetaFile{Version: bugMetaVersion, Bugs: index}, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", bugMetaName, err)
	}
	data = append(data, '\n')
	if len(data) > maxBugLedgerBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", bugMetaName, maxBugLedgerBytes)
	}
	return data, nil
}
