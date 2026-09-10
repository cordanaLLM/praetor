package forge

import (
	"context"
	"testing"
)

func TestNewForge_Providers(t *testing.T) {
	ctx := context.Background()

	providers := []string{"github", "gitlab", "gitea", "forgejo"}
	for _, p := range providers {
		f, err := NewForge(p, "test-token", "")
		if err != nil {
			t.Fatalf("failed to create forge driver for %s: %v", p, err)
		}
		if err := f.Authenticate(ctx); err != nil {
			t.Fatalf("expected authentication to pass with token for %s: %v", p, err)
		}
	}

	_, err := NewForge("invalid", "token", "")
	if err == nil {
		t.Fatalf("expected error for unsupported forge provider")
	}
}
