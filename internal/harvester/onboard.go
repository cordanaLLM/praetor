package harvester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrNotADirectory is returned when an onboarding target exists but is not a directory.
var ErrNotADirectory = errors.New("harvester: onboarding target is not a directory")

// ErrOnboardingIncomplete identifies a scaffold whose dependency lock is missing or
// invalid. A valid lock without a local catalog is reported through LockStatus instead.
var ErrOnboardingIncomplete = errors.New("harvester: onboarding scaffold requires a valid dependency lock")

// ErrEditorFilesIncomplete identifies a run that wrote the scaffold and agent harness but not
// every editor file. Onboarding resolves editor files before its first write, so only a
// failure while publishing them, such as a file edited after it was resolved, ends here.
var ErrEditorFilesIncomplete = errors.New("harvester: onboarding wrote the scaffold but not its editor files")

// onboardFilePerm is the mode of every file onboarding scaffolds into a repository.
const (
	onboardFilePerm         os.FileMode = util.TrackedFilePerm
	maxOnboardDocumentBytes             = 8 * 1024 * 1024
	maxOnboardOutputs                   = 50
)

// OnboardPlan captures planned or applied onboarding actions for a repository.
// LockVerified is true only when every pinned content digest was hashed against the
// repository's catalog; LockStatus names the outcome of a live run. EditorFiles lists what a
// live run decided for each editor file (created, merged, already present, rewritten or
// preserved), as `editors generate` reports it (editor.WriteReport); a dry run lists none.
type OnboardPlan struct {
	RepoPath     string               `json:"repo_path"`
	Archetype    string               `json:"archetype"`
	Facets       []string             `json:"facets"`
	Actions      []string             `json:"actions"`
	DryRun       bool                 `json:"dry_run"`
	LockVerified bool                 `json:"lock_verified"`
	LockStatus   config.LockStatus    `json:"lock_status,omitempty"`
	EditorFiles  []editor.WriteResult `json:"editor_files,omitempty"`
}

// OnboardRepository scaffolds standards governance and agent harnesses into a repo.
func OnboardRepository(ctx context.Context, repoPath string, dryRun bool) (*OnboardPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before onboarding: %w", err)
	}

	info, err := os.Stat(repoPath)
	if err != nil {
		return nil, fmt.Errorf("invalid repository path %q: %w", repoPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrNotADirectory, repoPath)
	}

	inputs, err := readOnboardInputs(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	repoName := filepath.Base(repoPath)
	arch := detectRepoArchetype(repoPath)
	facets := config.DefaultFacets()

	plan := &OnboardPlan{
		RepoPath:  repoPath,
		Archetype: arch,
		Facets:    facets,
		Actions:   make([]string, 0),
		DryRun:    dryRun,
	}

	plan.Actions = append(plan.Actions, fmt.Sprintf("Scaffold .standards.yaml (Profile: %s, Facets: %v)", arch, facets))
	plan.Actions = append(plan.Actions, "Initialize .standards-baseline.json (0 infractions)")
	plan.Actions = append(plan.Actions, "Verify existing .standards.lock pins and digests (source pin resolution is required if absent)")
	plan.Actions = append(plan.Actions, inputs.transpile)
	plan.Actions = append(plan.Actions, "Generate IDE settings (.vscode, .idea, .nvim.lua)")

	if dryRun {
		return plan, nil
	}

	editorFiles, err := executeOnboarding(ctx, repoPath, repoName, arch, facets, inputs.editors)
	if err != nil {
		return plan, err
	}
	plan.EditorFiles = editorFiles
	lock, err := verifyOnboardLock(ctx, repoPath)
	if err != nil {
		return plan, fmt.Errorf("%w; scaffold files have been written: %w", ErrOnboardingIncomplete, err)
	}
	plan.LockStatus, plan.LockVerified = lock.Status, lock.Verified()
	return plan, nil
}

// onboardInputs is what onboarding reads and decides before its first write.
type onboardInputs struct {
	// transpile is the plan's compile-context action under the agent_clients selection.
	transpile string
	// editors are the selected editors' files, resolved and not yet written; nil when the
	// selection holds no editor.
	editors *editor.PreparedWrites
}

