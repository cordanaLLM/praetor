// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// Every built-in required setting is either rendered by apply (the branch ruleset) or names the
// command that writes it, so no built-in flavor reaches the no-renderer error.
func TestRequiredSettings_Positive_EveryBuiltinSettingHasARendererOrProducer(t *testing.T) {
	for _, flv := range builtinFlavorList() {
		for _, s := range flv.RequiredSettings() {
			if s.Path != forge.RepositoryRulesetPath && s.Producer == "" {
				t.Errorf("%s: setting %s has no renderer and no producer", flv.Name(), s.Path)
			}
			if s.Path == forge.RepositoryRulesetPath && s.Producer != "" {
				t.Errorf("%s: the rendered ruleset must not name a producer, got %q", flv.Name(), s.Producer)
			}
		}
	}
}

// A setting apply cannot render and no command produces is an error, never a silent skip.
func TestApplySetting_Negative_NoRendererNoProducer(t *testing.T) {
	_, err := applySetting(t.Context(), t.TempDir(), SettingItem{Path: "config/orphan.json"}, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "config/orphan.json has no renderer and no producer") {
		t.Fatalf("expected the no-renderer error, got %v", err)
	}
}

// A cancelled context stops the settings walk before the next setting.
func TestApplySettings_Boundary_CancelledContextStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report := &ApplyReport{}
	err := applySettings(ctx, t.TempDir(), []SettingItem{{Path: "lefthook.yml", Producer: "x"}}, ApplyOptions{}, report)
	if err == nil || len(report.Settings) != 0 {
		t.Fatalf("a cancelled walk recorded %+v, err %v", report.Settings, err)
	}
}
