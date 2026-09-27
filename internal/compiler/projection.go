package compiler

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// syncWriter prints progress lines and keeps the first write error, so a caller checks once.
type syncWriter struct {
	w   io.Writer
	err error
}

func (sw *syncWriter) printf(format string, args ...any) {
	if sw.err != nil {
		return
	}
	_, sw.err = fmt.Fprintf(sw.w, format, args...)
}

func (sw *syncWriter) println(args ...any) {
	if sw.err != nil {
		return
	}
	_, sw.err = fmt.Fprintln(sw.w, args...)
}

// VerifyCompiledContext checks the six transpiled vendor files, the caveman lint over AGENTS.md
// and every canonical persona and skill, and every persona and plugin skill projection, without
// writing anything. The CLI's compile-context --verify and the MCP standards_compile_context
// verify_only call both run it.
func VerifyCompiledContext(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	sw.printf("Verifying agent context synchronization against %s...\n", source)
	if err := verifyVendorContext(ctx, sw, tr, source, targetDir); err != nil {
		return err
	}
	verified, err := verifyAgentSurfaces(ctx, sw, targetDir)
	if err != nil {
		return err
	}
	sw.printf("All agent context targets are 100%% in sync with canonical AGENTS.md (%d persona projections verified).\n", verified)
	return sw.err
}

// verifyVendorContext checks the text register block, the vendor files compiled from source and
// the caveman lint over source.
func verifyVendorContext(ctx context.Context, sw *syncWriter, tr *Transpiler, source, targetDir string) error {
	if _, err := SyncRegisterBlock(ctx, filepath.Dir(source), source, false); err != nil {
		return fmt.Errorf("context verification failed: %s: %w", source, err)
	}
	res, err := tr.VerifyCompiled(ctx, source, targetDir)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	if err := printNotApplicableTargets(sw.w, res); err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	lint, err := LintContext(ctx, source)
	if err != nil {
		return fmt.Errorf("context verification failed: %w", err)
	}
	sw.printf("  %s: %s.\n", source, lint.Summary())
	return nil
}

// verifyAgentSurfaces lints every canonical persona and skill and verifies every persona and
// plugin skill projection under targetDir. It returns the number of persona copies verified.
func verifyAgentSurfaces(ctx context.Context, sw *syncWriter, targetDir string) (int, error) {
	personasLinted, err := LintCanonicalPersonas(ctx, targetDir)
	if err != nil {
		return 0, fmt.Errorf("context verification failed: %w", err)
	}
	skillsLinted, err := LintCanonicalSkillFiles(ctx, targetDir)
	if err != nil {
		return 0, fmt.Errorf("context verification failed: %w", err)
	}
	sw.printf("  %d personas and %d skills passed the caveman lint (<= %d prose words each).\n",
		personasLinted, skillsLinted, AgentTextCeiling)
	verified, err := VerifyAgentProjections(ctx, targetDir)
	if err != nil {
		return 0, fmt.Errorf("agent persona verification failed: %w", err)
	}
	if err := printNotApplicablePersonaDirs(ctx, sw.w, targetDir); err != nil {
		return 0, fmt.Errorf("agent persona verification failed: %w", err)
	}
	skills, err := VerifyPluginSkills(ctx, targetDir)
	if err != nil {
		return 0, fmt.Errorf("plugin skill verification failed: %w", err)
	}
	if skills > 0 {
		sw.printf("  %d plugin skill projections verified (%s).\n", skills, PluginSkillsRel)
	}
	return verified, nil
}