// readOnboardInputs reads the manifest's editors and agent_clients selection and resolves the
// editor files before onboarding writes anything, dry run included. The plan names the vendor
// files the run will write, so it reads the same agent_clients selection writeAgentHarness
// compiles with (#202). A refused editor file therefore fails the run with nothing written and
// a dry run predicts it: the editor step used to run last, so a refusal left the scaffold and
// harness behind and `harvest onboard --all-missing` then skipped the half-onboarded
// repository because AGENTS.md existed (#717).
func readOnboardInputs(ctx context.Context, repoPath string) (onboardInputs, error) {
	declared, err := config.LoadDeclaredTooling(ctx, repoPath)
	if err != nil {
		return onboardInputs{}, fmt.Errorf("read editors and agent_clients selection: %w", err)
	}
	transpile, err := transpileAction(declared.AgentClients)
	if err != nil {
		return onboardInputs{}, fmt.Errorf("agent_clients in .standards.yaml: %w", err)
	}
	editors, err := prepareOnboardEditors(ctx, repoPath, declared.Editors)
	if err != nil {
		return onboardInputs{}, err
	}
	return onboardInputs{transpile: transpile, editors: editors}, nil
}

// transpileAction names the vendor files compile-context writes under the manifest's
// agent_clients selection, from the transpiler's own registry. The hand-kept plan text omitted
// Codex (BUG-840), and a list that ignored the selection named files the run never writes.
func transpileAction(clients []string) (string, error) {
	targets, err := agentcontext.VendorTargets(clients)
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		return "Transpile AGENTS.md -> no vendor file (agent_clients selects none)", nil
	}
	sections := make([]string, 0, len(targets))
	for _, target := range targets {
		sections = append(sections, target.Section)
	}
	return "Transpile AGENTS.md -> " + strings.Join(sections, ", "), nil
}

// archetypeMarkers maps a repository marker file to the archetype it implies.
var archetypeMarkers = []struct {
	file      string
	archetype string
}{
	{"go.mod", "framework"},
	{"Cargo.toml", "native-gpu-systems"},
	{"pubspec.yaml", "app-service"},
	{"pom.xml", "app-service"},
	{"build.gradle", "app-service"},
	{"package.json", "app-service"},
	{"pyproject.toml", "app-service"},
}

// detectRepoArchetype infers the archetype from the build manifests present in the repo.
func detectRepoArchetype(repoPath string) string {
	for _, marker := range archetypeMarkers {
		if util.FileExists(filepath.Join(repoPath, marker.file)) {
			return marker.archetype
		}
	}
	return "template-seed"
}

// executeOnboarding writes the governance scaffold, the compiled agent harnesses and the
// editor files resolved before it (readOnboardInputs), and returns the outcome of each editor
// file. Every step propagates its failure: the returned plan claims these actions were
// performed, so a swallowed transpile or editor error would make the plan a lie.
func executeOnboarding(ctx context.Context, repoPath, repoName, arch string, facets []string, editors *editor.PreparedWrites) ([]editor.WriteResult, error) {
	if err := ensureOnboardingManifest(ctx, repoPath, repoName, arch, facets); err != nil {
		return nil, err
	}
	if err := ensureOnboardingBaseline(ctx, repoPath); err != nil {
		return nil, err
	}
	if err := ensureOnboardingHarness(ctx, repoPath, repoName); err != nil {
		return nil, err
	}
	if err := writeAgentHarness(ctx, repoPath); err != nil {
		return nil, err
	}
	return publishOnboardEditors(ctx, repoPath, editors)
}

// writeAgentHarness writes generated outputs with the caller's context and confinement. The
// compiler convenience writer does not accept that context.
func writeAgentHarness(ctx context.Context, repoPath string) error {
	agentsPath, err := util.ConfinePath(repoPath, "AGENTS.md")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The scaffolded harness tells its reader to run compile-context --verify, which requires
	// the text register block; splice it first so that instruction holds from the start.
	if _, err := compiler.SyncRegisterBlock(ctx, repoPath, agentsPath, true); err != nil {
		return fmt.Errorf("splice text register into %s: %w", agentsPath, err)
	}
	// An existing manifest may select agent clients (#202); the manifest onboarding scaffolds
	// itself selects none, so every one applies.
	declared, err := config.LoadDeclaredTooling(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("read editors and agent_clients selection: %w", err)
	}
	return writeCompiledHarness(ctx, repoPath, agentsPath, declared.AgentClients)
}

