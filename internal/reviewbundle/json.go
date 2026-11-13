// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// jsonBytes keeps ordinary JSON strings readable; invalid UTF-8 uses {"base64":"..."}.
// The tagged object distinguishes raw bytes from literal strings containing encoding markers.
type jsonBytes string

// MarshalJSON preserves arbitrary Git bytes instead of replacing invalid UTF-8.
func (s jsonBytes) MarshalJSON() ([]byte, error) {
	if utf8.ValidString(string(s)) {
		return json.Marshal(string(s))
	}
	return json.Marshal(struct {
		Base64 []byte `json:"base64"`
	}{[]byte(s)})
}

// UnmarshalJSON accepts readable strings and explicitly tagged base64 bytes.
func (s *jsonBytes) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = jsonBytes(text)
		return nil
	}
	var raw struct {
		Base64 []byte `json:"base64"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("git bytes: %w", err)
	}
	if raw.Base64 == nil {
		return fmt.Errorf("git bytes need a base64 field")
	}
	*s = jsonBytes(raw.Base64)
	return nil
}

// jsonPaths applies the same byte representation to every bounded path inventory.
type jsonPaths []string

// MarshalJSON checks the inventory bound before allocating its byte-preserving view.
func (paths jsonPaths) MarshalJSON() ([]byte, error) {
	if len(paths) > MaxMetadataEntries {
		return nil, fmt.Errorf("JSON path metadata exceeds %d entries", MaxMetadataEntries)
	}
	if paths == nil {
		return []byte("null"), nil
	}
	values := make([]jsonBytes, len(paths))
	for i := 0; i < MaxMetadataEntries && i < len(paths); i++ {
		values[i] = jsonBytes(paths[i])
	}
	return json.Marshal(values)
}

// UnmarshalJSON restores byte-exact paths while refusing oversized decoded inventories.
func (paths *jsonPaths) UnmarshalJSON(data []byte) error {
	var values []jsonBytes
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("JSON paths: %w", err)
	}
	if len(values) > MaxMetadataEntries {
		return fmt.Errorf("JSON path metadata exceeds %d entries", MaxMetadataEntries)
	}
	if values == nil {
		*paths = nil
		return nil
	}
	decoded := make(jsonPaths, len(values))
	for i := 0; i < MaxMetadataEntries && i < len(values); i++ {
		decoded[i] = string(values[i])
	}
	*paths = decoded
	return nil
}

// fileJSON prevents recursive method dispatch while retaining File's metadata schema.
type fileJSON File

// MarshalJSON emits byte-exact file paths and content with the original metadata fields.
func (f File) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		fileJSON
		Path    jsonBytes `json:"path"`
		Content jsonBytes `json:"content"`
	}{fileJSON(f), jsonBytes(f.Path), jsonBytes(f.Content)})
}

// UnmarshalJSON restores both plain UTF-8 and tagged byte fields.
func (f *File) UnmarshalJSON(data []byte) error {
	var wire struct {
		fileJSON
		Path    jsonBytes `json:"path"`
		Content jsonBytes `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("bundle file JSON: %w", err)
	}
	*f = File(wire.fileJSON)
	f.Path, f.Content = string(wire.Path), string(wire.Content)
	return nil
}

// conflictJSON retains the classification schema without Conflict's JSON methods.
type conflictJSON Conflict

// MarshalJSON preserves the exact bytes of a classified conflict path.
func (c Conflict) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		conflictJSON
		Path jsonBytes `json:"path"`
	}{conflictJSON(c), jsonBytes(c.Path)})
}

// UnmarshalJSON restores a classified path without Unicode replacement.
func (c *Conflict) UnmarshalJSON(data []byte) error {
	var wire struct {
		conflictJSON
		Path jsonBytes `json:"path"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("bundle conflict JSON: %w", err)
	}
	*c = Conflict(wire.conflictJSON)
	c.Path = string(wire.Path)
	return nil
}

// bundleJSON retains provenance and file metadata without Bundle's JSON methods.
type bundleJSON Bundle

// MarshalJSON preserves all path inventories, including omitted and conflicted paths.
func (b Bundle) MarshalJSON() ([]byte, error) {
	if err := validateMetadata(b); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		bundleJSON
		Cut             jsonPaths `json:"cut"`
		ConflictedPaths jsonPaths `json:"conflicted_paths"`
		OmittedPaths    jsonPaths `json:"omitted_paths"`
	}{bundleJSON(b), b.Cut, b.ConflictedPaths, b.OmittedPaths})
}

// UnmarshalJSON restores byte-exact path inventories and checks their decoded count.
func (b *Bundle) UnmarshalJSON(data []byte) error {
	var wire struct {
		bundleJSON
		Cut             jsonPaths `json:"cut"`
		ConflictedPaths jsonPaths `json:"conflicted_paths"`
		OmittedPaths    jsonPaths `json:"omitted_paths"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("bundle JSON: %w", err)
	}
	decoded := Bundle(wire.bundleJSON)
	decoded.Cut, decoded.ConflictedPaths, decoded.OmittedPaths = wire.Cut, wire.ConflictedPaths, wire.OmittedPaths
	if err := validateMetadata(decoded); err != nil {
		return err
	}
	*b = decoded
	return nil
}
