package router

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// settingsPresence records which operator-owned settings an existing catalog declares:
// the governance keys, and per tier its description, target_tasks and fallback_tier keys.
// A decoded struct cannot tell an explicit false or zero from an omitted key, so presence
// is read from the document itself.
type settingsPresence struct {
	governance map[string]bool
	tiers      map[string]map[string]bool
}

// declaredSettings reads the keys the existing catalog declares. The bytes were already
// decoded and validated by existingCatalog, so this second pass only reads key names.
func declaredSettings(data []byte, exists bool) (settingsPresence, error) {
	presence := settingsPresence{governance: map[string]bool{}, tiers: map[string]map[string]bool{}}
	if !exists {
		return presence, nil
	}
	var doc struct {
		Tiers      map[string]map[string]yaml.Node `yaml:"tiers"`
		Governance map[string]yaml.Node            `yaml:"governance"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return presence, fmt.Errorf("read declared catalog settings: %w", err)
	}
	for key := range doc.Governance {
		presence.governance[key] = true
	}
	for name, fields := range doc.Tiers {
		keys := make(map[string]bool, len(fields))
		for key := range fields {
			keys[key] = true
		}
		presence.tiers[name] = keys
	}
	return presence, nil
}

// keepDeclaredSettings gives governance and default-tier metadata the ownership rule
// model entries have: a value the existing catalog declares wins over the built-in
// default, and only an undeclared key takes the default. Tiers the defaults do not
// define keep their metadata through preserveUnowned.
func keepDeclaredSettings(cfg, existing *RoutingConfig, declared settingsPresence) {
	keepGovernance(&cfg.Governance, existing.Governance, declared.governance)
	for name, tier := range cfg.Tiers {
		keys, ok := declared.tiers[name]
		if !ok {
			continue
		}
		previous := existing.Tiers[name]
		if keys["description"] {
			tier.Description = previous.Description
		}
		if keys["target_tasks"] {
			tier.TargetTasks = slices.Clone(previous.TargetTasks)
		}
		if keys["fallback_tier"] {
			tier.FallbackTier = previous.FallbackTier
		}
		cfg.Tiers[name] = tier
	}
}

func keepGovernance(policy *GovernancePolicy, existing GovernancePolicy, keys map[string]bool) {
	if keys["max_concurrent_same_model"] {
		policy.MaxConcurrentSameModel = existing.MaxConcurrentSameModel
	}
	if keys["exhaustion_threshold_percent"] {
		policy.ExhaustionThresholdPercent = existing.ExhaustionThresholdPercent
	}
	if keys["orthogonal_audit_required"] {
		policy.OrthogonalAuditRequired = existing.OrthogonalAuditRequired
	}
}