// writeCompiledHarness compiles agentsPath for the selected agent clients and writes each
// vendor file confined to repoPath.
func writeCompiledHarness(ctx context.Context, repoPath, agentsPath string, clients []string) error {
	tr := compiler.NewTranspiler()
	tr.Clients = clients
	data, err := readOnboardDocument(ctx, agentsPath)
	if err != nil {
		return err
	}
	res, err := tr.CompileContent(string(data))
	if err != nil {
		return fmt.Errorf("compile %s: %w", agentsPath, err)
	}
	if len(res.Files) > maxOnboardOutputs {
		return fmt.Errorf("too many compiled outputs: %d", len(res.Files))
	}
	for i := 0; i < len(res.Files) && i < maxOnboardOutputs; i++ {
		f := res.Files[i]
		if err := writeOnboardFile(ctx, repoPath, f.RelativePath, []byte(f.Content)); err != nil {
			return err
		}
	}
	return nil
}

// prepareOnboardEditors resolves the selected editors' files through editor.PrepareWritesIn,
// the rule `editors generate` applies (editor.WriteWithReport) under ctx and confined to the
// repository, and writes nothing; it returns nil when the selection holds no editor. An
// existing JSON file keeps every key and gains the missing managed values; a developer-owned
// file (editor.IsPreservedEditorFile) is preserved. A file the rule refuses, such as a
// commented .vscode file that lacks managed values (editor.CommentedJSONError, #316), is an
// error naming it. Onboarding used to replace every existing editor file that was not
// developer-owned, dropping the repository's own keys and comments (#717).
//
// It runs before the scaffold is written. The scaffold adds YAML, Markdown and JSON files, and
// the synthesis derives editor content only from Go sources, a language server binary and a
// Makefile verify-all target, none of which the scaffold writes; so the set resolved here is
// the one the scaffolded repository synthesizes, which
// TestOnboardRepository_Positive_EditorSetResolvedBeforeScaffoldVerifies pins.
func prepareOnboardEditors(ctx context.Context, repoPath string, declared []string) (*editor.PreparedWrites, error) {
	selection, err := editor.SelectEditors(declared)
	if err != nil {
		return nil, fmt.Errorf("editors in .standards.yaml: %w", err)
	}
	if len(selection.Editors) == 0 {
		return nil, ctx.Err()
	}
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	opts.Editors = selection.Editors
	// Complexity is deliberately left at config.HISSComplexityCeiling here. Onboarding never
	// materializes the pinned profile catalog in the target repository, so the declared policy
	// is not resolvable at this point; `praetorctl adopt` and `praetorctl editors generate`
	// restate the resolved ceilings once it is (issue #360).
	set, err := editor.SynthesizeContext(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("synthesize editor configs: %w", err)
	}
	if len(set.Files) > maxOnboardOutputs {
		return nil, fmt.Errorf("too many editor outputs: %d", len(set.Files))
	}
	// The repository path is the operator's chosen boundary, resolved once as writeOnboardFile
	// resolves it, so a checkout reached through a link is onboarded; every path below it is
	// walked without following a link.
	root, err := util.ResolveExistingPath(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve onboarding repository %s: %w", repoPath, err)
	}
	prepared, err := editor.PrepareWritesIn(ctx, set, root)
	if err != nil {
		return nil, fmt.Errorf("resolve editor configs: %w", err)
	}
	return prepared, nil
}

// publishOnboardEditors writes the editor files prepareOnboardEditors resolved and returns the
// outcome of each. It runs after the scaffold is written, so a failure here, such as a file
// edited since it was resolved (editor.PreparedWrites.Publish refuses it), is wrapped with
// ErrEditorFilesIncomplete and names the command that finishes the run.
func publishOnboardEditors(ctx context.Context, repoPath string, editors *editor.PreparedWrites) ([]editor.WriteResult, error) {
	if editors == nil {
		return nil, ctx.Err()
	}
	report, err := editors.Publish(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w; scaffold files have been written: write editor configs: %w; "+
			"resolve it and run praetorctl harvest onboard --repo=%s --dry-run=false again", ErrEditorFilesIncomplete, err, repoPath)
	}
	return report.Files, ctx.Err()
}

// writeOnboardFile checks cancellation before each mutation, then creates rel's directory
// and replaces rel atomically, both confined to root through a pinned handle on it, so no
// output can be redirected outside the repository, not even by a link swapped in after a
// check (BUG-826).
func writeOnboardFile(ctx context.Context, root, rel string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("onboarding cancelled before writing %s: %w", rel, err)
	}
	if err := util.MkdirConfined(root, filepath.Dir(rel), util.TrackedDirPerm); err != nil {
		return fmt.Errorf("create onboarding output directory for %s: %w", rel, err)
	}
	if err := util.WriteFileConfined(root, rel, data, onboardFilePerm); err != nil {
		return fmt.Errorf("write onboarding output %s: %w", rel, err)
	}
	return nil
}

