// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
)

// operatorMerge folds the operator settings of each layer, in layer order, into one value per
// concrete path and records which layers contributed it.
type operatorMerge struct {
	values       map[string]OperatorSetting
	contributors map[string][]string
}

func newOperatorMerge() *operatorMerge {
	return &operatorMerge{values: map[string]OperatorSetting{}, contributors: map[string][]string{}}
}

func (m *operatorMerge) apply(layer string, settings []OperatorSetting) error {
	if len(settings) > maxOperatorSettings {
		return fmt.Errorf("%s: operator sections exceed %d settings", layer, maxOperatorSettings)
	}
	for _, setting := range settings {
		spec, err := settingSpecFor(setting.Path)
		if err == nil {
			err = checkSetting(layer, spec, setting)
		}
		if err == nil {
			err = m.merge(layer, spec, setting)
		}
		if err != nil {
			return fmt.Errorf("%s policy: %w", layer, err)
		}
	}
	return nil
}

// checkSetting validates shape, the literal rule, the host-data rule and the key's own check.
// It runs on every layer, including layers built by callers of ResolvePolicy.
func checkSetting(layer string, spec settingSpec, setting OperatorSetting) error {
	if err := checkShape(spec, setting); err != nil {
		return err
	}
	if spec.kind == kindText && !util.LiteralString(setting.Value, maxSettingValueBytes) {
		return fmt.Errorf("%s must be a literal of at most %d bytes without control bytes, '$', backticks, '{env:' or '{file:' markers",
			setting.Path, maxSettingValueBytes)
	}
	if spec.host && setting.Value != "" && layer != workstationLayer {
		return fmt.Errorf("%s is host data; set it in the workstation layer, not %s", setting.Path, layer)
	}
	if spec.check == nil {
		return nil
	}
	return spec.check(layer, setting)
}

// kindMembers lists which of Value, List and Argv each value kind may carry.
var kindMembers = map[settingKind][3]bool{
	kindText: {true, false, false}, kindBool: {true, false, false}, kindList: {false, true, false},
	kindArgv: {false, false, true}, kindEntry: {false, false, false},
}

func checkShape(spec settingSpec, setting OperatorSetting) error {
	allowed, ok := kindMembers[spec.kind]
	if !ok {
		return fmt.Errorf("%s is a section, not a value", setting.Path)
	}
	present := [3]bool{setting.Value != "", setting.List != nil, setting.Argv != nil}
	for i := range present {
		if present[i] && !allowed[i] {
			return fmt.Errorf("%s has an invalid value", setting.Path)
		}
	}
	if spec.kind == kindBool && setting.Value != "true" && setting.Value != "false" {
		return fmt.Errorf("%s must be true or false", setting.Path)
	}
	if len(setting.List) > spec.maxItems {
		return fmt.Errorf("%s exceeds %d entries", setting.Path, spec.maxItems)
	}
	return nil
}

func (m *operatorMerge) merge(layer string, spec settingSpec, setting OperatorSetting) error {
	switch spec.rule {
	case ruleAppend:
		return m.appendList(layer, spec, setting)
	case ruleTighten:
		return m.tighten(layer, spec, setting)
	case ruleConflict:
		return m.mergeConflicting(layer, setting)
	}
	m.replace(layer, setting)
	return nil
}

// tighten refuses a value that moves away from the strict one a previous layer set.
func (m *operatorMerge) tighten(layer string, spec settingSpec, setting OperatorSetting) error {
	current, set := m.values[setting.Path]
	if set && current.Value == spec.strict && setting.Value != spec.strict {
		return fmt.Errorf("%s loosens %s, set to %s by %s", layer, setting.Path, spec.strict, m.named(setting.Path))
	}
	m.replace(layer, setting)
	return nil
}

// mergeConflicting refuses two different non-empty values; an empty value keeps the other.
func (m *operatorMerge) mergeConflicting(layer string, setting OperatorSetting) error {
	current, set := m.values[setting.Path]
	if set && current.Value != "" && setting.Value != "" && current.Value != setting.Value {
		return fmt.Errorf("%s and %s give different values for %s", m.named(setting.Path), layer, setting.Path)
	}
	if !set || setting.Value != "" {
		m.replace(layer, setting)
	}
	return nil
}

// replace records a scalar. An equal value adds the layer as a further contributor; a
// different value makes the layer the only one.
func (m *operatorMerge) replace(layer string, setting OperatorSetting) {
	if current, set := m.values[setting.Path]; set && sameSetting(current, setting) {
		m.contribute(setting.Path, layer)
	} else {
		m.contributors[setting.Path] = []string{layer}
	}
	m.values[setting.Path] = cloneSetting(setting)
}

func (m *operatorMerge) appendList(layer string, spec settingSpec, setting OperatorSetting) error {
	if len(setting.List) == 0 {
		return nil
	}
	merged := slices.Clone(m.values[setting.Path].List)
	for _, item := range setting.List {
		if !slices.Contains(merged, item) {
			merged = append(merged, item)
		}
	}
	if len(merged) > spec.maxItems {
		return fmt.Errorf("merged %s exceeds %d entries", setting.Path, spec.maxItems)
	}
	m.values[setting.Path] = OperatorSetting{Path: setting.Path, List: merged}
	m.contribute(setting.Path, layer)
	return nil
}

