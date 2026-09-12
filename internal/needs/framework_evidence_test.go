package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkAvailabilityRequiresExactCatalogPackage(t *testing.T) {
	root := setupFrameworkCheckout(t, "example.com/observed", "db", "cache")
	writeFixture(t, root, "db/doc.go", "package db\n")
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if index.ProvidesCapability("db.postgres") || index.ProvidesCapability("cache.redis") {
		t.Fatal("domain directories must not imply specific replacement packages")
	}
	writeFixture(t, root, "db/pgx/doc.go", "package pgx\nfunc Available() {}\n")
	index, err = InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !index.ProvidesCapability("db.postgres") || index.ProvidesCapability("db.orm") {
		t.Fatalf("exact pgx package must provide only its declared catalog capabilities: %+v", index.Capabilities)
	}
	report, err := ScanRepoWithFramework(t.Context(), setupFixtureRepo(t), index)
	if err != nil {
		t.Fatal(err)
	}
	if report.Readiness.Score != 25 {
		t.Fatalf("one available dependency out of four must be 25%%: %+v", report.Readiness)
	}
}

func TestFrameworkPackageHeaderAloneDoesNotEstablishAvailability(t *testing.T) {
	for _, source := range []string{
		"// Package pgx is a planned adapter.\npackage pgx\n",
		"package pgx\nimport _ \"fmt\"\n",
	} {
		root := setupFrameworkCheckout(t, "example.com/observed", "db/pgx")
		writeFixture(t, root, "db/pgx/doc.go", source)
		index, err := InspectFramework(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		if index.ProvidesCapability("db.postgres") {
			t.Fatal("package documentation/header/imports with no library declarations are an unavailable stub")
		}
	}
}

func TestFrameworkNestedModuleDoesNotProvideParentReplacement(t *testing.T) {
	root := setupFrameworkCheckout(t, "example.com/parent", "db/pgx")
	writeFixture(t, root, "db/go.mod", "module example.com/other\ngo 1.27\n")
	writeFixture(t, root, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if index.ProvidesCapability("db.postgres") {
		t.Fatal("nested module cannot establish a replacement in the parent module")
	}
}

func TestFrameworkDeclaredAndObservedEvidenceStayDistinct(t *testing.T) {
	declared, err := InspectFramework(t.Context(), "")
	if err != nil || declared.Basis != FrameworkCatalogDeclared {
		t.Fatalf("catalog basis: %+v %v", declared, err)
	}
	root := setupFrameworkCheckout(t, "example.com/observed", "db/pgx")
	writeFixture(t, root, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	observed, err := InspectFramework(t.Context(), root)
	if err != nil || observed.Basis != FrameworkSourceObserved || observed.Version != "unverified" {
		t.Fatalf("observed basis: %+v %v", observed, err)
	}
	report, err := ScanRepoWithFramework(t.Context(), setupFixtureRepo(t), observed)
	if err != nil || report.Readiness.Basis != FrameworkSourceObserved {
		t.Fatalf("report basis: %+v %v", report, err)
	}
	for _, dep := range report.Dependencies {
		if dep.Capability == "db.postgres" && dep.GolusorisReplacement != "example.com/observed/db/pgx" {
			t.Fatalf("replacement does not identify observed fork package: %+v", dep)
		}
	}
}

func TestFrameworkObservedSourceFailuresAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{"invalid source", func(t *testing.T, root string) { writeFixture(t, root, "db/pgx/doc.go", "package pgx\nfunc broken(") }, "parse framework source"},
		{"conflicting modules", func(t *testing.T, root string) {
			writeFixture(t, root, "go.mod", "module example.com/a\nmodule example.com/b\n")
		}, "one unambiguous module"},
		{"entry limit", func(t *testing.T, root string) {
			for i := 0; i <= maxFrameworkPackageEntries; i++ {
				writeFixture(t, root, fmt.Sprintf("db/pgx/file-%d.txt", i), "data")
			}
		}, "128 directory entries"},
		{"oversized source", func(t *testing.T, root string) {
			writeFixture(t, root, "db/pgx/doc.go", "package pgx\n//"+strings.Repeat("x", 1<<20))
		}, "at most"},
		{"source symlink", func(t *testing.T, root string) {
			target := writeFixture(t, t.TempDir(), "outside.go", "package pgx\ntype Available struct{}\n")
			if err := os.Symlink(target, filepath.Join(root, "db/pgx/doc.go")); err != nil {
				t.Fatal(err)
			}
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupFrameworkCheckout(t, "example.com/observed", "db/pgx")
			tc.setup(t, root)
			if _, err := InspectFramework(t.Context(), root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted explicit %s error, got %v", tc.name, err)
			}
		})
	}
}

func TestFrameworkTestOnlyAndMainPackagesDoNotProvideLibrary(t *testing.T) {
	for _, name := range []string{"only_test.go", "main.go"} {
		t.Run(name, func(t *testing.T) {
			root := setupFrameworkCheckout(t, "example.com/observed", "db/pgx")
			writeFixture(t, root, "db/pgx/"+name, "package main\n")
			index, err := InspectFramework(t.Context(), root)
			if err != nil || index.ProvidesCapability("db.postgres") {
				t.Fatalf("non-library source implied capability: %+v %v", index, err)
			}
		})
	}
}

func TestFrameworkInspectionAcceptsEntryBoundaryAndRejectsAggregateOverflow(t *testing.T) {
	root := setupFrameworkCheckout(t, "example.com/observed", "db/pgx")
	writeFixture(t, root, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	for i := 1; i < 128; i++ {
		writeFixture(t, root, fmt.Sprintf("db/pgx/file-%d.txt", i), "data")
	}
	index, err := InspectFramework(t.Context(), root)
	if err != nil || !index.ProvidesCapability("db.postgres") {
		t.Fatalf("128 entries should retain complete source evidence: %+v %v", index, err)
	}
	large := setupFrameworkCheckout(t, "example.com/large", "db/pgx")
	for i := 0; i < 8; i++ {
		prefix := fmt.Sprintf("package pgx\nconst Available%d = 1\n//", i)
		source := prefix + strings.Repeat("x", (1<<20)-len(prefix))
		writeFixture(t, large, fmt.Sprintf("db/pgx/source%d.go", i), source)
	}
	if _, err := InspectFramework(t.Context(), large); err != nil {
		t.Fatalf("8 MiB source boundary must be accepted: %v", err)
	}
	writeFixture(t, large, "db/pgx/overflow.go", "package pgx\ntype Available struct{}\n")
	if _, err := InspectFramework(t.Context(), large); err == nil || !strings.Contains(err.Error(), "8 MiB aggregate") {
		t.Fatalf("aggregate overflow must not yield partial availability: %v", err)
	}
}

func TestFrameworkExplicitMissingPathDoesNotFallBackToCatalog(t *testing.T) {
	_, err := InspectFramework(t.Context(), filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("explicitly missing framework must fail")
	}
}

func TestFrameworkEmptyContextIsRejected(t *testing.T) {
	var absent context.Context
	if _, err := InspectFramework(absent, ""); err == nil {
		t.Fatal("missing context must fail")
	}
}

func TestFrameworkModuleIdentityQuotingAndInvalidPaths(t *testing.T) {
	for _, directive := range []string{
		`module "example.com/observed" // source URL https://example.com/docs`,
		`module "example.com/\x6fbserved"//comment without a preceding space`,
	} {
		root := setupFrameworkCheckout(t, "example.com/original", "db/pgx")
		writeFixture(t, root, "go.mod", directive+"\ngo 1.27\n")
		writeFixture(t, root, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
		index, err := InspectFramework(t.Context(), root)
		if err != nil || index.Name != "example.com/observed" {
			t.Fatalf("valid quoted module/comment: %+v %v", index, err)
		}
	}
	for _, path := range []string{
		`"https://example.com/project"`, `example.com/../escape`, `example.com/project@v1`,
		`"example.com/\x1bproject"`, `"example.com/project//other"`, `example.com/project/`,
		"\nmodule example.com/observed", `"example.com/observed" extra`,
	} {
		t.Run(path, func(t *testing.T) {
			root := setupFrameworkCheckout(t, "example.com/original", "db/pgx")
			writeFixture(t, root, "go.mod", "module "+path+"\n")
			if _, err := InspectFramework(t.Context(), root); err == nil {
				t.Fatal("invalid module identity must not enter replacement paths")
			}
		})
	}
}
