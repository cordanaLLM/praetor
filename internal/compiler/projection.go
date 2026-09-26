package compiler

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// compileContextTimeout bounds the transpilation and persona projection I/O (HISS-02).

// verifyCompiledContext checks the six transpiled vendor files and every persona
// projection without writing anything.
func VerifyCompiledContext(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	fmt.Fprintf(w, "Verifying agent context synchronization against %s...\n", source)
	if _, err := SyncRegisterBlock(ctx, filepath.Dir(source), source, false); err != nil {
		return fmt.Errorf("context verification failed: %s: %w", source, err)
	}
	res, err := tr.VerifyCompiled(ctx, source, targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	printNotApplicableTargets(w, res)
	lint, err := LintContext(ctx, source)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	fmt.Fprintf(w, "  %s: %s.\n", source, lint.Summary())
	personasLinted, err := LintCanonicalPersonas(targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	skillsLinted, err := LintCanonicalSkillFiles(targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	fmt.Fprintf(w, "  %d personas and %d skills passed the caveman lint (<= %d prose words each).\n",
		personasLinted, skillsLinted, AgentTextCeiling)
	verified, err := VerifyAgentProjections(ctx, targetDir)
	if err != nil {
		return fmt.Errorf("agent persona verification failed: %w", err)
	}
	if err := printNotApplicablePersonaDirs(ctx, w, targetDir); err != nil {
		return fmt.Errorf("agent persona verification failed: %w", err)
	}
	skills, err := VerifyPluginSkills(targetDir)
	if err != nil {
		return fmt.Errorf("plugin skill verification failed: %w", err)
	}
	if skills > 0 {
		fmt.Fprintf(w, "  %d plugin skill projections verified (%s).\n", skills, PluginSkillsRel)
	}
	fmt.Fprintf(w, "All agent context targets are 100%% in sync with canonical AGENTS.md (%d persona projections verified).\n", verified)
	return nil
}

// compileVendorTargets splices the text register block into the canonical source, then
// compiles and writes the six vendor files. The splice comes first so that every target
// receives the block through the unchanged renderer. The manifest that governs the block is
// the one beside the source, wherever the targets are written.
func CompileVendorTargets(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	spliced, err := SyncRegisterBlock(ctx, filepath.Dir(source), source, true)
	if err != nil {
		return fmt.Errorf("compilation failed: text register: %w", err)
	}
	if spliced {
		fmt.Fprintf(w, "  [SPLICED] %s text register\n", source)
	}
	res, err := tr.CompileContext(ctx, source)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if err := tr.WriteOutputsContext(ctx, res, targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	for _, f := range res.Files {
		fmt.Fprintf(w, "  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, MaxLineBudget)
	}
	printNotApplicableTargets(w, res)
	return nil
}

// printNotApplicableTargets names the projections agent_clients leaves out. They are neither
// written nor verified, so one the repository deleted stays deleted.
func printNotApplicableTargets(w io.Writer, res *CompileResult) {
	printNotApplicable(w, res.NotApplicable)
}

// printNotApplicablePersonaDirs names the persona directories agent_clients leaves out, on the
// same terms as printNotApplicableTargets.
func printNotApplicablePersonaDirs(ctx context.Context, w io.Writer, targetDir string) error {
	dirs, err := notApplicablePersonaDirs(ctx, targetDir)
	if err != nil {
		return err
	}
	printNotApplicable(w, dirs)
	return nil
}

func printNotApplicable(w io.Writer, rels []string) {
	for _, rel := range rels {
		fmt.Fprintln(w, NotApplicableLine(rel))
	}
}

// compileContext writes the vendor context files and every persona projection; any
// projection failure is an error, never a silently skipped success line.
func CompileContextProjections(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	fmt.Fprintf(w, "Compiling agent context from canonical %s...\n", source)
	if err := CompileVendorTargets(ctx, w, tr, source, targetDir); err != nil {
		return err
	}

	agentsSrc := filepath.Join(targetDir, filepath.FromSlash(CanonicalAgentsRel))
	agentFiles, err := CompileAgents(ctx, agentsSrc, targetDir)
	if err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if len(agentFiles) > 0 {
		fmt.Fprintf(w, "  [COMPILED] %d autonomous agent vendor projections.\n", len(agentFiles))
	}
	if err := printNotApplicablePersonaDirs(ctx, w, targetDir); err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	pluginFiles, err := ProjectPluginAgents(targetDir)
	if err != nil {
		return fmt.Errorf("plugin agent projection failed: %w", err)
	}
	if pluginFiles > 0 {
		fmt.Fprintf(w, "  [COMPILED] %d plugin agent projections (%s).\n", pluginFiles, PluginAgentsRel)
	}
	pluginSkills, err := ProjectPluginSkills(targetDir)
	if err != nil {
		return fmt.Errorf("plugin skill projection failed: %w", err)
	}
	if pluginSkills > 0 {
		fmt.Fprintf(w, "  [COMPILED] %d plugin skill projections (%s).\n", pluginSkills, PluginSkillsRel)
	}

	fmt.Fprintln(w, "Cross-agent context transpilation completed successfully.")
	return nil
}
