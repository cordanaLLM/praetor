// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

// backlogFixtureRepo creates a fixture repository in t.TempDir with an initialized .workingdir,
// optional defects in BUGS.md, optional tasks in OPEN.md, and loads the effective policy.
func backlogFixtureRepo(t *testing.T, facets []string, exceptions []config.Exception, defects int, tasks int) (string, *config.EffectivePolicy) {
	t.Helper()
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner: "acme",
			Name:  "widgets",
		},
		Facets:     facets,
		Exceptions: exceptions,
	}
	manifestData, err := config.RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".standards.yaml", string(manifestData))
	if err := state.InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	if defects > 0 {
		addDefects(t, dir, defects)
	}
	if tasks > 0 {
		for i := 0; i < tasks; i++ {
			if err := state.AddTask(dir, fmt.Sprintf("task %d", i+1)); err != nil {
				t.Fatal(err)
			}
		}
	}
	lockData, err := config.BuildLockfile(t.Context(), repoRoot, manifest)
	if err != nil {
		t.Fatalf("build lockfile: %v", err)
	}
	writeFixtureFile(t, dir, ".standards.lock", string(lockData))
	opts := config.EffectiveOptions{Root: dir, CatalogRoot: repoRoot, Audit: true}
	policy, err := config.LoadEffectivePolicyContext(t.Context(), opts)
	if err != nil {
		t.Fatalf("load effective policy: %v", err)
	}
	return dir, policy
}

// TestAuditBacklogCaps_BoundedFacet_DefectsCap: facet listed, 20 open defects passes (inclusive cap);
// 21 defects fails naming defects, 21, and cap 20.
func TestAuditBacklogCaps_BoundedFacet_DefectsCap(t *testing.T) {
	// 20 open defects: cap 20 is inclusive, audit passes
	dir20, policy20 := backlogFixtureRepo(t, []string{"backlog:bounded"}, nil, 20, 0)
	out20, err20 := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dir20, policy20)
	})
	if err20 != nil {
		t.Fatalf("at cap (20 defects) must pass, got error: %v\n%s", err20, out20)
	}
	mustContain(t, out20, "defects: 20 of 20, at the cap", "[PASS] Backlog caps")

	// 21 open defects: over cap 20, audit fails naming defects, 21, and cap 20
	dir21, policy21 := backlogFixtureRepo(t, []string{"backlog:bounded"}, nil, 21, 0)
	out21, err21 := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dir21, policy21)
	})
	if err21 == nil {
		t.Fatalf("over cap (21 defects) must fail, but passed\n%s", out21)
	}
	mustErrContain(t, err21, "backlog cap defects: 21 items, over the cap of 20")
}

// TestAuditBacklogCaps_BoundedFacet_DefectsException: facet listed, 21 defects, unexpired exception
// backlog.caps.defects: audit passes and names the exception and its expiry; expired exception: audit fails.
func TestAuditBacklogCaps_BoundedFacet_DefectsException(t *testing.T) {
	unexpiredDate := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	liveEx := config.Exception{
		Rule:    "backlog.caps.defects",
		Tool:    "backlog",
		Reason:  "sprint refactor underway",
		Expires: unexpiredDate,
	}

	// Unexpired exception: passes and names the exception and expiry
	dirLive, policyLive := backlogFixtureRepo(t, []string{"backlog:bounded"}, []config.Exception{liveEx}, 21, 0)
	outLive, errLive := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dirLive, policyLive)
	})
	if errLive != nil {
		t.Fatalf("unexpired exception must pass, got error: %v\n%s", errLive, outLive)
	}
	mustContain(t, outLive, "defects: 21 of 20, OVER the cap", "excepted until "+unexpiredDate,
		"rule backlog.caps.defects", "sprint refactor underway", "[PASS] Backlog caps")

	// Expired exception: audit fails
	expiredEx := config.Exception{
		Rule:    "backlog.caps.defects",
		Tool:    "backlog",
		Reason:  "old sprint refactor",
		Expires: "2020-01-01",
	}
	dirExp, policyExp := backlogFixtureRepo(t, []string{"backlog:bounded"}, []config.Exception{expiredEx}, 21, 0)
	outExp, errExp := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dirExp, policyExp)
	})
	if errExp == nil {
		t.Fatalf("expired exception must fail, but passed\n%s", outExp)
	}
	mustErrContain(t, errExp, "backlog cap defects: 21 items, over the cap of 20")
	mustErrContain(t, errExp, "expired on 2020-01-01")
}

// TestAuditBacklogCaps_BoundedFacet_TasksBatchAction: facet listed, 60 open tasks: audit passes,
// the batch action is reported.
func TestAuditBacklogCaps_BoundedFacet_TasksBatchAction(t *testing.T) {
	dir, policy := backlogFixtureRepo(t, []string{"backlog:bounded"}, nil, 0, 60)
	out, err := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dir, policy)
	})
	if err != nil {
		t.Fatalf("batch action must not fail audit, got: %v\n%s", err, out)
	}
	mustContain(t, out, "tasks: 60 of 50, OVER the cap", "action batch", "[PASS] Backlog caps")
}

// TestAuditBacklogCaps_NoFacet_Uncapped: facet not listed, 21 defects: no cap applies (unchanged behaviour).
func TestAuditBacklogCaps_NoFacet_Uncapped(t *testing.T) {
	dir, policy := backlogFixtureRepo(t, nil, nil, 21, 0)
	out, err := captureStdout(t, func() error {
		return auditBacklogCaps(t.Context(), dir, policy)
	})
	if err != nil {
		t.Fatalf("uncapped repository must pass, got: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("uncapped repository should print no cap lines, got: %q", out)
	}
}

// TestAuditBacklogCaps_UnknownCategoryExceptionRefused: an exception for an unknown category
// (backlog.caps.bogus) is refused by the validator.
func TestAuditBacklogCaps_UnknownCategoryExceptionRefused(t *testing.T) {
	bogusEx := config.Exception{
		Rule:    "backlog.caps.bogus",
		Tool:    "backlog",
		Reason:  "testing unknown category rejection",
		Expires: time.Now().AddDate(0, 0, 10).Format(config.ExceptionDateLayout),
	}
	err := config.ValidateExceptions([]config.Exception{bogusEx}, time.Now())
	if err == nil {
		t.Fatal("expected ValidateExceptions to reject backlog.caps.bogus, but it accepted it")
	}
	if !strings.Contains(err.Error(), `rule "backlog.caps.bogus" is not a rule`) {
		t.Fatalf("expected error naming unknown rule, got: %v", err)
	}
}
