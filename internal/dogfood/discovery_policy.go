package dogfood

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ValidateDiscoveryPolicy validates the bounded, path-local discovery vocabulary.
func ValidateDiscoveryPolicy(policy DiscoveryPolicy) error {
	if _, err := normalizeInputLimits(policy.InputLimits); err != nil {
		return err
	}
	if policy.Version != 1 || len(policy.Rules) == 0 || len(policy.Rules) > maxDiscoveryRules {
		return errors.New("discovery policy requires version 1 and 1..128 rules")
	}
	seen := make(map[string]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		if err := validateDiscoveryRule(rule, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateDiscoveryRule(rule DiscoveryRule, seen map[string]struct{}) error {
	if rule.Key == "" || len(rule.Key) > maxDiscoveryKeyBytes || !safeDiscoveryText(rule.Key) {
		return fmt.Errorf("invalid discovery key %q", rule.Key)
	}
	if rule.Title == "" || len(rule.Title) > maxDiscoveryTitleBytes || !safeDiscoveryText(rule.Title) {
		return fmt.Errorf("invalid discovery title for %q", rule.Key)
	}
	if _, ok := seen[rule.Key]; ok {
		return fmt.Errorf("duplicate discovery key %q", rule.Key)
	}
	seen[rule.Key] = struct{}{}
	if err := validateDiscoveryMatches(rule); err != nil {
		return err
	}
	return validateDiscoveryKind(rule)
}

func validateDiscoveryMatches(rule DiscoveryRule) error {
	if len(rule.Matches) == 0 || len(rule.Matches) > maxDiscoveryMatches {
		return fmt.Errorf("discovery rule %q requires 1..32 matches", rule.Key)
	}
	matchSeen := make(map[string]struct{}, len(rule.Matches))
	for _, match := range rule.Matches {
		if match == "" || len(match) > maxDiscoveryMatchBytes || !safeDiscoveryText(match) || strings.Contains(match, "..") {
			return fmt.Errorf("invalid discovery match for %q", rule.Key)
		}
		if _, ok := matchSeen[match]; ok {
			return fmt.Errorf("duplicate discovery match for %q", rule.Key)
		}
		if rule.Kind != discoveryScannerExtension && strings.ContainsAny(strings.TrimPrefix(match, "*."), "*?[]") {
			return fmt.Errorf("invalid marker pattern for %q", rule.Key)
		}
		matchSeen[match] = struct{}{}
	}
	return nil
}

func validateDiscoveryKind(rule DiscoveryRule) error {
	switch rule.Kind {
	case discoveryScannerExtension:
		for _, match := range rule.Matches {
			if !validDiscoveryExtension(match) || rule.Analyzer != "hiss" {
				return fmt.Errorf("invalid scanner extension rule %q", rule.Key)
			}
		}
	case discoveryNeedsMarker:
		if rule.Analyzer == "" || len(rule.Analyzer) > maxDiscoveryKeyBytes || !safeDiscoveryText(rule.Analyzer) {
			return fmt.Errorf("needs marker %q requires analyzer", rule.Key)
		}
	case discoveryVerificationMarker:
		if rule.Analyzer != "adopt" {
			return fmt.Errorf("verification marker %q requires analyzer adopt", rule.Key)
		}
	default:
		return fmt.Errorf("unsupported discovery rule kind %q", rule.Kind)
	}
	return nil
}

func safeDiscoveryText(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

func validDiscoveryExtension(value string) bool {
	if len(value) < 2 || value[0] != '.' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}
