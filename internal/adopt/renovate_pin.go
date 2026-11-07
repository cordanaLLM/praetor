// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

const renovatePinDescription = "praetor workflow engine commit pin: track the engine default branch"

// renderRenovatePinManager constructs the Renovate regex customManager configuration for workflow engine commit pins.
func renderRenovatePinManager() (jsontext.Value, error) {
	// Keep this dependency name outside the engine module/repository namespaces disabled by
	// renovateEngineNames. Updating a workflow pin is independent of managed asset updates.
	manager := map[string]any{
		"customType": "regex", "description": renovatePinDescription,
		"managerFilePatterns": []string{`/^\.github/workflows/[^/]+\.ya?ml$/`},
		"matchStrings": []string{
			fmt.Sprintf(workflowPinPattern, "PRAETOR_COMMIT", `[0-9a-f]{40}`),
			fmt.Sprintf(workflowPinPattern, "PRAETOR_REF", `[0-9a-fA-F]{7,40}`),
		},
		"depNameTemplate":     "praetor-workflow-engine-pin",
		"packageNameTemplate": "https://" + gomanifest.EngineModulePath,
		"datasourceTemplate":  "git-refs", "currentValueTemplate": "HEAD",
	}
	raw, err := json.Marshal(manager, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("render workflow pin Renovate manager: %w", err)
	}
	return raw, nil
}

// mergeRenovatePinManager ensures Renovate configuration declares the custom regex manager for workflow engine pins.
func mergeRenovatePinManager(data []byte) ([]byte, bool, error) {
	text, crlf, err := normalizeRenovateDocument(data)
	if err != nil {
		return nil, false, err
	}
	root, err := clientjson.DecodeObject([]byte(text))
	if err != nil {
		return nil, false, renovateUnsafe("pin manager requires an object: %v", err)
	}
	managers, err := readRenovatePinManagers(root)
	if err != nil {
		return nil, false, err
	}
	found, err := findRenovateRule(managers, renovatePinDescription, "customManagers")
	if err != nil {
		return nil, false, err
	}
	manager, err := renderRenovatePinManager()
	if err != nil {
		return nil, false, err
	}
	if found >= 0 && sameJSON(managers[found], manager) {
		return data, false, nil
	}
	managers, err = withRenovatePinManager(managers, found, manager)
	if err != nil {
		return nil, false, err
	}
	encoded, err := encodeRenovatePinManagers(root, managers)
	if err != nil {
		return nil, false, err
	}
	return []byte(util.RestoreLineEndings(string(encoded), crlf)), true, nil
}

// withRenovatePinManager updates an existing customManager or appends a new workflow pin manager entry.
func withRenovatePinManager(managers []jsontext.Value, found int, manager jsontext.Value) ([]jsontext.Value, error) {
	if found >= 0 {
		managers[found] = manager
		return managers, nil
	}
	if len(managers) >= maxRenovateRules {
		return nil, renovateUnsafe("workflow pin manager exceeds %d customManagers", maxRenovateRules)
	}
	return append(managers, manager), nil
}

// readRenovatePinManagers extracts and validates the customManagers array from decoded Renovate JSON.
func readRenovatePinManagers(root clientjson.Object) ([]jsontext.Value, error) {
	managers := []jsontext.Value{}
	if raw, exists := root.Get("customManagers"); exists {
		if raw.Kind() != '[' || json.Unmarshal(raw, &managers) != nil || len(managers) > maxRenovateRules {
			return nil, renovateUnsafe("customManagers must be an array with at most %d entries", maxRenovateRules)
		}
	}
	return managers, nil
}

// encodeRenovatePinManagers serializes the customManagers array back into the root Renovate configuration object.
func encodeRenovatePinManagers(root clientjson.Object, managers []jsontext.Value) ([]byte, error) {
	raw, err := json.Marshal(managers)
	if err != nil {
		return nil, fmt.Errorf("encode workflow pin managers: %w", err)
	}
	encoded, err := root.With("customManagers", raw).Encode()
	if err != nil {
		return nil, fmt.Errorf("encode workflow pin configuration: %w", err)
	}
	return encoded, nil
}

// renovatePinReport returns an informational summary fragment if the workflow engine pin customManager is present.
func renovatePinReport(data []byte) string {
	if strings.Contains(string(data), renovatePinDescription) {
		return "; workflow engine SHA pins updated by a custom regex manager (git-refs, default branch HEAD)"
	}
	return ""
}
