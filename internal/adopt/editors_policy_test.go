package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Adopted editor projections state the policy the session resolved, the one the generated
// audit enforces, rather than a literal of their own (#360).

const inspectionProfile = ".idea/inspectionProfiles/standards.xml"

// cyclomaticOption is the one resolved ceiling the JetBrains profile carries (BUG-656).
func cyclomaticOption(value int) string { return fmt.Sprintf(`name="m_limit" value="%d"`, value) }

func TestReconcileEditors_Positive_ProjectsResolvedPolicy(t *testing.T) {
	s, _ := catalogSession(t)
	manifest := filepath.Join(s.repoPath, manifestFile)
	mustWrite(t, manifest, mustRead(t, manifest)+"overrides:\n  complexity:\n    max_cyclomatic: 5\n")
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := reconcileEditors(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if profile := mustRead(t, filepath.Join(s.repoPath, inspectionProfile)); !strings.Contains(profile, cyclomaticOption(5)) {
		t.Errorf("inspection profile ignores the resolved max_cyclomatic 5:\n%s", profile)
	}
}

func TestReconcileEditors_Boundary_UnresolvedPolicyUsesTheCeiling(t *testing.T) {
	s, _ := catalogSession(t)
	s.policy = nil
	if err := reconcileEditors(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if profile := mustRead(t, filepath.Join(s.repoPath, inspectionProfile)); !strings.Contains(profile, cyclomaticOption(config.HISSComplexityCeiling().MaxCyclomatic)) {
		t.Errorf("an unresolved policy must fall back to the HISS-04 ceiling:\n%s", profile)
	}
}

func TestReconcileEditors_Negative_CancelledContextWritesNothing(t *testing.T) {
	s, _ := catalogSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reconcileEditors(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled synthesis = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.repoPath, inspectionProfile)); !os.IsNotExist(err) {
		t.Errorf("cancelled synthesis wrote editor files: %v", err)
	}
}
