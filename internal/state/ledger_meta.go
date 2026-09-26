package state

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// sidecarVersion is the envelope version shared by every ledger metadata sidecar:
// {"version":1,"<member>":{"<ID>":{context, created_at, resolved_at}}}.
const sidecarVersion = 1

// ledgerMetaIndex maps a canonical record ID to its metadata.
type ledgerMetaIndex map[string]ledgerMetadata

// sidecarSpec names one ledger's metadata sidecar: the file, the member holding its
// records, and the ID rule every key must satisfy.
type sidecarSpec struct {
	name, member string
	checkID      func(string) error
}

// decodeSidecar reads a sidecar strictly: the envelope holds exactly the version and the
// records member, every ID and record must be valid, and the ledger bounds apply.
func decodeSidecar(spec sidecarSpec, data []byte) (ledgerMetaIndex, error) {
	if len(data) > maxLedgerBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", spec.name, maxLedgerBytes)
	}
	var wire map[string]jsontext.Value
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", spec.name, err)
	}
	records, err := sidecarRecords(spec, wire)
	if err != nil {
		return nil, err
	}
	index := make(ledgerMetaIndex, len(records))
	for id, raw := range records {
		metadata, err := decodeSidecarRecord(spec, id, raw)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", spec.name, id, err)
		}
		index[id] = *metadata
	}
	return index, nil
}

// sidecarRecords checks the envelope and returns the undecoded records. JSON duplicate
// member names are already rejected by the decoder.
func sidecarRecords(spec sidecarSpec, wire map[string]jsontext.Value) (map[string]jsontext.Value, error) {
	shape := fmt.Errorf("%s requires version %d and a %s object, and nothing else", spec.name, sidecarVersion, spec.member)
	if len(wire) != 2 {
		return nil, shape
	}
	var version *int
	if err := json.Unmarshal(wire["version"], &version); err != nil || version == nil || *version != sidecarVersion {
		return nil, shape
	}
	var records map[string]jsontext.Value
	if err := json.Unmarshal(wire[spec.member], &records); err != nil || records == nil {
		return nil, shape
	}
	if len(records) > maxLedgerEntries {
		return nil, fmt.Errorf("%s exceeds %d records", spec.name, maxLedgerEntries)
	}
	return records, nil
}

func decodeSidecarRecord(spec sidecarSpec, id string, raw jsontext.Value) (*ledgerMetadata, error) {
	if err := spec.checkID(id); err != nil {
		return nil, err
	}
	metadata, err := decodeLedgerMetadataJSON(raw)
	if err != nil {
		return nil, err
	}
	return metadata, validateLedgerText(metadata.Context)
}

// encodeSidecar writes a sidecar deterministically: version first, IDs sorted, one line.
func encodeSidecar(spec sidecarSpec, index ledgerMetaIndex) ([]byte, error) {
	if len(index) > maxLedgerEntries {
		return nil, fmt.Errorf("%s exceeds %d records", spec.name, maxLedgerEntries)
	}
	records, err := json.Marshal(index, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", spec.name, err)
	}
	member, err := jsontext.AppendQuote(nil, spec.member)
	if err != nil {
		return nil, fmt.Errorf("encode %s member: %w", spec.name, err)
	}
	data := fmt.Appendf(nil, "{\"version\":%d,%s:%s}\n", sidecarVersion, member, records)
	if len(data) > maxLedgerBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", spec.name, maxLedgerBytes)
	}
	return data, nil
}

// sidecarIndex decodes the sidecar a ledger read observed. An absent sidecar is an empty,
// non-nil index, so writers can always add to it and readers treat every sidecar-form row
// as unresolved.
func sidecarIndex(spec sidecarSpec, files ledgerFiles) (ledgerMetaIndex, error) {
	if !files.metaExists {
		return ledgerMetaIndex{}, nil
	}
	return decodeSidecar(spec, files.meta)
}
