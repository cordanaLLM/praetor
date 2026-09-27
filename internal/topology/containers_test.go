package topology

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestOrgContainers_UnsetIsBuiltinOnly: with no topology.org_containers the set is exactly
// the built-in names; the engine names no operator organization.
func TestOrgContainers_UnsetIsBuiltinOnly(t *testing.T) {
	for name, configured := range map[string][]string{"nil": nil, "empty": {}, "blank": {"", "  "}} {
		set := OrgContainers(configured)
		if len(set) != len(BuiltinOrgContainers) {
			t.Fatalf("%s: OrgContainers = %v, want only %v", name, set, BuiltinOrgContainers)
		}
		for _, builtin := range BuiltinOrgContainers {
			if !set[builtin] {
				t.Fatalf("%s: built-in container %q missing from %v", name, builtin, set)
			}
		}
	}
}

// TestOrgContainers_ConfiguredNamesAreLowercased: configured names join the built-in ones,
// compared lowercased and trimmed.
func TestOrgContainers_ConfiguredNamesAreLowercased(t *testing.T) {
	set := OrgContainers([]string{"ACME-Labs", " acme ", "local"})
	for _, want := range []string{"acme-labs", "acme", "local", "scratch"} {
		if !set[want] {
			t.Fatalf("OrgContainers lacks %q: %v", want, set)
		}
	}
	if set["ACME-Labs"] || len(set) != len(BuiltinOrgContainers)+2 {
		t.Fatalf("OrgContainers = %v, want lowercased names and no duplicate of a built-in", set)
	}
}

// TestOrgContainers_BoundaryMaxConfigured: the 64 names internal/config admits all land in
// the set.
func TestOrgContainers_BoundaryMaxConfigured(t *testing.T) {
	configured := make([]string, 64)
	for i := range configured {
		configured[i] = fmt.Sprintf("acme-%02d", i)
	}
	set := OrgContainers(configured)
	if len(set) != len(BuiltinOrgContainers)+64 || !set["acme-00"] || !set["acme-63"] {
		t.Fatalf("OrgContainers holds %d names, want %d", len(set), len(BuiltinOrgContainers)+64)
	}
}

// TestAuditWorkstationTopology_ConfiguredContainerRecognised: a folder with no child
// repository is a container once configured, matched case-insensitively; so is a built-in
// name in any case without configuration. Unconfigured, the same folder is plain content.
func TestAuditWorkstationTopology_ConfiguredContainerRecognised(t *testing.T) {
	devRoot := t.TempDir()
	stray := filepath.Join(devRoot, "Acme-Labs", ".standards.yaml")
	writeTestFile(t, stray)
	builtinStray := filepath.Join(devRoot, "LOCAL", ".standards.yaml")
	writeTestFile(t, builtinStray)

	report, err := AuditWorkstationTopology(context.Background(), devRoot, []string{"ACME-labs"})
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "OrgContainers", report.OrgContainers, []string{"Acme-Labs", "LOCAL"})
	requireSafeStray(t, report, stray)
	requireSafeStray(t, report, builtinStray)

	unconfigured, err := AuditWorkstationTopology(context.Background(), devRoot, nil)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "OrgContainers", unconfigured.OrgContainers, []string{"LOCAL"})
	if _, found := findStray(unconfigured, stray); found {
		t.Fatalf("an unconfigured folder without a child repository was audited as a container: %+v", unconfigured.StrayFiles)
	}
}

// TestAuditWorkstationTopology_StructuralContainerNeedsNoConfiguration: a folder that is
// not a repository and holds exactly one child repository is a container with no
// configuration at all.
func TestAuditWorkstationTopology_StructuralContainerNeedsNoConfiguration(t *testing.T) {
	devRoot := t.TempDir()
	initTestGit(t, filepath.Join(devRoot, "acme-labs", "app"))
	stray := filepath.Join(devRoot, "acme-labs", "CLAUDE.md")
	writeTestFile(t, stray)

	report, err := AuditWorkstationTopology(context.Background(), devRoot, nil)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "OrgContainers", report.OrgContainers, []string{"acme-labs"})
	assertStringSet(t, "ValidRepos", report.ValidRepos, []string{filepath.Join("acme-labs", "app")})
	assertStringSet(t, "Violations", report.Violations, nil)
	requireSafeStray(t, report, stray)
}

// TestAuditWorkstationTopology_Negative_PlainFoldersAreNoContainers: an empty folder, a
// folder of files and a folder of folders without git metadata are neither containers nor
// violations when unconfigured.
func TestAuditWorkstationTopology_Negative_PlainFoldersAreNoContainers(t *testing.T) {
	devRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(devRoot, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(devRoot, "acme-labs", "CLAUDE.md"))
	writeTestFile(t, filepath.Join(devRoot, "notes", "drafts", "todo.txt"))

	report, err := AuditWorkstationTopology(context.Background(), devRoot, nil)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "OrgContainers", report.OrgContainers, nil)
	assertStringSet(t, "Violations", report.Violations, nil)
	if len(report.StrayFiles) != 0 || report.Truncated {
		t.Fatalf("plain folders produced strays %v or truncation %v", report.StrayFiles, report.TruncationReasons)
	}
}

