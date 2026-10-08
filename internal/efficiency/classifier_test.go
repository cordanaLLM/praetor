// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestClassifier_Positive_FrontierAndLocal(t *testing.T) {
	policy := &config.EfficiencyPolicy{
		FrontierModels: []string{"claude-3-7-sonnet", "gpt-4o", "heavy-frontier"},
		LocalModels:    []string{"local", "ollama", "nano"},
	}
	c := NewClassifier(policy)

	// Frontier matches
	if !c.IsFrontier("claude-3-7-sonnet-20250219") {
		t.Error("expected claude-3-7-sonnet-20250219 to be classified as frontier")
	}
	if !c.IsFrontier("gpt-4o") {
		t.Error("expected gpt-4o to be classified as frontier")
	}
	if !c.IsFrontier("heavy-frontier") {
		t.Error("expected heavy-frontier tier to be classified as frontier")
	}

	// Local matches
	if !c.IsLocal("local-qwen-7b") {
		t.Error("expected local-qwen-7b to be classified as local")
	}
	if !c.IsLocal("ollama/llama3") {
		t.Error("expected ollama/llama3 to be classified as local")
	}
	if !c.IsLocal("nano") {
		t.Error("expected nano tier to be classified as local")
	}
}

func TestClassifier_Negative_NonMatching(t *testing.T) {
	c := NewClassifier(&config.EfficiencyPolicy{})

	if c.IsFrontier("unknown-small-model") {
		t.Error("expected unknown model not to be classified as frontier")
	}
	if c.IsLocal("claude-3-7-sonnet") {
		t.Error("expected claude-3-7-sonnet not to be classified as local")
	}
	if c.IsFrontier("") {
		t.Error("empty model name should not be frontier")
	}
	if c.IsLocal("") {
		t.Error("empty model name should not be local")
	}
}

func TestClassifier_Boundary_DefaultsAndCase(t *testing.T) {
	c := NewClassifier(nil) // nil policy falls back to documented defaults

	if !c.IsFrontier("CLAUDE-3-7-SONNET") {
		t.Error("expected case-insensitive match for frontier model")
	}
	if !c.IsLocal("OLLAMA") {
		t.Error("expected case-insensitive match for local model")
	}
}
