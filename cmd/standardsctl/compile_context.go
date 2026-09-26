package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// compileContextTimeout bounds the transpilation and persona projection I/O (HISS-02).
const compileContextTimeout = 2 * time.Minute

func runCompileContext(args []string) error {
	fs := flag.NewFlagSet("compile-context", flag.ContinueOnError)
	verify := fs.Bool("verify", false, "Verify target files match AGENTS.md without modifying them")
	source := fs.String("source", "AGENTS.md", "Path to canonical AGENTS.md file")
	targetDir := fs.String("target-dir", ".", "Root directory to write/verify target vendor files")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("compile-context accepts no positional arguments, got %q", fs.Args())
	}

	ctx, cancel := context.WithTimeout(context.Background(), compileContextTimeout)
	defer cancel()

	tr := compiler.NewTranspiler()
	if *verify {
		return verifyCompiledContext(ctx, tr, *source, *targetDir)
	}
	return compileContext(ctx, tr, *source, *targetDir)
}

// verifyCompiledContext checks the six transpiled vendor files and every persona
// projection without writing anything.
func verifyCompiledContext(ctx context.Context, tr *compiler.Transpiler, source, targetDir string) error {
	fmt.Printf("Verifying agent context synchronization against %s...\n", source)
	if _, err := compiler.SyncRegisterBlock(ctx, filepath.Dir(source), source, false); err != nil {
		return fmt.Errorf("context verification failed: %s: %w", source, err)
	}
	res, err := tr.VerifyCompiled(ctx, source, targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	printNotApplicableTargets(res)
	lint, err := compiler.LintContext(ctx, source)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	fmt.Printf("  %s: %s.\n", source, lint.Summary())
	personasLinted, err := lintCanonicalPersonas(targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	skillsLinted, err := lintCanonicalSkillFiles(targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	fmt.Printf("  %d personas and %d skills passed the caveman lint (<= %d prose words each).\n",
		personasLinted, skillsLinted, compiler.AgentTextCeiling)
	verified, err := verifyAgentProjections(ctx, targetDir)
	if err != nil {
		return fmt.Errorf("agent persona verification failed: %w", err)
	}
	if err := printNotApplicablePersonaDirs(ctx, targetDir); err != nil {
		return fmt.Errorf("agent persona verification failed: %w", err)
	}
	skills, err := verifyPluginSkills(targetDir)
	if err != nil {
		return fmt.Errorf("plugin skill verification failed: %w", err)
	}
	if skills > 0 {
		fmt.Printf("  %d plugin skill projections verified (%s).\n", skills, pluginSkillsRel)
	}
	fmt.Printf("All agent context targets are 100%% in sync with canonical AGENTS.md (%d persona projections verified).\n", verified)
	return nil
}

// compileVendorTargets splices the text register block into the canonical source, then
// compiles and writes the six vendor files. The splice comes first so that every target
// receives the block through the unchanged renderer. The manifest that governs the block is
// the one beside the source, wherever the targets are written.
func compileVendorTargets(ctx context.Context, tr *compiler.Transpiler, source, targetDir string) error {
	spliced, err := compiler.SyncRegisterBlock(ctx, filepath.Dir(source), source, true)
	if err != nil {
		return fmt.Errorf("compilation failed: text register: %w", err)
	}
	if spliced {
		fmt.Printf("  [SPLICED] %s text register\n", source)
	}
	res, err := tr.CompileContext(ctx, source)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if err := tr.WriteOutputsContext(ctx, res, targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	for _, f := range res.Files {
		fmt.Printf("  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, compiler.MaxLineBudget)
	}
	printNotApplicableTargets(res)
	return nil
}

// printNotApplicableTargets names the projections agent_clients leaves out. They are neither
// written nor verified, so one the repository deleted stays deleted.
func printNotApplicableTargets(res *compiler.CompileResult) {
	printNotApplicable(res.NotApplicable)
}

// printNotApplicablePersonaDirs names the persona directories agent_clients leaves out, on the
// same terms as printNotApplicableTargets.
func printNotApplicablePersonaDirs(ctx context.Context, targetDir string) error {
	dirs, err := notApplicablePersonaDirs(ctx, targetDir)
	if err != nil {
		return err
	}
	printNotApplicable(dirs)
	return nil
}

func printNotApplicable(rels []string) {
	for _, rel := range rels {
		fmt.Printf("  [NOT_APPLICABLE] %-35s (not selected by agent_clients)\n", rel)
	}
}

// compileContext writes the vendor context files and every persona projection; any
// projection failure is an error, never a silently skipped success line.
func compileContext(ctx context.Context, tr *compiler.Transpiler, source, targetDir string) error {
	fmt.Printf("Compiling agent context from canonical %s...\n", source)
	if err := compileVendorTargets(ctx, tr, source, targetDir); err != nil {
		return err
	}

	agentsSrc := filepath.Join(targetDir, filepath.FromSlash(canonicalAgentsRel))
	agentFiles, err := compiler.CompileAgents(ctx, agentsSrc, targetDir)
	if err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if len(agentFiles) > 0 {
		fmt.Printf("  [COMPILED] %d autonomous agent vendor projections.\n", len(agentFiles))
	}
	if err := printNotApplicablePersonaDirs(ctx, targetDir); err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	pluginFiles, err := projectPluginAgents(targetDir)
	if err != nil {
		return fmt.Errorf("plugin agent projection failed: %w", err)
	}
	if pluginFiles > 0 {
		fmt.Printf("  [COMPILED] %d plugin agent projections (%s).\n", pluginFiles, pluginAgentsRel)
	}
	pluginSkills, err := projectPluginSkills(targetDir)
	if err != nil {
		return fmt.Errorf("plugin skill projection failed: %w", err)
	}
	if pluginSkills > 0 {
		fmt.Printf("  [COMPILED] %d plugin skill projections (%s).\n", pluginSkills, pluginSkillsRel)
	}

	fmt.Println("Cross-agent context transpilation completed successfully.")
	return nil
}
