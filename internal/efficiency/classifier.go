// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Classifier handles model tier and locality classification.
type Classifier struct {
	frontierClasses []string
	localClasses    []string
}

// NewClassifier creates a classifier from efficiency policy.
func NewClassifier(policy *config.EfficiencyPolicy) *Classifier {
	return &Classifier{
		frontierClasses: policy.EffectiveFrontierModels(),
		localClasses:    policy.EffectiveLocalModels(),
	}
}

// IsFrontier reports whether model is classified as frontier.
func (c *Classifier) IsFrontier(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if norm == "" {
		return false
	}
	for i := 0; i < len(c.frontierClasses) && i < 100; i++ {
		fc := strings.ToLower(strings.TrimSpace(c.frontierClasses[i]))
		if fc == "" {
			continue
		}
		if norm == fc || strings.HasPrefix(norm, fc) || strings.Contains(norm, fc) {
			return true
		}
	}
	return false
}

// IsLocal reports whether model is classified as local or cluster.
func (c *Classifier) IsLocal(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if norm == "" {
		return false
	}
	for i := 0; i < len(c.localClasses) && i < 100; i++ {
		lc := strings.ToLower(strings.TrimSpace(c.localClasses[i]))
		if lc == "" {
			continue
		}
		if norm == lc || strings.HasPrefix(norm, lc) || strings.Contains(norm, lc) {
			return true
		}
	}
	return false
}