func (m *operatorMerge) contribute(path, layer string) {
	if !slices.Contains(m.contributors[path], layer) {
		m.contributors[path] = append(m.contributors[path], layer)
	}
}

func (m *operatorMerge) named(path string) string { return strings.Join(m.contributors[path], ", ") }

func sameSetting(a, b OperatorSetting) bool {
	return a.Value == b.Value && slices.Equal(a.List, b.List) &&
		slices.EqualFunc(a.Argv, b.Argv, func(x, y []string) bool { return slices.Equal(x, y) })
}

func cloneSetting(setting OperatorSetting) OperatorSetting {
	setting.List = slices.Clone(setting.List)
	argv := make([][]string, len(setting.Argv))
	for i, candidate := range setting.Argv {
		argv[i] = slices.Clone(candidate)
	}
	if setting.Argv != nil {
		setting.Argv = argv
	}
	return setting
}

// finish assembles the typed settings over the built-in values. It returns nil when no layer
// carried an operator section, so a policy without one keeps its previous digest.
func (m *operatorMerge) finish() (*OperatorSettings, map[string][]string, error) {
	if len(m.values) == 0 {
		return nil, nil, nil
	}
	settings := DefaultOperatorSettings()
	if m.values["clients.govern"].Value == GovernListed {
		settings.Clients.Selected = map[clientid.ID]ClientSelection{}
	}
	for path, setting := range m.values {
		if err := assignSetting(&settings, path, setting); err != nil {
			return nil, nil, err
		}
	}
	return &settings, m.contributors, nil
}

func assignSetting(settings *OperatorSettings, path string, setting OperatorSetting) error {
	client := settingClient(path)
	if client == "" {
		return assignTarget(settings.targets(), path, setting)
	}
	selection, ok := settings.Clients.Selected[client]
	if !ok {
		selection = defaultClientSelection()
	}
	field := strings.TrimPrefix(strings.TrimPrefix(genericSettingPath(path), selectedPrefix+"*"), ".")
	if err := assignTarget(selection.targets(), field, setting); err != nil {
		return err
	}
	settings.Clients.Selected[client] = selection
	return nil
}

// targets maps each section path to the field it sets.
func (s *OperatorSettings) targets() map[string]any {
	c, h, u := &s.Clients, &s.Hooks, &s.Update
	return map[string]any{
		"clients.mode": &c.Mode, "clients.verified_max_age": &c.VerifiedMaxAge, "clients.govern": &c.Govern,
		"hooks.scope": &h.Scope, "hooks.command_policy.deny": &h.CommandPolicy.Deny, "hooks.python": &h.Python,
		"update.channel": &u.Channel, "update.source": &u.Source, "update.checkout": &u.Checkout,
		"update.remote": &u.Remote, "update.branch": &u.Branch, "update.pin": &u.Pin,
		"update.interval": &u.Interval, "update.require_signed": &u.RequireSigned,
		"update.allowed_signers": &u.AllowedSigners, "update.receipt_public_key": &u.ReceiptPublicKey,
		"update.bin_dir": &u.BinDir,
	}
}

// targets maps each client key to the field it sets; "" is the entry that lists the client.
func (c *ClientSelection) targets() map[string]any {
	return map[string]any{
		"": nil, "required": &c.Required, "scopes": &c.Scopes, "plugin": &c.Plugin, "binary": &c.Binary,
		"config_root": &c.ConfigRoot, "registry": &c.Registry, "connection_profile": &c.ConnectionProfile,
		"permissions.manage": &c.Permissions.Manage, "permissions.allow": &c.Permissions.Allow,
	}
}

func assignTarget(targets map[string]any, key string, setting OperatorSetting) error {
	target, ok := targets[key]
	if !ok {
		return fmt.Errorf("%s has no settings field", setting.Path)
	}
	var err error
	switch field := target.(type) {
	case nil:
	case *string:
		*field = setting.Value
	case *bool:
		*field = setting.Value == "true"
	case *[]string:
		*field = slices.Clone(setting.List)
	case *[][]string:
		*field = cloneSetting(setting).Argv
	case *time.Duration:
		*field, err = time.ParseDuration(setting.Value)
	default:
		err = fmt.Errorf("%s has an unsupported settings field", setting.Path)
	}
	return err
}

// ResolveOperatorPath resolves a document path from the operator settings (a registry,
// connection profile or allowed-signers file). An absolute value is returned unchanged; a
// relative one is joined to the directory of the layer file that contributed key.
func (p *EffectivePolicy) ResolveOperatorPath(key, value string) (string, error) {
	native := filepath.FromSlash(value)
	if value == "" || filepath.IsAbs(native) {
		return native, nil
	}
	if p == nil || len(p.OperatorFields[key]) == 0 {
		return "", fmt.Errorf("%s has no contributing layer to resolve %q against", key, value)
	}
	contributors := p.OperatorFields[key]
	last := contributors[len(contributors)-1]
	for i := 0; i < len(p.Sources) && i <= maxPolicyLayers; i++ {
		if p.Sources[i].ID == last && p.Sources[i].Path != "" {
			return filepath.Join(filepath.Dir(p.Sources[i].Path), native), nil
		}
	}
	return "", errors.New(key + ": the contributing layer has no file to resolve a relative path against")
}