// TestAuditWorkstationTopology_StructuralScanBound: structural detection inspects at most
// MaxScanEntries entries; a child repository beyond the bound leaves the folder unclassified
// and the audit truncated rather than silently not a container.
func TestAuditWorkstationTopology_StructuralScanBound(t *testing.T) {
	cases := []struct {
		name      string
		fillers   int
		container bool
	}{
		{name: "repository is entry MaxScanEntries", fillers: MaxScanEntries - 1, container: true},
		{name: "repository is entry MaxScanEntries+1", fillers: MaxScanEntries, container: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devRoot := t.TempDir()
			folder := filepath.Join(devRoot, "acme")
			writeFillerEntries(t, folder, tc.fillers)
			initTestGit(t, filepath.Join(folder, "zz-repo"))

			report, err := AuditWorkstationTopology(context.Background(), devRoot, nil)
			if err != nil {
				t.Fatalf("AuditWorkstationTopology: %v", err)
			}
			if got := slices.Contains(report.OrgContainers, "acme"); got != tc.container {
				t.Fatalf("container = %v, want %v (reasons %v)", got, tc.container, report.TruncationReasons)
			}
			reasons := strings.Join(report.TruncationReasons, "\n")
			if tc.container == strings.Contains(reasons, "acme: organization container detection incomplete") {
				t.Fatalf("truncation reasons = %q", reasons)
			}
		})
	}
}

// TestAuditWorkstationTopology_StructuralScanUnreadableIsTruncation: a folder whose listing
// cannot be read is unclassified, so the audit is truncated and cleanup refuses.
func TestAuditWorkstationTopology_StructuralScanUnreadableIsTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not implement POSIX chmod permission denial")
	}
	devRoot := t.TempDir()
	folder := filepath.Join(devRoot, "acme")
	initTestGit(t, filepath.Join(folder, "app"))
	// Search without read: .git can be looked up, the listing cannot be read.
	if err := os.Chmod(folder, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(folder, 0o755); err != nil {
			t.Errorf("restore permissions: %v", err)
		}
	})
	if _, err := os.ReadDir(folder); err == nil {
		t.Skip("current user can list search-only directories")
	}

	report, err := AuditWorkstationTopology(context.Background(), devRoot, nil)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	if !report.Truncated || !strings.Contains(strings.Join(report.TruncationReasons, "\n"),
		"acme: organization container detection incomplete") {
		t.Fatalf("truncation = %v %v, want the unreadable folder", report.Truncated, report.TruncationReasons)
	}
	if _, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, nil, false); !errors.Is(err, ErrScanTruncated) {
		t.Fatalf("clean err = %v, want ErrScanTruncated", err)
	}
}

// TestVerifyDeletionSafety_RefusesDirectoryHoldingRepository: the final boundary refuses a
// directory that holds a repository whatever its name or classification, including
// proven-headless git metadata with a repository nested inside.
func TestVerifyDeletionSafety_RefusesDirectoryHoldingRepository(t *testing.T) {
	devRoot := t.TempDir()
	structural := filepath.Join(devRoot, "acme-labs")
	initTestGit(t, filepath.Join(structural, "app"))
	headless := filepath.Join(devRoot, "acme", ".git")
	initTestGit(t, filepath.Join(headless, "nested"))

	for _, candidate := range []string{structural, headless} {
		err := verifyDeletionSafety(context.Background(), devRoot, candidate, OrgContainers(nil))
		if err == nil || !strings.Contains(err.Error(), "holds a git repository") {
			t.Errorf("verifyDeletionSafety(%s) = %v, want a repository-holding refusal", candidate, err)
		}
	}
}

// TestCleanWorkstationTopologyDetailed_KeepsHeadlessMetadataHoldingRepository: headless git
// metadata is a safe finding, but cleanup refuses it once it holds a repository.
func TestCleanWorkstationTopologyDetailed_KeepsHeadlessMetadataHoldingRepository(t *testing.T) {
	devRoot := t.TempDir()
	container := filepath.Join(devRoot, "acme-labs")
	initTestGit(t, filepath.Join(container, "app"))
	nested := filepath.Join(container, ".git", "nested")
	initTestGit(t, nested)

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, nil, false)
	var safetyErr *deletionSafetyError
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "holds a git repository") {
		t.Fatalf("clean err = %v, want the repository-holding refusal", err)
	}
	if result == nil || len(result.Cleaned) != 0 {
		t.Fatalf("result = %+v, want no removals", result)
	}
	assertPathsExist(t, []string{filepath.Join(nested, ".git", "HEAD")})
}

// TestHoldsChildRepository pins the structural test itself: a directory or gitlink child
// repository counts; a headless child, a plain child and a file do not; a missing directory
// and an ended context are errors, never a false "no repository".
func TestHoldsChildRepository(t *testing.T) {
	root := t.TempDir()
	holder := filepath.Join(root, "holder")
	initTestGit(t, filepath.Join(holder, "app"))
	linked := filepath.Join(root, "linked")
	writeGitlink(t, filepath.Join(linked, "worktree"), "gitdir: "+filepath.Join(holder, "app", ".git")+"\n")
	plain := filepath.Join(root, "plain")
	writeTestFile(t, filepath.Join(plain, "docs", "README.md"))
	writeTestFile(t, filepath.Join(plain, "repo"))
	if err := os.MkdirAll(filepath.Join(plain, "headless", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dir  string
		want bool
	}{{holder, true}, {linked, true}, {plain, false}} {
		if got, err := HoldsChildRepository(context.Background(), tc.dir); err != nil || got != tc.want {
			t.Errorf("HoldsChildRepository(%s) = %v, %v; want %v", tc.dir, got, err, tc.want)
		}
	}
	if got, err := HoldsChildRepository(context.Background(), filepath.Join(root, "absent")); err == nil || got {
		t.Errorf("missing directory = %v, %v; want an error", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := HoldsChildRepository(ctx, holder); !errors.Is(err, context.Canceled) || got {
		t.Errorf("cancelled scan = %v, %v; want context.Canceled", got, err)
	}
}
