package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/changelog"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

func registerTestPlan() *VerificationPlan {
	return &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}
}

// The harness carries the default block, and that block is byte-for-byte what the
// adoptee's own compile-context renders without a manifest once it carries the register skills
// adoption installs (#235): a fresh adoption verifies.
func TestHarnessCarriesTheDefaultRegisterSection(t *testing.T) {
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", registerTestPlan()))
	if err != nil {
		t.Fatal(err)
	}
	for _, once := range []string{config.RegisterBlockHeading + "\n", config.RegisterBlockStart, config.RegisterBlockEnd, harnessFooterHeading, harnessEndMarker} {
		if strings.Count(harness, once) != 1 {
			t.Errorf("harness must contain %q exactly once", once)
		}
	}
	if strings.Index(harness, config.RegisterBlockEnd) > strings.Index(harness, harnessFooterHeading) {
		t.Error("the register section must precede the footer, so the footer stays the last section")
	}

	repo := t.TempDir()
	writeRegisterSkillSources(t, repo)
	agents := filepath.Join(repo, agentsFile)
	if err := os.WriteFile(agents, []byte(harness), filePerm); err != nil {
		t.Fatal(err)
	}
	if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
		t.Fatalf("a fresh harness must verify against the default policy: changed=%v err=%v", changed, err)
	}
}

func TestDropRegisterSection(t *testing.T) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ content, want string }{
		"no section":             {"# Rules\nKeep this.\n", "# Rules\nKeep this.\n"},
		"appended section":       {"# Rules\nKeep this.\n\n" + config.RegisterSectionPrefix + block + "\n", "# Rules\nKeep this."},
		"heading without a gap":  {"# Rules\n" + config.RegisterBlockHeading + "\n" + block + "\n\n## After\nAlso kept.\n", "# Rules\n\n## After\nAlso kept."},
		"markers without a head": {block + "\nKeep this.\n", "Keep this."},
		"only the section":       {config.RegisterSectionPrefix + block + "\n", ""},
		"unterminated marker":    {"# Rules\n" + config.RegisterBlockStart + "\nrest\n", "# Rules\n" + config.RegisterBlockStart + "\nrest\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := dropRegisterSection(tc.content); got != tc.want {
				t.Fatalf("dropRegisterSection = %q, want %q", got, tc.want)
			}
		})
	}
	if got := foreignInstructions("# Rules\nKeep this.\n"); got != "# Rules\nKeep this.\n" {
		t.Fatalf("instructions without a section must stay byte-identical, got %q", got)
	}
}

// compile-context appends the section to an AGENTS.md that has none, so instructions kept
// across a harness refresh can hold one. The refreshed file must carry exactly one.
func TestHarnessRefreshKeepsOneRegisterSection(t *testing.T) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	section := config.RegisterSectionPrefix + block + "\n"
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", registerTestPlan()))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		initial string
		force   bool
	}{
		"forced refresh":      {harness + harnessSeparator + "\nKeep project instructions.\n\n" + section, true},
		"foreign first merge": {"# Repository rules\nKeep project instructions.\n\n" + section, false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "harness-register")
			writeRegisterSkillSources(t, repo)
			path := filepath.Join(repo, agentsFile)
			if err := os.WriteFile(path, []byte(tc.initial), filePerm); err != nil {
				t.Fatal(err)
			}
			s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: registerTestPlan(), opts: AdoptOptions{Force: tc.force}}
			merged, err := mergeExistingAgentsContent(context.Background(), s, path, tc.initial, harness)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(merged, config.RegisterBlockStart) != 1 || !strings.Contains(merged, "Keep project instructions.") {
				t.Fatalf("merged context must keep the instructions and one register section:\n%s", merged)
			}
			if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, path, false); err != nil || changed {
				t.Fatalf("merged context must verify: changed=%v err=%v", changed, err)
			}
		})
	}
}

