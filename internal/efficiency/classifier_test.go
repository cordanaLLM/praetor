// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestClassifier_Positive_FrontierAndLocal(t *testing.T) {
	policy := &config.EfficiencyPolicy{
		FrontierModels: []string{"claude-opus-", "claude-sonnet-", "gpt-5", "heavy-frontier"},
		LocalModels:    []string{"local", "ollama", "vllm"},
	}
	c := NewClassifier(policy)

	// Frontier matches with modern IDs
	if !c.IsFrontier("claude-opus-4-1") {
		t.Error("expected claude-opus-4-1 to be classified as frontier")
	}
	if !c.IsFrontier("claude-sonnet-4-5") {
		t.Error("expected claude-sonnet-4-5 to be classified as frontier")
	}
	if !c.IsFrontier("gpt-5") {
		t.Error("expected gpt-5 to be classified as frontier")
	}
	if !c.IsFrontier("gpt-5-preview") {
		t.Error("expected gpt-5-preview to be classified as frontier")
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
	if !c.IsLocal("vllm/mistral") {
		t.Error("expected vllm/mistral to be classified as local")
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

	// Hosted nano models must NOT match local
	if c.IsLocal("gpt-4.1-nano") {
		t.Error("gpt-4.1-nano should NOT be classified as local")
	}
	if c.IsLocal("claude-3-nano") {
		t.Error("claude-3-nano should NOT be classified as local")
	}

	// Arbitrary substrings must NOT match o1/o3
	if c.IsFrontier("model-foo123") {
		t.Error("model-foo123 should NOT match frontier o1")
	}
}

func TestClassifier_Boundary_DefaultsAndCase(t *testing.T) {
	c := NewClassifier(nil) // nil policy falls back to documented defaults

	// Default family prefix matches
	if !c.IsFrontier("claude-opus-4-1") {
		t.Error("expected claude-opus-4-1 to match default frontier prefixes")
	}
	if !c.IsFrontier("claude-sonnet-4-5") {
		t.Error("expected claude-sonnet-4-5 to match default frontier prefixes")
	}
	if !c.IsFrontier("gpt-5") {
		t.Error("expected gpt-5 to match default frontier prefixes")
	}
	if !c.IsFrontier("gemini-3") {
		t.Error("expected gemini-3 to match default frontier prefixes")
	}
	if !c.IsFrontier("gemini-3-pro") {
		t.Error("expected gemini-3-pro to match default frontier prefixes")
	}
	if !c.IsFrontier("CLAUDE-OPUS-4-1") {
		t.Error("expected case-insensitive match for frontier model")
	}
	if !c.IsLocal("OLLAMA/LLAMA3") {
		t.Error("expected case-insensitive match for local model")
	}
}
