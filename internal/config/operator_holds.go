// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// MaxHookHolds bounds the merged hold list; MaxHoldPrograms bounds each name list.
const (
	MaxHookHolds    = 32
	MaxHoldPrograms = 32
)

func holdsSequence(node *yaml.Node, path string) ([]HoldSettings, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) > MaxHookHolds {
		return nil, fmt.Errorf("%s must be a list of at most %d holds", path, MaxHookHolds)
	}
	holds := make([]HoldSettings, 0, len(node.Content))
	for _, item := range node.Content {
		hold, err := decodeHold(item, path)
		if err != nil {
			return nil, err
		}
		holds = append(holds, hold)
	}
	return holds, nil
}

func decodeHold(node *yaml.Node, path string) (HoldSettings, error) {
	var hold HoldSettings
	if node.Kind != yaml.MappingNode || len(node.Content) > 6 {
		return hold, fmt.Errorf("%s entry must contain file, commands and via", path)
	}
	seen := make(map[string]bool)
	for i := 0; i+1 < len(node.Content) && i < 6; i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if seen[key] {
			return hold, fmt.Errorf("%s entry repeats %s", path, key)
		}
		seen[key] = true
		if err := decodeHoldField(&hold, key, value, path); err != nil {
			return hold, err
		}
	}
	return hold, nil
}

func decodeHoldField(hold *HoldSettings, key string, node *yaml.Node, path string) error {
	var err error
	switch key {
	case "file":
		hold.File, err = stringScalar(node, path+".file")
	case "commands":
		hold.Commands, err = stringSequence(node, path+".commands", MaxHoldPrograms)
	case "via":
		hold.Via, err = stringSequence(node, path+".via", MaxHoldPrograms)
	default:
		err = fmt.Errorf("%s entry: unknown key %q", path, key)
	}
	return err
}

func holdList(_ string, setting OperatorSetting) error {
	return ValidateHookHolds(setting.Holds)
}

// ValidateHookHolds validates decoded and programmatically supplied hold settings.
func ValidateHookHolds(holds []HoldSettings) error {
	if len(holds) > MaxHookHolds {
		return fmt.Errorf("hooks.holds exceeds %d entries", MaxHookHolds)
	}
	for _, hold := range holds {
		if err := validateHold(hold); err != nil {
			return err
		}
	}
	return nil
}

func validateHold(hold HoldSettings) error {
	if hold.File == "" || len(hold.File) > maxSettingValueBytes || !utf8.ValidString(hold.File) || strings.ContainsFunc(hold.File, unicode.IsControl) {
		return fmt.Errorf("hooks.holds.file must be nonempty UTF-8 without controls, at most %d bytes", maxSettingValueBytes)
	}
	if err := holdNames(hold.Commands, "commands"); err != nil {
		return err
	}
	if err := holdNames(hold.Via, "via"); err != nil {
		return err
	}
	return validateHoldLaunchers(hold)
}

func validateHoldLaunchers(hold HoldSettings) error {
	for _, name := range hold.Via {
		if slices.Contains(hold.Commands, name) {
			return fmt.Errorf("hooks.holds: launcher %s also appears in commands", name)
		}
	}
	return nil
}

func holdNames(names []string, field string) error {
	if len(names) == 0 || len(names) > MaxHoldPrograms {
		return fmt.Errorf("hooks.holds.%s requires 1..%d basenames", field, MaxHoldPrograms)
	}
	for _, name := range names {
		if !bareCommand.MatchString(name) {
			return fmt.Errorf("hooks.holds.%s: %q must be a program basename", field, name)
		}
	}
	return nil
}

func sameHold(a, b HoldSettings) bool {
	return a.File == b.File && slices.Equal(a.Commands, b.Commands) && slices.Equal(a.Via, b.Via)
}

func cloneHolds(holds []HoldSettings) []HoldSettings {
	cloned := slices.Clone(holds)
	for i := range cloned {
		cloned[i].Commands = slices.Clone(cloned[i].Commands)
		cloned[i].Via = slices.Clone(cloned[i].Via)
	}
	return cloned
}

func (m *operatorMerge) appendHolds(layer string, spec settingSpec, setting OperatorSetting) error {
	if len(setting.Holds) == 0 {
		return nil
	}
	merged := cloneHolds(m.values[setting.Path].Holds)
	for _, hold := range setting.Holds {
		if !slices.ContainsFunc(merged, func(current HoldSettings) bool { return sameHold(current, hold) }) {
			merged = append(merged, cloneHolds([]HoldSettings{hold})[0])
		}
	}
	if len(merged) > spec.maxItems {
		return fmt.Errorf("merged %s exceeds %d entries", setting.Path, spec.maxItems)
	}
	m.values[setting.Path] = OperatorSetting{Path: setting.Path, Holds: merged}
	m.contribute(setting.Path, layer)
	return nil
}