// ensureOnboardingBaseline writes an empty baseline when the repository has none.
func ensureOnboardingBaseline(ctx context.Context, repoPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	baselinePath, err := util.ConfinePath(repoPath, ".standards-baseline.json")
	if err != nil {
		return err
	}
	if util.PathExists(baselinePath) {
		return nil
	}
	base := &baseline.Baseline{Version: 1, TotalInfractions: 0, Infractions: []baseline.Infraction{}}
	if err := baseline.SaveBaseline(baselinePath, base); err != nil {
		return fmt.Errorf("save baseline: %w", err)
	}
	return nil
}

// ensureOnboardingHarness writes the initial AGENTS.md. The scaffolded
// AGENTS.md names the commands praetorctl actually provides; onboarding writes no Makefile,
// so it must not instruct agents to run a make target that does not exist.
func ensureOnboardingHarness(ctx context.Context, repoPath, repoName string) error {

	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if util.PathExists(agentsPath) {
		return nil
	}
	initialAgentsMD := fmt.Sprintf(
		"# %s Agent Operating Harness\n\nRun verification before concluding any turn:\n"+
			"```bash\npraetorctl audit\npraetorctl compile-context --verify\n```\n",
		repoName)
	if err := writeOnboardFile(ctx, repoPath, "AGENTS.md", []byte(initialAgentsMD)); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

// resolveGitIdentity reads the owner and repository name from the target's own origin
// remote through util.ResolveRemoteIdentity, the one origin-remote identity reader. It
// deliberately does not fall back to the directory layout: an onboarding manifest must never
// claim an owner the repository did not itself declare. A remote that is absent or names no
// <owner>/<repo> yields an empty identity; a read git did not answer is an error.
func resolveGitIdentity(ctx context.Context, repoPath string) (owner, name string, err error) {
	owner, name, err = util.ResolveRemoteIdentity(ctx, repoPath)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve onboarding identity: %w", err)
	}
	return owner, name, nil
}

// ensureOnboardingManifest writes .standards.yaml when the repository has none. The owner
// is resolved from the repository's own git identity; it is never invented, and the
// visibility is left blank for the operator to declare rather than defaulted to "public".
// repository.default_branch is the origin HEAD only this checkout records, when it is not main
// (forge.DefaultBranchToDeclare), so a CI checkout without it renders the same ruleset.
func ensureOnboardingManifest(ctx context.Context, repoPath, repoName, arch string, facets []string) error {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if util.PathExists(manifestPath) {
		return nil
	}

	owner, resolvedName, err := resolveGitIdentity(ctx, repoPath)
	if err != nil {
		return err
	}
	if resolvedName == "" {
		resolvedName = repoName
	}
	branch, err := forge.DefaultBranchToDeclare(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("onboarding manifest: %w", err)
	}

	manifest := config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner:         owner,
			Name:          resolvedName,
			DefaultBranch: branch,
		},
		Profiles: []string{arch},
		Facets:   facets,
	}
	data, err := config.RenderManifest(&manifest)
	if err != nil {
		return err
	}
	if err := writeOnboardFile(ctx, repoPath, ".standards.yaml", data); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}

// verifyOnboardLock verifies real pins; onboarding has no source pin resolver and must
// never invent a lockfile or report unhashed content digests as verified.
func verifyOnboardLock(ctx context.Context, repoPath string) (*config.LockValidation, error) {
	path, err := util.ConfinePath(repoPath, ".standards.yaml")
	if err != nil {
		return nil, err
	}
	data, err := readOnboardDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parse onboarding manifest: %w", err)
	}
	return config.ValidateLockfile(ctx, repoPath, manifest)
}

// readOnboardDocument bounds existing scaffold inputs and refuses special files before
// opening them, so a hostile AGENTS.md or manifest cannot block onboarding on a FIFO.
func readOnboardDocument(ctx context.Context, path string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openRegularSource(path)
	if err != nil {
		return nil, fmt.Errorf("open onboarding document: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	data, err = io.ReadAll(io.LimitReader(file, maxOnboardDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read onboarding document: %w", err)
	}
	if len(data) > maxOnboardDocumentBytes {
		return nil, fmt.Errorf("onboarding document exceeds %d bytes", maxOnboardDocumentBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
