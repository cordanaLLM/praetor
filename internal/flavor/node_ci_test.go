package flavor_test

// The scaffolded Node CI job must pass as written wherever flavor apply writes it. Adoption
// makes every job of a scaffolded workflow a required status check, and typescript-node
// detects any package.json: a pnpm project, or a Go repository carrying a package.json for its
// commit tooling, used to receive `npm ci` + `npm test` as a check no pull request could pass.
// The job runs on CI's checkout, not the working tree, so a lockfile Git never commits counts
// as no lockfile.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/templates"
	"gopkg.in/yaml.v3"
)

const nodeCIPath = ".github/workflows/ci.yml"

// npmLock is a package-lock.json npm ci accepts for a package with no dependencies.
const npmLock = `{"name": "widget", "lockfileVersion": 3, "requires": true, "packages": {"": {"name": "widget"}}}` + "\n"

// setupNodeLockfiles are the files setup-node's `cache: npm` looks for in the repository root;
// with none of them the action fails the job (actions/setup-node src/cache-utils.ts,
// findLockFile in src/cache-restore.ts).
var setupNodeLockfiles = []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock"}

// nodeWorkflow is the part of a workflow the runnability check reads.
type nodeWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Uses string            `yaml:"uses"`
			With map[string]string `yaml:"with"`
			Run  string            `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// unrunnableNodeSteps returns every step of a Node workflow that cannot pass on CI's checkout of
// repo: an action whose inputs the checkout does not satisfy, a command naming a lockfile or
// script that is not there, and any action or command the check does not model, so a later
// body cannot add one unchecked.
func unrunnableNodeSteps(t *testing.T, body, repo string) []string {
	t.Helper()
	var workflow nodeWorkflow
	if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
		t.Fatalf("parse workflow: %v\n%s", err, body)
	}
	checkout := checkoutFiles(t, repo)
	scripts := packageScripts(t, repo)
	var problems []string
	for _, job := range workflow.Jobs {
		for i := 0; i < len(job.Steps) && i < maxWorkflowSteps; i++ {
			step := job.Steps[i]
			if step.Uses != "" {
				problems = append(problems, unrunnableAction(checkout, step.Uses, step.With)...)
			}
			problems = append(problems, unrunnableCommands(checkout, step.Run, scripts)...)
		}
	}
	return problems
}

// maxCheckoutFiles bounds the files read from one fixture's index (HISS-02).
const maxCheckoutFiles = 4096

// checkoutFiles returns the slash paths CI's checkout of repo would hold once its working tree
// is committed: what `git add -A` stages. Git leaves out an untracked file the repository's
// ignore rules exclude and keeps one already tracked, whatever the rules say. The model is
// Git's own staging, not the check-ignore query npmCIRequirement makes, so the two cannot share
// a mistake about which files a checkout carries.
func checkoutFiles(t *testing.T, repo string) map[string]bool {
	t.Helper()
	runFixtureGit(t, repo, "add", "-A")
	names := strings.Split(string(runFixtureGit(t, repo, "ls-files", "-z")), "\x00")
	files := make(map[string]bool, len(names))
	for i := 0; i < len(names) && i < maxCheckoutFiles; i++ {
		if names[i] != "" {
			files[names[i]] = true
		}
	}
	return files
}

func unrunnableAction(checkout map[string]bool, uses string, with map[string]string) []string {
	switch {
	case strings.HasPrefix(uses, "actions/checkout@"):
		return nil
	case strings.HasPrefix(uses, "actions/setup-node@"):
		if with["cache"] == "npm" && !slices.ContainsFunc(setupNodeLockfiles, func(name string) bool { return checkout[name] }) {
			return []string{uses + ": cache: npm finds no lockfile"}
		}
		return nil
	default:
		return []string{uses + ": action not modelled by this check"}
	}
}

// maxRunLines bounds the lines read from one run: body (HISS-02).
const maxRunLines = 64

func unrunnableCommands(checkout map[string]bool, run string, scripts map[string]string) []string {
	var problems []string
	lines := strings.Split(run, "\n")
	for i := 0; i < len(lines) && i < maxRunLines; i++ {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if problem := unrunnableCommand(checkout, fields, scripts); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

// unrunnableCommand checks one command line against what npm needs to run it: `npm ci` reads
// package-lock.json (npm 12 reads no npm-shrinkwrap.json), `npm test` and `npm run <name>` fail
// on a script that is missing or cannot pass, and `--if-present` makes an absent script a no-op.
func unrunnableCommand(checkout map[string]bool, fields []string, scripts map[string]string) string {
	command := strings.Join(fields, " ")
	switch {
	case command == "npm ci":
		if !checkout["package-lock.json"] {
			return command + ": no package-lock.json in the checkout"
		}
	case command == "npm test":
		if !nodemanifest.ScriptRuns(scripts["test"]) {
			return command + ": no test script that can pass"
		}
	case len(fields) == 4 && fields[0] == "npm" && fields[1] == "run" && fields[3] == "--if-present":
		return ""
	case len(fields) == 3 && fields[0] == "npm" && fields[1] == "run":
		if !nodemanifest.ScriptRuns(scripts[fields[2]]) {
			return command + ": no such script"
		}
	default:
		return command + ": command not modelled by this check"
	}
	return ""
}

func packageScripts(t *testing.T, repo string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	// Exact keys, as npm reads them: a tagged struct field would also take "Scripts".
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("parse package.json: %v", err)
	}
	var scripts map[string]string
	if raw, ok := fields["scripts"]; ok {
		if err := json.Unmarshal(raw, &scripts); err != nil {
			t.Fatalf("parse package.json scripts: %v", err)
		}
	}
	return scripts
}

// nodeCICase is one repository shape and whether flavor apply may scaffold the Node CI job there.
type nodeCICase struct {
	name  string
	files map[string]string
	// track lists files Git tracks, force-added past any ignore rule; noGit leaves the fixture
	// outside a Git work tree.
	track []string
	noGit bool
	// flavor is what apply is asked for; "auto" exercises detection as adoption does.
	flavor string
	// want is whether the job is scaffolded; unmet is a fragment of the reason it was not.
	want  bool
	unmet string
}

// repo builds the case's fixture.
func (tc nodeCICase) repo(t *testing.T) string {
	t.Helper()
	if tc.noGit {
		return repoWithFiles(t, tc.files)
	}
	repo := gitRepoWithFiles(t, tc.files)
	if len(tc.track) > 0 {
		runFixtureGit(t, repo, append([]string{"add", "-f", "--"}, tc.track...)...)
	}
	return repo
}

const commitTooling = `{"name": "widget-tooling", "private": true, "devDependencies": {"@commitlint/cli": "^20.0.0"}}` + "\n"

func nodeCICases() []nodeCICase {
	withTest := `{"name": "widget", "scripts": {"test": "node --test", "lint": "eslint ."}}` + "\n"
	ignoresLock := "node_modules/\npackage-lock.json\n"
	return []nodeCICase{
		// Positive: an npm project with a committed lockfile and a test script.
		{name: "npm-project", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock}, track: []string{"package.json", "package-lock.json"}, flavor: "typescript-node", want: true},
		// Boundary: a lockfile not yet committed that nothing ignores goes into the next commit.
		{name: "npm-project-uncommitted-lockfile", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock}, flavor: "typescript-node", want: true},
		// Negative: the review's library, which ignores package-lock.json while a local
		// `npm install` has written one. CI's checkout has no lockfile.
		{name: "ignored-lockfile", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock, ".gitignore": ignoresLock}, flavor: "typescript-node", unmet: "untracked and git-ignored"},
		// Negative: a glob pattern excludes the lockfile just the same.
		{name: "ignored-by-glob", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock, ".gitignore": "*-lock.json\n"}, flavor: "auto", unmet: "untracked and git-ignored"},
		// Boundary: a lockfile force-added past the ignore rule is tracked, so the checkout has it.
		{name: "ignored-but-tracked-lockfile", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock, ".gitignore": ignoresLock}, track: []string{"package-lock.json"}, flavor: "typescript-node", want: true},
		// Boundary: outside a Git work tree nothing says what CI checks out, so the job is withheld.
		{name: "not-a-git-work-tree", files: map[string]string{"package.json": withTest, "package-lock.json": npmLock}, noGit: true, flavor: "typescript-node", unmet: "cannot tell whether Git commits package-lock.json"},
		// Boundary: pinning npm itself through packageManager is still npm.
		{name: "npm-pinned", files: map[string]string{"package.json": `{"packageManager": "npm@11.15.0", "scripts": {"test": "vitest run"}}`, "package-lock.json": npmLock}, flavor: "typescript-node", want: true},
		// Negative: the review's case, a pnpm project with no npm lockfile.
		{name: "pnpm-lock-only", files: map[string]string{"package.json": withTest, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n"}, flavor: "typescript-node", unmet: "no package-lock.json"},
		// Negative: the review's probe, a Go service whose package.json only holds commit tooling.
		// Detection names typescript-node (it precedes the Go flavors); the job must not follow.
		{name: "go-repo-with-commit-tooling", files: map[string]string{
			"go.mod": "module example.com/widget\n\ngo 1.27\n", "cmd/widget/main.go": "package main\n\nfunc main() {}\n",
			"internal/w/w.go": "package w\n", "package.json": commitTooling, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
		}, flavor: "auto", unmet: "no package-lock.json"},
		// Negative: the same tooling installed with npm keeps the placeholder test `npm init` writes.
		{name: "npm-init-placeholder", files: map[string]string{
			"package.json":      `{"name": "t", "scripts": {"test": "echo \"Error: no test specified\" && exit 1"}}`,
			"package-lock.json": npmLock,
		}, flavor: "typescript-node", unmet: "no test script"},
		{name: "no-test-script", files: map[string]string{"package.json": commitTooling, "package-lock.json": npmLock}, flavor: "typescript-node", unmet: "no test script"},
		// Negative: a declared manager other than npm is not overridden by a stray npm lockfile.
		{name: "yarn-declared", files: map[string]string{"package.json": `{"packageManager": "yarn@4.9.1", "scripts": {"test": "jest"}}`, "package-lock.json": npmLock}, flavor: "typescript-node", unmet: `packageManager "yarn@4.9.1"`},
		// Boundary: npm-shrinkwrap.json satisfies setup-node but not npm 12's `npm ci`.
		{name: "shrinkwrap-only", files: map[string]string{"package.json": withTest, "npm-shrinkwrap.json": npmLock}, flavor: "typescript-node", unmet: "no package-lock.json"},
		// Boundary: an explicit apply to a repository with no package.json at all.
		{name: "no-package-json", files: map[string]string{}, flavor: "typescript-node", unmet: "package.json is unreadable"},
		// Boundary: npm reads "scripts" exactly; a "Scripts" key is not the test script.
		{name: "case-folded-scripts", files: map[string]string{"package.json": `{"Scripts": {"test": "node --test"}}`, "package-lock.json": npmLock}, flavor: "typescript-node", unmet: "no test script"},
	}
}

// The scaffolded job is written only where each of its steps can pass, and every step of the
// body it writes is checked against the fixture: every lockfile and script it names exists.
func TestNodeCIJobIsScaffoldedOnlyWhereItRunsAsWritten(t *testing.T) {
	for _, tc := range nodeCICases() {
		t.Run(tc.name, func(t *testing.T) {
			repo := tc.repo(t)
			if tc.flavor == "auto" {
				if got, _ := flavor.Detect(repo); got != "typescript-node" {
					t.Fatalf("fixture detects %q, not typescript-node; the case no longer tests the job", got)
				}
			}
			report, err := flavor.ApplyFlavor(t.Context(), repo, tc.flavor, false)
			if err != nil || len(report.Errors) > 0 {
				t.Fatalf("apply: err %v, recorded %v", err, report.Errors)
			}
			data, readErr := os.ReadFile(filepath.Join(repo, nodeCIPath))
			if written := readErr == nil; written != tc.want {
				t.Fatalf("CI job scaffolded = %v, want %v; report %+v", written, tc.want, report)
			}
			if tc.want {
				if problems := unrunnableNodeSteps(t, string(data), repo); len(problems) > 0 {
					t.Fatalf("the scaffolded job cannot pass here: %v", problems)
				}
				return
			}
			assertUnmet(t, report, tc.unmet)
		})
	}
}

func assertUnmet(t *testing.T, report *flavor.ApplyReport, fragment string) {
	t.Helper()
	for _, entry := range report.UnmetTemplates {
		if strings.HasPrefix(entry, nodeCIPath+": ") && strings.Contains(entry, fragment) {
			return
		}
	}
	t.Fatalf("want %s reported unmet with %q, got %v", nodeCIPath, fragment, report.UnmetTemplates)
}

// The check above is only as good as its ability to fail. Replayed against the job as the
// template renders it, it flags the pnpm project, the placeholder test and the ignored lockfile
// (the body flavor apply used to write there) and passes the npm project.
func TestUnrunnableNodeStepsCatchesTheJobWhereItCannotPass(t *testing.T) {
	body, err := templates.RenderFile("node/ci-node.yml.tmpl", templates.Context{RepoName: "widget", Owner: "acme"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	cases := nodeCICases()
	for _, name := range []string{"pnpm-lock-only", "npm-init-placeholder", "shrinkwrap-only", "ignored-lockfile"} {
		i := slices.IndexFunc(cases, func(tc nodeCICase) bool { return tc.name == name })
		if i < 0 {
			t.Fatalf("no case named %s", name)
		}
		if problems := unrunnableNodeSteps(t, body, cases[i].repo(t)); len(problems) == 0 {
			t.Errorf("%s: the check passed a job that fails there", name)
		}
	}
	if problems := unrunnableNodeSteps(t, body, cases[0].repo(t)); len(problems) > 0 {
		t.Errorf("npm-project: the check rejected a job that passes: %v", problems)
	}
	// Boundary: a command the check does not model is a finding, not a pass.
	unknown := "jobs:\n  test:\n    steps:\n      - run: pnpm install --frozen-lockfile\n"
	if problems := unrunnableNodeSteps(t, unknown, cases[0].repo(t)); len(problems) != 1 {
		t.Errorf("an unmodelled command passed the check: %v", problems)
	}
}

// Boundary: --force replaces existing files, never with a job that cannot pass. An existing
// workflow in a pnpm project stays exactly as it was.
func TestApplyFlavor_Boundary_ForceNeverWritesAnUnrunnableNodeJob(t *testing.T) {
	own := "name: CI\non: push\njobs:\n  test:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: pnpm test\n"
	files := map[string]string{"package.json": `{"scripts": {"test": "vitest"}}`, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n", nodeCIPath: own}
	repo := gitRepoWithFiles(t, files)
	report, err := flavor.ApplyFlavor(t.Context(), repo, "typescript-node", true)
	if err != nil || len(report.Errors) > 0 {
		t.Fatalf("apply --force: err %v, recorded %v", err, report.Errors)
	}
	data, err := os.ReadFile(filepath.Join(repo, nodeCIPath))
	if err != nil || string(data) != own {
		t.Fatalf("--force replaced the repository's workflow: err %v\n%s", err, data)
	}
	assertUnmet(t, report, "no package-lock.json")
}
