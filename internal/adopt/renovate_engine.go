// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. Engine pins move with coordinated adoption.
const renovateEngineDescription = "praetor-managed engine: updates arrive in one coordinated adoption pull request"

func renovateEngineNames() []string {
	repository := strings.TrimPrefix(gomanifest.EngineModulePath, "github.com/")
	return []string{gomanifest.EngineModulePath, gomanifest.EngineModulePath + "/**", engineToolPackage, repository, repository + "/**"}
}

func engineRepository(ctx context.Context, root string) (bool, error) {
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, "go.mod")
	if err != nil || !exists {
		return false, err
	}
	module, _ := gomanifest.ModuleDirective(data)
	return module == gomanifest.EngineModulePath, nil
}

// AuditRenovateEngineBumps verifies engine suppression through adoption's config discovery.
func AuditRenovateEngineBumps(ctx context.Context, root string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	engine, err := engineRepository(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %w", err)
	}
	if engine {
		return "[SKIP] renovate-engine-bumps: engine repository identified by module " + gomanifest.EngineModulePath + ".", nil
	}
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	rel, data, found, err := findRenovateConfig(ctx, s, nil)
	if err != nil {
		return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %w", err)
	}
	if len(s.report.Warnings) > 0 {
		return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %s", strings.Join(s.report.Warnings, "; "))
	}
	if !found {
		return "[PASS] renovate-engine-bumps: No Renovate configuration found; none required.", nil
	}
	if rel == packageJSONFile {
		object, decodeErr := clientjson.DecodeObject(data)
		if decodeErr != nil {
			return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %s: %w", rel, decodeErr)
		}
		data, _ = object.Get("renovate")
	}
	_, changed, err := mergeRenovateEngineRule(ctx, data)
	if err != nil {
		return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %s: %w", rel, err)
	}
	if changed {
		return "", fmt.Errorf("[FAIL] renovate-engine-bumps: %s missing effective packageRules entry %q: matchPackageNames=%v, enabled=false; run praetorctl adopt", rel, renovateEngineDescription, renovateEngineNames())
	}
	return "[PASS] renovate-engine-bumps: " + rel + " disables engine packages; updates arrive through coordinated adoption.", nil
}

func mergeRenovateAdoptionRules(ctx context.Context, s *adoptSession, data []byte, paths []string) ([]byte, []string, bool, error) {
	merged, declared, changed, err := mergeRenovateRule(ctx, data, paths)
	if err != nil {
		return nil, nil, false, err
	}
	engine, err := engineRepository(ctx, s.repoPath)
	if err != nil {
		return nil, nil, false, err
	}
	if engine {
		return merged, declared, changed, nil
	}
	if !changed {
		merged = data
	}
	next, engineChanged, err := mergeRenovateEngineRule(ctx, merged)
	if err != nil {
		return nil, nil, false, err
	}
	if engineChanged {
		merged = next
	}
	return merged, declared, changed || engineChanged, nil
}

func mergeRenovateEngineRule(ctx context.Context, data []byte) ([]byte, bool, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, false, renovateUnsafe("its line endings are mixed")
	}
	if err := clientjson.Validate(ctx, []byte(text)); err != nil {
		return nil, false, renovateUnsafe("engine rule requires strict JSON: %v", err)
	}
	root, err := clientjson.DecodeObject([]byte(text))
	if err != nil {
		return nil, false, renovateUnsafe("engine rule requires an object: %v", err)
	}
	rules, err := renovatePackageRules(root)
	if err != nil {
		return nil, false, err
	}
	next, changed, err := withRenovateEngineRule(rules)
	if err != nil || !changed {
		return nil, changed, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, false, fmt.Errorf("encode engine rules: %w", err)
	}
	encoded, err := root.With("packageRules", raw).Encode()
	if err != nil {
		return nil, false, fmt.Errorf("encode engine configuration: %w", err)
	}
	return []byte(util.RestoreLineEndings(string(encoded), crlf)), true, nil
}

func withRenovateEngineRule(rules []jsontext.Value) ([]jsontext.Value, bool, error) {
	found, err := findRenovateRule(rules, renovateEngineDescription)
	if err != nil {
		return nil, false, err
	}
	start := renovateCoverageStart(rules)
	covered := len(uncoveredRenovateValues(rules, renovateEngineNames(), "matchPackageNames")) == 0
	if found >= 0 && found < start {
		return nil, false, renovateUnsafe("a later packageRules entry may re-enable engine packages after the owned engine rule")
	}
	if covered {
		return rules, false, nil
	}
	raw, err := json.Marshal(renovateRule{Description: renovateEngineDescription, MatchPackageNames: renovateEngineNames()}, json.Deterministic(true))
	if err != nil {
		return nil, false, fmt.Errorf("render engine Renovate rule: %w", err)
	}
	next := append([]jsontext.Value(nil), rules...)
	if found >= 0 {
		next[found] = raw
		return next, true, nil
	}
	if len(next) >= maxRenovateRules {
		return nil, false, renovateUnsafe("engine entry exceeds %d rules", maxRenovateRules)
	}
	return append(next, raw), true, nil
}

func renovateDisablingPatterns(rule jsontext.Value, matcher string) ([]string, bool) {
	object, _ := decodeRenovateRule(rule)
	if object == nil {
		return nil, false
	}
	for i := 0; i < len(object) && i < maxRenovatePatterns; i++ {
		name := object[i].Name
		if !renovateEquivalentMembers[name] && name != matcher {
			return nil, false
		}
	}
	if len(object) > maxRenovatePatterns {
		return nil, false
	}
	if enabled, _ := object.Get("enabled"); enabled.Kind() != 'f' {
		return nil, false
	}
	raw, _ := object.Get(matcher)
	return supportedRenovatePatterns(raw)
}
