package needs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// selfTargetModule is the framework module migrationEvidenceFixture checks out.
const selfTargetModule = "example.org/fork"

// migrationEntryErrors runs both migration entry points, the plan and the epic, against
// target with the framework source selects, and returns their errors.
func migrationEntryErrors(t *testing.T, target string, source FrameworkSource) []error {
	t.Helper()
	_, planErr := PlanMigration(t.Context(), target, source, nil)
	_, epicErr := GeneratePreMigrationEpic(t.Context(), target, source, nil)
	return []error{planErr, epicErr}
}

// requireSelfTarget fails unless err is a SelfTargetMigrationError naming target and
// framework, matched by directory when sameDirectory and by module otherwise.
func requireSelfTarget(t *testing.T, err error, target, framework string, sameDirectory bool) {
	t.Helper()
	var refused *SelfTargetMigrationError
	if !errors.As(err, &refused) || !errors.Is(err, ErrSelfTargetMigration) {
		t.Fatalf("want a SelfTargetMigrationError, got %v", err)
	}
	if refused.Target != target || refused.Framework != framework || refused.SameDirectory != sameDirectory {
		t.Fatalf("refusal names target %q, framework %q, same directory %v; want %q, %q, %v",
			refused.Target, refused.Framework, refused.SameDirectory, target, framework, sameDirectory)
	}
	if !sameDirectory && refused.Module != selfTargetModule {
		t.Fatalf("refusal names module %q, want %q", refused.Module, selfTargetModule)
	}
	if message := err.Error(); !strings.Contains(message, target) || !strings.Contains(message, framework) {
		t.Fatalf("refusal must name both the target and the framework: %v", err)
	}
}

// TestMigrationRefusesSelfTarget_3D pins that a migration plan and an epic refuse a target
// that is the selected framework's own module (#299): the same directory, a symlink to it,
// another checkout declaring its module, or a module-path selection of it. Distinct modules,
// and nested modules that only share the framework's path prefix, are still analysed.
func TestMigrationRefusesSelfTarget_3D(t *testing.T) {
	consumer, framework := migrationEvidenceFixture(t, "package pgx\ntype Exists struct{}\n")

	t.Run("positive: distinct modules are analysed", func(t *testing.T) {
		for _, err := range migrationEntryErrors(t, consumer, acmeSource(framework)) {
			if err != nil {
				t.Fatalf("a consumer of the framework must be analysed: %v", err)
			}
		}
	})
	t.Run("negative: the framework's own directory", func(t *testing.T) {
		for _, err := range migrationEntryErrors(t, framework, acmeSource(framework)) {
			requireSelfTarget(t, err, framework, framework, true)
		}
	})
	t.Run("negative: a symlink to the framework", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "framework-link")
		symlinkOrSkip(t, framework, link)
		for _, err := range migrationEntryErrors(t, link, acmeSource(framework)) {
			requireSelfTarget(t, err, link, framework, true)
		}
	})
	t.Run("negative: another checkout of the framework module", func(t *testing.T) {
		clone := setupFrameworkCheckout(t, selfTargetModule)
		for _, err := range migrationEntryErrors(t, clone, acmeSource(framework)) {
			requireSelfTarget(t, err, clone, framework, false)
		}
	})
	t.Run("negative: a module-path selection of the target's module", func(t *testing.T) {
		for _, source := range []FrameworkSource{{Checkout: selfTargetModule}, {Module: selfTargetModule}} {
			for _, err := range migrationEntryErrors(t, framework, source) {
				requireSelfTarget(t, err, framework, selfTargetModule, false)
			}
		}
	})
	t.Run("boundary: modules sharing the framework's path prefix are analysed", func(t *testing.T) {
		for _, tc := range []struct{ dir, module string }{
			{filepath.Join(framework, "tools"), selfTargetModule + "/tools"}, // nested inside the checkout
			{t.TempDir(), selfTargetModule + "/v2"},
			{t.TempDir(), selfTargetModule + "ed"}, // a string prefix, not a path boundary
		} {
			writeFixture(t, tc.dir, "go.mod", "module "+tc.module+"\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
			writeFixture(t, tc.dir, "main.go", "package main\nimport \"github.com/jackc/pgx/v5\"\nfunc main() { _ = pgx.Connect }\n")
			for _, err := range migrationEntryErrors(t, tc.dir, acmeSource(framework)) {
				if err != nil {
					t.Fatalf("module %s only shares the framework's prefix and must be analysed: %v", tc.module, err)
				}
			}
		}
	})
	t.Run("boundary: an unreadable target module fails closed", func(t *testing.T) {
		target := t.TempDir()
		writeFixture(t, target, "go.mod", "module example.org/consumer\n"+strings.Repeat("// padding\n", (1<<20)/10+1))
		for _, err := range migrationEntryErrors(t, target, acmeSource(framework)) {
			if err == nil || errors.Is(err, ErrSelfTargetMigration) || !strings.Contains(err.Error(), "resolve migration target module") {
				t.Fatalf("an unresolvable target module must fail closed, not pass or read as self-target: %v", err)
			}
		}
	})
}

// TestAnalyzeMigrationRefusesSelfTargetBeforeScan pins that the refusal comes before the
// target is scanned, so no candidate is ever scored against the framework itself.
func TestAnalyzeMigrationRefusesSelfTargetBeforeScan(t *testing.T) {
	_, framework := migrationEvidenceFixture(t, "package pgx\n")
	scans := 0
	_, err := analyzeMigrationWith(t.Context(), framework, acmeSource(framework), nil, func(*FrameworkIndex) (*RepoNeeds, error) {
		scans++
		return nil, errors.New("scan must not run")
	})
	requireSelfTarget(t, err, framework, framework, true)
	if scans != 0 {
		t.Fatalf("the target was scanned %d times before the refusal", scans)
	}
}

// TestRegenerateFleetEpics_SkipsSelectedFramework pins that fleet regeneration reports the
// selected framework's own checkout as a skip with its reason, whether the framework is
// selected by checkout or by module, and still generates the consumer's epic.
func TestRegenerateFleetEpics_SkipsSelectedFramework(t *testing.T) {
	root := t.TempDir()
	consumer := filepath.Join(root, "org", "consumer")
	framework := filepath.Join(root, "org", "framework")
	writeFixture(t, consumer, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, consumer, "go.mod", "module example.org/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixture(t, consumer, "main.go", "package main\nimport \"github.com/jackc/pgx/v5\"\nfunc main() { _ = pgx.Connect }\n")
	writeFixture(t, framework, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, framework, "go.mod", "module "+selfTargetModule+"\ngo 1.27\n")
	writeFixture(t, framework, "db/pgx/doc.go", "package pgx\ntype Exists struct{}\n")

	for _, source := range []FrameworkSource{acmeSource(framework), {Checkout: selfTargetModule}} {
		epics, skips, err := RegenerateFleetEpics(t.Context(), root, FleetEpicOptions{Framework: source, DryRun: true})
		if err != nil {
			t.Fatalf("framework %q: the framework's own checkout must be a skip, not a failure: %v", source.Checkout, err)
		}
		if len(epics) != 1 || epics[0].OutputPath != filepath.Join(consumer, fleetEpicFileName) {
			t.Fatalf("framework %q: want the consumer's epic only, got %d epics", source.Checkout, len(epics))
		}
		if len(skips) != 1 || skips[0].RepoDir != framework || skips[0].Reason != selfTargetReason {
			t.Fatalf("framework %q: want the framework skipped with its reason, got %+v", source.Checkout, skips)
		}
	}
}
