// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"os"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestClassifier_Positive_CurrentFrontierFamilies(t *testing.T) {
	c := NewClassifier(nil)
	for _, model := range []string{
		"claude-opus-4-1", "claude-sonnet-4-5", "claude-fable-5-1", "anthropic/claude-opus-5",
		"gpt-5", "gpt-5.1-codex", "gpt-6-astra", "gemini-3.1-pro-preview", "gemini-2.5-pro", "Gemini-3-Pro-Preview",
	} {
		if !c.IsFrontier("", model) {
			t.Errorf("%s must be frontier", model)
		}
	}
}

func TestClassifier_Positive_RouterClassDecidesFirst(t *testing.T) {
	c := NewClassifier(nil)
	if !c.IsFrontier("cordana/reasoning", "some-routed-model") || !c.IsFrontier("coding", "") {
		t.Error("frontier router classes must classify without a family match")
	}
	if c.IsFrontier("cordana/light", "claude-sonnet-4-5") {
		t.Error("a light class is never frontier, whatever model serves it")
	}
	if !c.IsLocal("local", "any") || !c.IsLocal("", "ollama/llama3") || !c.IsLocal("", "vllm/mistral") || !c.IsLocal("", "local-qwen-7b") {
		t.Error("local class and local model prefixes must classify as local")
	}
}

func TestClassifier_Negative_CheapTiersNeverFrontier(t *testing.T) {
	c := NewClassifier(nil)
	for _, model := range []string{
		"gpt-5-mini", "gpt-5-nano", "gpt-6-mini", "gemini-3.5-flash-lite", "gemini-3.8-flash", "gemini-3-flash-preview",
		"gemini-2.5-flash", "claude-haiku-4-5", "claude-3-haiku", "claude-3-7-sonnet", "gpt-4o-mini", "unknown-small-model", "o1-mini", "",
	} {
		if c.IsFrontier("", model) {
			t.Errorf("%q must not be frontier", model)
		}
	}
	if c.IsLocal("", "claude-3-7-sonnet") || c.IsLocal("", "") || c.IsLocal("", "gpt-5-nano") {
		t.Error("hosted models are not local")
	}
}

func TestClassifier_Boundary_ConfiguredListsReplaceDefaults(t *testing.T) {
	policy := &config.EfficiencyPolicy{FrontierModels: []string{"heavy-"}, FrontierClasses: []string{"deep"}, LightClasses: []string{"tiny"}, LocalModels: []string{"rack"}}
	c := NewClassifier(policy)
	if !c.IsFrontier("", "HEAVY-one") || c.IsFrontier("", "claude-opus-4-1") {
		t.Error("configured families replace the defaults")
	}
	if !c.IsFrontier("deep", "") || c.IsFrontier("reasoning", "x") || c.IsFrontier("tiny", "heavy-one") {
		t.Error("configured classes replace the defaults")
	}
	if !c.IsLocal("rack", "") || c.IsLocal("local", "") {
		t.Error("configured local list replaces the default")
	}
}

// The guide documents the default lists; a drift between the two is a defect.
func TestDefaultsMatchGuide(t *testing.T) {
	data, err := os.ReadFile("../../docs/guides/efficiency-ledger.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(data)
	lists := map[string][]string{
		"frontier_models":  config.DefaultFrontierModels(),
		"frontier_classes": config.DefaultFrontierClasses(),
		"light_classes":    config.DefaultLightClasses(),
		"local_models":     config.DefaultLocalModels(),
		"cheap markers":    config.CheapTierMarkers(),
	}
	for name, list := range lists {
		line := "Default " + name + ": `" + strings.Join(list, "`, `") + "`"
		if !strings.Contains(guide, line) {
			t.Errorf("guide lacks the exact default line %q", line)
		}
	}
}
