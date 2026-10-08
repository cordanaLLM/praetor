// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Classifier handles model tier and locality classification. The router class (model group)
// decides first; the model family list decides when no class is known or the class is neither
// frontier nor light.
type Classifier struct {
	frontierClasses []string
	lightClasses    []string
	frontierModels  []string
	localModels     []string
	cheapMarkers    []string
}

// NewClassifier creates a classifier from efficiency policy (nil selects the defaults).
func NewClassifier(policy *config.EfficiencyPolicy) *Classifier {
	return &Classifier{
		frontierClasses: policy.EffectiveFrontierClasses(),
		lightClasses:    policy.EffectiveLightClasses(),
		frontierModels:  policy.EffectiveFrontierModels(),
		localModels:     policy.EffectiveLocalModels(),
		cheapMarkers:    config.CheapTierMarkers(),
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// className strips a router prefix: "gateway/coding" -> "coding".
func className(group string) string {
	norm := normalize(group)
	if idx := strings.LastIndex(norm, "/"); idx != -1 {
		return norm[idx+1:]
	}
	return norm
}

func inList(list []string, value string) bool {
	for i := 0; i < len(list) && i < 100; i++ {
		if normalize(list[i]) == value {
			return true
		}
	}
	return false
}

func matchModelClass(model, pattern string) bool {
	norm := normalize(model)
	pat := normalize(pattern)
	if norm == "" || pat == "" {
		return false
	}
	clean := norm
	if idx := strings.Index(clean, "/"); idx != -1 {
		clean = clean[idx+1:]
	}
	return strings.HasPrefix(norm, pat) || strings.HasPrefix(clean, pat)
}

func (c *Classifier) isCheapTier(model string) bool {
	tokens := strings.FieldsFunc(normalize(model), func(r rune) bool {
		return r == '-' || r == '.' || r == '/' || r == ':' || r == '_'
	})
	for _, token := range tokens {
		if inList(c.cheapMarkers, token) {
			return true
		}
	}
	return false
}

// IsFrontier reports whether the request is frontier: by router class first, then by model
// family. A cheap-tier model name is never frontier.
func (c *Classifier) IsFrontier(group, model string) bool {
	if class := className(group); class != "" {
		if inList(c.lightClasses, class) {
			return false
		}
		if inList(c.frontierClasses, class) {
			return true
		}
	}
	if normalize(model) == "" || c.isCheapTier(model) {
		return false
	}
	for i := 0; i < len(c.frontierModels) && i < 100; i++ {
		if matchModelClass(model, c.frontierModels[i]) {
			return true
		}
	}
	return false
}

// IsLocal reports whether the request ran on a local or cluster model, by class then model.
func (c *Classifier) IsLocal(group, model string) bool {
	if class := className(group); class != "" && inList(c.localModels, class) {
		return true
	}
	for i := 0; i < len(c.localModels) && i < 100; i++ {
		if matchModelClass(model, c.localModels[i]) {
			return true
		}
	}
	return false
}
