package config

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLoadRegisterRoutingAuthority(t *testing.T) {
	root := t.TempDir()
	body := registerTaskRouting + "offload:\n  labels: [deploy_prod]\nclaude_subagent:\n  deploy_prod: {model: sonnet, effort: high}\n"
	writeCheckedFixture(t, root, ".config/models/routing.yaml", body)
	authority, labels, routing, err := LoadRegisterRoutingAuthority(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if authority.ManifestSHA256() == "" || !slices.Equal(labels, []string{"deploy_prod"}) || routing.ClaudeSubagent["deploy_prod"].Effort != "high" {
		t.Fatalf("snapshot: %v %+v", labels, routing)
	}
	_, oldLabels, offload, err := LoadRegisterDispatchAuthority(t.Context(), root)
	if err != nil || !slices.Equal(labels, oldLabels) || !slices.Equal(offload.Labels, routing.Offload.Labels) {
		t.Fatalf("offload compatibility: %+v %v", offload, err)
	}
}

func TestLoadRegisterRoutingAuthorityNegative(t *testing.T) {
	var nilContext context.Context
	if _, _, _, err := LoadRegisterRoutingAuthority(nilContext, t.TempDir()); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := LoadRegisterRoutingAuthority(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	root := t.TempDir()
	writeCheckedFixture(t, root, ".config/models/routing.yaml", registerTaskRouting+"claude_subagent:\n  deploy_prod: {model: other}\n")
	if _, _, _, err := LoadRegisterRoutingAuthority(t.Context(), root); err == nil || !strings.Contains(err.Error(), "deploy_prod") {
		t.Fatalf("invalid route: %v", err)
	}
}

func TestLoadRegisterRoutingAuthorityAbsentCatalog(t *testing.T) {
	_, labels, routing, err := LoadRegisterRoutingAuthority(t.Context(), t.TempDir())
	if err != nil || routing != nil || !slices.Contains(labels, "feature_implementation") {
		t.Fatalf("absent catalog: %v %+v %v", labels, routing, err)
	}
}