// TestAdoptForceHarnessCarriesManifestRegisterBlock: the harness carries the block
// compile-context splices from the manifest (compiler.LoadRegisterBlock), so an AGENTS.md that
// --force refreshes under a register.tasks override verifies without a compile-context run
// (#502, probe502b). Positive: the override row is in the refreshed file and verifies.
// Negative: the default block the old harness carried is gone. Boundary: without a manifest
// the harness still carries the default block and verifies.
func TestAdoptForceHarnessCarriesManifestRegisterBlock(t *testing.T) {
	const override = "version: 1\nregister:\n  tasks:\n    ci_debugging: {register: social, max_tokens: 256}\n"
	stale, err := buildAgentHarness(adoptedFacts("", "widget", "framework", registerTestPlan()))
	if err != nil {
		t.Fatal(err)
	}
	for name, manifest := range map[string]string{"register.tasks override": override, "no manifest": ""} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t, "widget")
			mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
			if manifest != "" {
				mustWrite(t, filepath.Join(repo, manifestFile), manifest)
			}
			agents := filepath.Join(repo, agentsFile)
			mustWrite(t, agents, stale+harnessSeparator+"\nKeep project instructions.\n")
			opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true}
			if _, err := Adopt(context.Background(), opts); err != nil {
				t.Fatalf("Adopt --force: %v", err)
			}
			if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
				t.Fatalf("refreshed register block does not verify: changed=%v err=%v", changed, err)
			}
			_, want, err := compiler.LoadRegisterBlock(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			content := mustRead(t, agents)
			if !strings.Contains(content, want) || !strings.Contains(content, "Keep project instructions.") {
				t.Fatalf("refreshed AGENTS.md must carry the manifest block and keep the tail:\n%s", content)
			}
			if overridden := strings.Contains(content, "ci_debugging"); overridden != (manifest != "") {
				t.Errorf("override row present = %v, want %v", overridden, manifest != "")
			}
		})
	}
}

// TestAdoptRegisterBlockFollowsDispatchHook: the harness says a hook denies a subagent brief
// without `task:` only where the repository registers the pre-dispatch hook, decided the way
// compile-context decides it, so the adopted AGENTS.md verifies either way (#504). Positive: a
// repository whose .claude/settings.json already registers it. Negative: a plain repository,
// where adoption registers only the pre-tool row. Boundary: both results pass compile-context's
// register block verification unchanged.
func TestAdoptRegisterBlockFollowsDispatchHook(t *testing.T) {
	const claim = "registered dispatch hook denies brief missing `task:`"
	adopt := func(settings string) string {
		repo := newTestRepo(t, "widget")
		mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
		if settings != "" {
			mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), settings)
		}
		if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"}); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
		agents := filepath.Join(repo, agentsFile)
		if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
			t.Fatalf("adopted register block does not verify: changed=%v err=%v", changed, err)
		}
		return mustRead(t, agents)
	}
	registered := `{"hooks": {"PreToolUse": [{"matcher": "^Agent$", "hooks": [{"type": "command", "command": "praetorctl hook claude pre-dispatch"}]}]}}`
	if content := adopt(registered); !strings.Contains(content, claim) {
		t.Errorf("registered dispatch hook not stated in the adopted harness")
	}
	if content := adopt(""); strings.Contains(content, claim) || !strings.Contains(content, "Subagent launch brief: `caveman` brief shape with `task:` = routing label.") {
		t.Errorf("plain adoption claims a dispatch hook or drops the brief rule")
	}
}

// staleRegisterHarness adopts a fresh repository, then edits one line inside the register block
// of the AGENTS.md it wrote, the state a manifest change leaves until compile-context runs, and
// returns the repository with the AGENTS.md path. eol is the line ending the edited file keeps.
func staleRegisterHarness(t *testing.T, name, eol string) (repo, agents string) {
	t.Helper()
	repo = newTestRepo(t, name)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("first Adopt: %v", err)
	}
	agents = filepath.Join(repo, agentsFile)
	stale := strings.Replace(mustRead(t, agents), config.RegisterBlockStart+"\n", config.RegisterBlockStart+"\nstale register row\n", 1)
	mustWrite(t, agents, strings.ReplaceAll(stale, "\n", eol))
	return repo, agents
}