// CompileVendorTargets splices the text register block into the canonical source, then
// compiles and writes the six vendor files. The splice comes first so that every target
// receives the block through the unchanged renderer. The manifest that governs the block is
// the one beside the source, wherever the targets are written.
func CompileVendorTargets(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	spliced, err := SyncRegisterBlock(ctx, filepath.Dir(source), source, true)
	if err != nil {
		return fmt.Errorf("compilation failed: text register: %w", err)
	}
	if spliced {
		sw.printf("  [SPLICED] %s text register\n", source)
	}
	res, err := tr.CompileContext(ctx, source)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if err := tr.WriteOutputsContext(ctx, res, targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	for _, f := range res.Files {
		sw.printf("  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, MaxLineBudget)
	}
	if err := printNotApplicableTargets(w, res); err != nil {
		return fmt.Errorf("failed to write not applicable targets: %w", err)
	}
	return sw.err
}

// printNotApplicableTargets names the projections agent_clients leaves out. They are neither
// written nor verified, so one the repository deleted stays deleted.
func printNotApplicableTargets(w io.Writer, res *CompileResult) error {
	return printNotApplicable(w, res.NotApplicable)
}

// printNotApplicablePersonaDirs names the persona directories agent_clients leaves out, on the
// same terms as printNotApplicableTargets.
func printNotApplicablePersonaDirs(ctx context.Context, w io.Writer, targetDir string) error {
	dirs, err := notApplicablePersonaDirs(ctx, targetDir)
	if err != nil {
		return err
	}
	return printNotApplicable(w, dirs)
}

func printNotApplicable(w io.Writer, rels []string) error {
	for _, rel := range rels {
		if _, err := fmt.Fprintln(w, NotApplicableLine(rel)); err != nil {
			return fmt.Errorf("write not applicable line: %w", err)
		}
	}
	return nil
}

// CompileContextProjections writes the vendor context files, every persona projection and the
// plugin persona and skill copies; any projection failure is an error, never a silently skipped
// success line. Every canonical persona and skill is read, and every target is checked
// (checkProjectionFiles), before the text register splice into source and before the first file
// is written, so a refused target leaves source and every output unchanged. The CLI's
// compile-context and the MCP standards_compile_context write call both run it.
func CompileContextProjections(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	sw.printf("Compiling agent context from canonical %s...\n", source)
	vendor, err := tr.vendorTargetFiles(ctx, filepath.Dir(source))
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	plan, err := planAgentSurfaces(ctx, targetDir, vendor)
	if err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if err := CompileVendorTargets(ctx, w, tr, source, targetDir); err != nil {
		return err
	}
	if err := writeAgentSurfaces(ctx, sw, targetDir, plan); err != nil {
		return err
	}
	sw.println("Cross-agent context transpilation completed successfully.")
	return sw.err
}

// agentSurfacePlan holds every persona and plugin copy one compile writes: the persona copies
// agent_clients selects, and the plugin persona and skill copies when the plugin ships.
type agentSurfacePlan struct {
	personas, pluginPersonas, pluginSkills []projectionFile
}

// planAgentSurfaces reads every canonical persona and skill and checks the target of every
// vendor file in vendor and of every persona and plugin copy (checkProjectionFiles), writing
// nothing. The persona and plugin directories are the ones verify reads (agentProjectionDirs), so
// write and verify refuse the same trees.
func planAgentSurfaces(ctx context.Context, targetDir string, vendor []projectionFile) (agentSurfacePlan, error) {
	var plan agentSurfacePlan
	dirs, _, err := SelectPersonaDirs(ctx, targetDir)
	if err != nil {
		return plan, err
	}
	if plan.personas, err = personaProjections(ctx, targetDir, dirs); err != nil {
		return plan, err
	}
	if plan.pluginPersonas, err = personaProjections(ctx, targetDir, pluginPersonaDirs(targetDir)); err != nil {
		return plan, err
	}
	if plan.pluginSkills, err = pluginSkillProjections(ctx, targetDir); err != nil {
		return plan, err
	}
	for _, files := range [][]projectionFile{vendor, plan.personas, plan.pluginPersonas, plan.pluginSkills} {
		if err := checkProjectionFiles(ctx, targetDir, files); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// writeAgentSurfaces writes a checked plan and reports each surface it wrote.
func writeAgentSurfaces(ctx context.Context, sw *syncWriter, targetDir string, plan agentSurfacePlan) error {
	if err := writeProjectionFiles(ctx, targetDir, plan.personas); err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if n := len(plan.personas); n > 0 {
		sw.printf("  [COMPILED] %d autonomous agent vendor projections.\n", n)
	}
	if err := printNotApplicablePersonaDirs(ctx, sw.w, targetDir); err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	if err := writeProjectionFiles(ctx, targetDir, plan.pluginPersonas); err != nil {
		return fmt.Errorf("plugin agent projection failed: %w", err)
	}
	if n := len(plan.pluginPersonas); n > 0 {
		sw.printf("  [COMPILED] %d plugin agent projections (%s).\n", n, PluginAgentsRel)
	}
	if err := writeProjectionFiles(ctx, targetDir, plan.pluginSkills); err != nil {
		return fmt.Errorf("plugin skill projection failed: %w", err)
	}
	if n := len(plan.pluginSkills); n > 0 {
		sw.printf("  [COMPILED] %d plugin skill projections (%s).\n", n, PluginSkillsRel)
	}
	return nil
}
