package needs

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func libraryRelationshipFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", `module example.com/relationships
go 1.27
require (
 go.uber.org/fx v1.24.0
 github.com/knadh/koanf/v2 v2.3.0
 github.com/lmittmann/tint v1.1.2
 github.com/ogen-go/ogen v1.13.0
 github.com/jackc/pgx/v5 v5.7.2
 github.com/riverqueue/river v0.23.1
)
`)
	writeFixture(t, root, "consumer.go", `package consumer
import (
 _ "go.uber.org/fx"
 _ "github.com/knadh/koanf/v2"
 _ "github.com/lmittmann/tint"
 _ "github.com/ogen-go/ogen/middleware"
 _ "github.com/ogen-go/ogen/ogenerrors"
 _ "github.com/jackc/pgx/v5"
 _ "github.com/jackc/pgx/v5/pgxpool"
 _ "github.com/riverqueue/river"
 _ "log/slog"
 _ "fmt"
)
`)
	writeFixture(t, root, "logging.go", "package consumer\nimport _ \"log/slog\"\n")
	return root
}

func relationshipDemand(t *testing.T, report *RepoNeeds, pkg string) DependencyDemand {
	t.Helper()
	for _, dep := range report.Dependencies {
		if dep.Package == pkg {
			return dep
		}
	}
	t.Fatalf("missing dependency %q in %+v", pkg, report.Dependencies)
	return DependencyDemand{}
}

func TestLibraryRelationshipCatalogBoundaries(t *testing.T) {
	for _, tc := range []struct {
		pkg        string
		capability CapabilityKey
		kind       LibraryRelationshipKind
		target     string
	}{
		{"go.uber.org/fx", "runtime.di", RelationshipFoundation, ""},
		{"github.com/knadh/koanf/v2", "config.loader", RelationshipWrappedBy, "/config"},
		{"github.com/lmittmann/tint", "telemetry.logging", RelationshipWrappedBy, "/log"},
		{"github.com/ogen-go/ogen", "http.openapi", RelationshipTooling, "/ogenkit"},
	} {
		t.Run(tc.pkg, func(t *testing.T) {
			for _, pkg := range []string{tc.pkg, tc.pkg + "/subpkg"} {
				entry, found := MatchPackage(pkg)
				if !found || entry.Capability != tc.capability || entry.Relationship == nil {
					t.Fatalf("missing relationship for %q: %+v", pkg, entry)
				}
				wantTarget := ""
				if tc.target != "" {
					wantTarget = defaultFrameworkModule + tc.target
				}
				want := LibraryRelationship{Kind: tc.kind, FrameworkPackage: wantTarget, Basis: FrameworkCatalogDeclared}
				if *entry.Relationship != want || entry.GolusorisReplacement != "" {
					t.Fatalf("relationship must not be a replacement: %+v", entry)
				}
			}
			if entry, found := MatchPackage(tc.pkg + "-unrelated"); found {
				t.Fatalf("relationship escaped module boundary: %+v", entry)
			}
		})
	}
}

func TestLibraryRelationshipsKeepThirdPartyAccounting(t *testing.T) {
	repo := libraryRelationshipFixture(t)
	index, err := InspectFramework(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := ScanRepoWithFramework(t.Context(), repo, index)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Dependencies) != 6 || report.Readiness.TotalThirdPartyDeps != 6 || report.Readiness.CoveredDeps != 6 || report.Readiness.GapDeps != 0 {
		t.Fatalf("declared relationships changed six-module accounting: %+v", report)
	}
	if len(report.StandardLibraryImports) != 1 {
		t.Fatalf("slog must be deduplicated separately from third-party modules: %+v", report.StandardLibraryImports)
	}
	slog := report.StandardLibraryImports[0]
	if slog.Package != "log/slog" || slog.Status != StatusNative || slog.Capability != "telemetry.logging" || slog.GolusorisReplacement != "" || slog.Relationship == nil || slog.Relationship.Kind != RelationshipFoundation {
		t.Fatalf("stdlib slog must remain a native foundation: %+v", slog)
	}
	fx := relationshipDemand(t, report, "go.uber.org/fx")
	if fx.Status != StatusNative || fx.Capability != "runtime.di" || fx.GolusorisReplacement != "" {
		t.Fatalf("fx is a foundation, not a CLI replacement: %+v", fx)
	}
	if dep := relationshipDemand(t, report, "github.com/jackc/pgx/v5"); dep.Version != "v5.7.2" {
		t.Fatalf("subimport collapse lost the declared module version: %+v", dep)
	}
}

func TestLibraryRelationshipTargetsRequireObservedPackages(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		status       CapabilityStatus
	}{
		{"missing", "", StatusGap},
		{"header only", "package adapter\n", StatusGap},
		{"declarations", "package adapter\ntype Available struct{}\n", StatusCovered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			framework := setupFrameworkCheckout(t, "example.com/selected", "config", "log", "ogenkit")
			for _, target := range []string{"config", "log", "ogenkit"} {
				writeFixture(t, framework, target+"/sibling/adapter.go", "package sibling\ntype Available struct{}\n")
				if tc.source != "" {
					writeFixture(t, framework, target+"/adapter.go", tc.source)
				}
			}
			index, err := InspectFramework(t.Context(), framework)
			if err != nil {
				t.Fatal(err)
			}
			report, err := ScanRepoWithFramework(t.Context(), libraryRelationshipFixture(t), index)
			if err != nil {
				t.Fatal(err)
			}
			checkObservedRelationships(t, report, tc.status)
		})
	}
}

func checkObservedRelationships(t *testing.T, report *RepoNeeds, status CapabilityStatus) {
	t.Helper()
	for pkg, target := range map[string]string{
		"github.com/knadh/koanf/v2": "/config",
		"github.com/lmittmann/tint": "/log",
		"github.com/ogen-go/ogen":   "/ogenkit",
	} {
		dep := relationshipDemand(t, report, pkg)
		if dep.Status != status || dep.Relationship == nil || dep.GolusorisReplacement != "" {
			t.Fatalf("incorrect observed adapter state: %+v", dep)
		}
		if status == StatusCovered && (dep.Relationship.FrameworkPackage != "example.com/selected"+target || dep.Relationship.Basis != FrameworkSourceObserved) {
			t.Fatalf("adapter source must identify exact fork package: %+v", dep)
		}
	}
	fx := relationshipDemand(t, report, "go.uber.org/fx")
	if fx.Status != StatusNative || fx.Relationship == nil || fx.Relationship.Basis != FrameworkCatalogDeclared || fx.Relationship.FrameworkPackage != "" {
		t.Fatalf("framework observation must not invent foundation evidence: %+v", fx)
	}
	if report.Readiness.TotalThirdPartyDeps != 6 {
		t.Fatalf("source observation changed dependency count: %+v", report.Readiness)
	}
}

func TestLibraryRelationshipScansDoNotContaminateCatalog(t *testing.T) {
	repo := libraryRelationshipFixture(t)
	framework := setupFrameworkCheckout(t, "example.com/first", "config")
	writeFixture(t, framework, "config/adapter.go", "package config\ntype Available struct{}\n")
	index, err := InspectFramework(t.Context(), framework)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ScanRepoWithFramework(t.Context(), repo, index)
	if err != nil {
		t.Fatal(err)
	}
	dep := relationshipDemand(t, observed, "github.com/knadh/koanf/v2")
	if dep.Relationship == nil {
		t.Fatal("observed relationship missing")
	}
	dep.Relationship.FrameworkPackage = "example.com/caller-edited"
	declared, err := ScanRepo(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := LibraryRelationship{Kind: RelationshipWrappedBy, FrameworkPackage: defaultFrameworkModule + "/config", Basis: FrameworkCatalogDeclared}
	fresh := relationshipDemand(t, declared, "github.com/knadh/koanf/v2")
	if fresh.Relationship == nil || *fresh.Relationship != want {
		t.Fatalf("one scan or caller mutation contaminated later declarations: %+v", fresh)
	}
}

func TestLibraryRelationshipsNeverBecomeMigrationActions(t *testing.T) {
	repo := libraryRelationshipFixture(t)
	framework := setupFrameworkCheckout(t, "example.com/selected", "config", "log", "ogenkit", "db/pgx")
	for _, target := range []string{"config", "log", "ogenkit", "db/pgx"} {
		writeFixture(t, framework, target+"/adapter.go", "package adapter\ntype Available struct{}\n")
	}
	analysis, err := analyzeMigration(t.Context(), repo, framework)
	if err != nil {
		t.Fatal(err)
	}
	// A legacy or future covered label cannot turn a relationship into a substitution.
	for i := range analysis.report.Dependencies {
		dep := &analysis.report.Dependencies[i]
		if dep.Relationship != nil {
			dep.Status = StatusCovered
			dep.GolusorisReplacement = "example.com/selected/unsafe"
		}
	}
	plan, err := planMigrationFromAnalysis(t.Context(), repo, analysis)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.DroppedRequires, []string{"github.com/jackc/pgx/v5"}) || len(plan.Replacements) != 2 {
		t.Fatalf("relationship became a migration action or legacy pgx mapping disappeared: %+v", plan)
	}
	for _, action := range plan.Replacements {
		if !strings.HasPrefix(action.OldImport, "github.com/jackc/pgx/v5") || strings.Contains(action.NewImport, "unsafe") {
			t.Fatalf("relationship import scheduled for rewrite: %+v", action)
		}
	}
}

func TestLibraryRelationshipSerializationIsAdditive(t *testing.T) {
	report, err := ScanRepo(t.Context(), libraryRelationshipFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		{"json", json.Marshal, json.Unmarshal},
		{"yaml", yaml.Marshal, yaml.Unmarshal},
	} {
		t.Run(codec.name, func(t *testing.T) {
			data, err := codec.marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var restored RepoNeeds
			if err := codec.unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored.Dependencies, report.Dependencies) || !reflect.DeepEqual(restored.StandardLibraryImports, report.StandardLibraryImports) || restored.Readiness != report.Readiness {
				t.Fatalf("relationship or accounting lost in %s round trip: %+v", codec.name, restored)
			}
			var legacy RepoNeeds
			if err := codec.unmarshal([]byte(`{"version":1,"dependencies":[{"package":"example.com/legacy","status":"gap"}]}`), &legacy); err != nil {
				t.Fatal(err)
			}
			if len(legacy.Dependencies) != 1 || legacy.Dependencies[0].Relationship != nil || len(legacy.StandardLibraryImports) != 0 {
				t.Fatalf("legacy manifest without additive fields changed meaning: %+v", legacy)
			}
		})
	}
}

func TestLibraryRelationshipUnknownFoundationCannotClaimRetention(t *testing.T) {
	index, err := InspectFramework(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	report := &RepoNeeds{Dependencies: []DependencyDemand{{
		Package: "example.com/unrecognized", Capability: "runtime.di", Status: StatusNative,
		GolusorisReplacement: "example.com/unverified/replacement",
		Relationship:         &LibraryRelationship{Kind: RelationshipFoundation, Basis: FrameworkSourceObserved},
	}}}
	applyFrameworkCoverage(index, report)
	dep := report.Dependencies[0]
	if dep.Status != StatusGap || dep.Relationship == nil || dep.Relationship.Basis != "unverified" || dep.GolusorisReplacement != "" {
		t.Fatalf("caller-supplied relationship bypassed catalog reconciliation: %+v", dep)
	}
	output := FormatLibraryRelationships(report)
	if strings.Contains(output, "retain library") || !strings.Contains(output, "unverified") {
		t.Fatalf("rejected relationship rendered as a retained foundation: %s", output)
	}
}
