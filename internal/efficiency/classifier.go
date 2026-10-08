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

func matchModelClass(model, pattern string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	pat := strings.ToLower(strings.TrimSpace(pattern))
	if norm == "" || pat == "" {
		return false
	}
	clean := norm
	if idx := strings.Index(clean, "/"); idx != -1 {
		clean = clean[idx+1:]
	}
	if norm == pat || clean == pat {
		return true
	}
	return strings.HasPrefix(norm, pat) || strings.HasPrefix(clean, pat)
}

// IsFrontier reports whether model is classified as frontier.
func (c *Classifier) IsFrontier(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	if norm == "" {
		return false
	}
	for i := 0; i < len(c.frontierClasses) && i < 100; i++ {
		fc := c.frontierClasses[i]
		if matchModelClass(norm, fc) {
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
		lc := c.localClasses[i]
		if matchModelClass(norm, lc) {
			return true
		}
	}
	return false
}