// TestAdoptKeptHarnessSplicesRegisterBlock: a run without --force keeps the harness and splices
// its text register block from the manifest, as compile-context does, so the compile-context
// --verify after the chain passes instead of failing the run. Positive: a stale block is
// re-rendered through replaceExisting, as a forced refresh is: the report lists AGENTS.md as
// replaced, says the block was spliced, and the backup holds the bytes the run found. Boundary: a
// CRLF file keeps CRLF, and a block already in sync leaves the file byte-identical and reported
// as reconciled.
func TestAdoptKeptHarnessSplicesRegisterBlock(t *testing.T) {
	opts := func(repo string) AdoptOptions {
		return AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"}
	}
	for name, eol := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
		t.Run(name, func(t *testing.T) {
			repo, agents := staleRegisterHarness(t, "widget", eol)
			stale := mustRead(t, agents)
			rep, err := Adopt(context.Background(), opts(repo))
			if err != nil {
				t.Fatalf("plain Adopt: %v", err)
			}
			assertNoIssues(t, rep)
			content := mustRead(t, agents)
			if strings.Contains(content, "stale register row") || strings.Count(content, "\n") != strings.Count(content, eol) {
				t.Fatalf("stale block kept or line endings mixed:\n%q", content)
			}
			if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
				t.Fatalf("spliced block does not verify: changed=%v err=%v", changed, err)
			}
			detail := findActionDetail(rep.ActionDetails, agentsFile)
			if !hasAction(rep, agentsFile, actionReplace) || !strings.Contains(detail, "text register block spliced from the manifest") {
				t.Fatalf("AGENTS.md not reported as replaced by the splice: %q", detail)
			}
			if backup := mustRead(t, backupOfDetail(t, repo, agentsFile, detail)); backup != stale {
				t.Errorf("backup does not hold the bytes the run found:\n%q", backup)
			}
			rep, err = Adopt(context.Background(), opts(repo))
			if err != nil || mustRead(t, agents) != content {
				t.Fatalf("an in-sync block must leave AGENTS.md byte-identical: err=%v", err)
			}
			if detail := findActionDetail(rep.ActionDetails, agentsFile); strings.Contains(detail, "spliced") || hasAction(rep, agentsFile, actionReplace) {
				t.Errorf("an in-sync block reported as spliced: %q", detail)
			}
		})
	}
}

