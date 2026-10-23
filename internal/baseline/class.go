// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package baseline

import "fmt"

// Class identifies the rule family a baseline file may contain.
type Class string

const (
	// HISSClass includes every rule except the known README presentation rules.
	HISSClass Class = "HISS"
	// READMEClass contains the known README presentation rules.
	READMEClass Class = "README"
)

// ValidateClass refuses mixed documents and documents belonging to another class.
// An empty expected class accepts either family, but never a mixture.
func (b *Baseline) ValidateClass(path string, expected Class) error {
	if b == nil {
		return ErrNilSnapshot
	}
	switch expected {
	case "", HISSClass, READMEClass:
	default:
		return fmt.Errorf("baseline %s: unknown class %q", path, expected)
	}
	hiss, readme := 0, 0
	var foreign *string
	for i := 0; i < len(b.Infractions); i++ {
		rule := b.Infractions[i].RuleID
		class := HISSClass
		if IsReadmeRule(rule) {
			class = READMEClass
			readme++
		} else {
			hiss++
		}
		if expected == "" {
			expected = class
		}
		if class != expected && foreign == nil {
			foreign = &b.Infractions[i].RuleID
		}
	}
	if foreign != nil {
		return fmt.Errorf("baseline %s: HISS=%d entries, README=%d entries; first foreign rule %q (expected %s); run praetorctl adopt to migrate README entries to .readme-baseline.json", path, hiss, readme, *foreign, expected)
	}
	return nil
}