// Negative: the kept harness's splice is checked before the first step writes
// (preflightKeptHarness). A register block with no end marker, which compile-context fails on
// too, and a symlinked backup root the splice would back AGENTS.md up to each fail the run in the
// agent-harness preflight with the tree unchanged; the refusals used to come mid-chain, after the
// manifest, lock and catalog steps had written.
func TestAdoptKeptHarnessSpliceRefusedBeforeAnyWrite(t *testing.T) {
	repo, agents := staleRegisterHarness(t, "widget", "\n")
	mustWrite(t, agents, strings.Replace(mustRead(t, agents), config.RegisterBlockEnd, "", 1))
	before := snapshotTree(t, repo)
	_, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"})
	if err == nil || !strings.Contains(err.Error(), "agent-harness preflight") || !strings.Contains(err.Error(), agentsFile+": text register block") {
		t.Fatalf("want the unterminated register block refused in preflight naming %s, got %v", agentsFile, err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))

	repo, _ = staleRegisterHarness(t, "widget", "\n")
	shared := plantBackupRootLink(t, repo)
	before = snapshotTree(t, repo)
	_, err = Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"})
	if err == nil || !strings.Contains(err.Error(), "agent-harness preflight") || !strings.Contains(err.Error(), adoptBackupRoot) {
		t.Fatalf("want the symlinked backup root refused in preflight, got %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))
	assertDirEmpty(t, shared)
}

// keptHarnessWithoutSkills adopts a repository with agent-definitions declined while Claude Code is
// selected, which installs no register skill (planRegisterSkills), so the AGENTS.md it keeps
// carries a register block naming none, then stops declining the step: the next run installs the
// skills and splices their names into the kept block.
func keptHarnessWithoutSkills(t *testing.T) string {
	t.Helper()
	repo := newTestRepo(t, "kept-block-without-skills")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: [agent-definitions]\n")
	adoptWithSource(t, repo, newAdoptLockSource(t), false)
	if strings.Contains(mustRead(t, filepath.Join(repo, agentsFile)), "`caveman`") {
		t.Fatal("the first run named a register skill it did not install")
	}
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\n")
	return repo
}

// The preflight checks the register block the step splices, rendered with the skills the run
// installs (s.registerBlock), not the block the disk renders before they are installed. Positive:
// a kept block naming no skill gets the installed skills' names spliced in, and the repository
// verifies. Negative: that splice backs AGENTS.md up, so a symlinked backup root fails the run in
// the agent-harness preflight with the tree unchanged; checked against the disk alone, the
// preflight saw no splice, and the refusal came from the step after the manifest, the lock and
// the skills were written.
func TestAdoptKeptHarnessSpliceOfInstalledSkillsPreflighted(t *testing.T) {
	repo := keptHarnessWithoutSkills(t)
	rep := adoptWithSource(t, repo, newAdoptLockSource(t), false)
	agents := mustRead(t, filepath.Join(repo, agentsFile))
	if !strings.Contains(agents, "`caveman` skill:") || !hasAction(rep, agentsFile, actionReplace) {
		t.Fatalf("the installed skills were not spliced into the kept block: %q", findActionDetail(rep.ActionDetails, agentsFile))
	}
	verifyAdoptedContext(t, repo)

	repo = keptHarnessWithoutSkills(t)
	shared := plantBackupRootLink(t, repo)
	before := snapshotTree(t, repo)
	_, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo})
	if err == nil || !strings.Contains(err.Error(), "agent-harness preflight") || !strings.Contains(err.Error(), adoptBackupRoot) {
		t.Fatalf("want the symlinked backup root refused in the agent-harness preflight, got %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))
	assertDirEmpty(t, shared)
}

// TestAdoptRegisterBlockFollowsFragmentDirectory: the adopted harness names a changelog
// fragment only for a repository that keeps the fragment directory, decided the way
// compile-context decides it, so the adopted AGENTS.md verifies either way (#328). Positive: a
// repository with the directory. Negative: a repository without one carries none of the clauses
// the harness used to assert for every adopter. Boundary: an empty conventions.social key
// declines the clause although the directory exists.
func TestAdoptRegisterBlockFollowsFragmentDirectory(t *testing.T) {
	adopt := func(withDir bool, manifest string) string {
		repo := newTestRepo(t, "widget")
		mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
		if withDir {
			mustWrite(t, filepath.Join(repo, changelog.FragmentDir, "0001-entry.yaml"), "type: fixed\ntitle: Fix a defect\n")
		}
		if manifest != "" {
			mustWrite(t, filepath.Join(repo, manifestFile), manifest)
		}
		if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework"}); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
		agents := filepath.Join(repo, agentsFile)
		if changed, err := compiler.SyncRegisterBlock(context.Background(), repo, agents, false); err != nil || changed {
			t.Fatalf("adopted register block does not verify: changed=%v err=%v", changed, err)
		}
		return mustRead(t, agents)
	}
	const clause = "conventional commit subject unchanged; " + config.FragmentConvention + " |"
	if content := adopt(true, ""); !strings.Contains(content, clause) {
		t.Errorf("fragment directory not reflected in the adopted harness")
	}
	plain := adopt(false, "")
	for _, word := range []string{"changelog fragment", "receipt fence", "PR template"} {
		if strings.Contains(plain, word) {
			t.Errorf("adopted harness without a fragment directory carries %q", word)
		}
	}
	if content := adopt(true, "version: 1\nregister:\n  conventions:\n    social: \"\"\n"); strings.Contains(content, "changelog fragment") {
		t.Errorf("an empty conventions.social key must decline the detected clause")
	}
}
