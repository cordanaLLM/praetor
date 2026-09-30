package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
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

// ErrAgentSurface marks every persona and plugin skill projection failure VerifyCompiledContext
// returns, whatever its cause, so a caller tells a persona or plugin copy apart from AGENTS.md and
// the vendor files without matching text (errors.Is): adoption records it on the
// agent-definitions step, which writes those copies.
var ErrAgentSurface = errors.New("agent persona or plugin skill projection rejected")

// agentSurfaceError is a projection failure marked ErrAgentSurface. Its text and its chain are
// the failure's own, so errors.Is still reaches ErrAgentProjectionDrift below it.
type agentSurfaceError struct{ err error }

func (e agentSurfaceError) Error() string        { return e.err.Error() }
func (e agentSurfaceError) Unwrap() error        { return e.err }
func (e agentSurfaceError) Is(target error) bool { return target == ErrAgentSurface }

// markAgentSurface marks err ErrAgentSurface, and leaves nil nil so errors.Join drops it.
func markAgentSurface(err error) error {
	if err == nil {
		return nil
	}
	return agentSurfaceError{err: err}
}

// VerifyCompiledContext checks the text register block, the ignore rule for the evidence
// directory the block names (CheckEvidenceIgnored), the six transpiled vendor files, the caveman
// lint over AGENTS.md and every canonical persona and skill, and every persona and plugin skill
// projection, without writing anything. Every check runs and every failure is returned, so one
// run names each fix instead of hiding the later failures behind the first; each projection
// failure is marked ErrAgentSurface. The CLI's compile-context --verify and the MCP
// standards_compile_context verify_only call both run it.
func VerifyCompiledContext(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	sw.printf("Verifying agent context synchronization against %s...\n", source)
	vendorErr := verifyVendorContext(ctx, sw, tr, source, targetDir)
	lintErrs := lintAgentText(ctx, sw, source, targetDir)
	for i := range lintErrs {
		lintErrs[i] = prefixError("context verification failed", lintErrs[i])
	}
	verified, surfaceErr := verifyAgentSurfaces(ctx, sw, targetDir)
	if err := errors.Join(vendorErr, errors.Join(lintErrs...), surfaceErr); err != nil {
		return err
	}
	sw.printf("All agent context targets are 100%% in sync with canonical AGENTS.md (%d persona projections verified).\n", verified)
	return sw.err
}

// verifyVendorContext checks the text register block, the ignore rule for its evidence directory
// and the vendor files compiled from source, and returns every failure.
func verifyVendorContext(ctx context.Context, sw *syncWriter, tr *Transpiler, source, targetDir string) error {
	dir := filepath.Dir(source)
	var registerErr error
	if _, err := SyncRegisterBlock(ctx, dir, source, false); err != nil {
		registerErr = fmt.Errorf("%s: %w", source, err)
	}
	ignoreErr := CheckEvidenceIgnored(ctx, dir)
	res, targetsErr := tr.VerifyCompiled(ctx, source, targetDir)
	if targetsErr == nil {
		targetsErr = printNotApplicableTargets(sw.w, res)
	}
	const prefix = "context verification failed"
	return errors.Join(prefixError(prefix, registerErr), prefixError(prefix, ignoreErr), prefixError(prefix, targetsErr))
}

// lintAgentText runs the caveman gate over the canonical AGENTS.md at source and over every
// canonical persona and skill under targetDir. It prints the verdict of each surface that
// passed and returns one error per surface that failed. compile-context runs it after writing
// and compile-context --verify runs it read-only, so both hold the text to the same gate.
func lintAgentText(ctx context.Context, sw *syncWriter, source, targetDir string) []error {
	var errs []error
	if lint, err := LintContext(ctx, source); err != nil {
		errs = append(errs, err)
	} else {
		sw.printf("  %s: %s.\n", source, lint.Summary())
	}
	personas, personaErr := LintCanonicalPersonas(ctx, targetDir)
	skills, skillErr := LintCanonicalSkillFiles(ctx, targetDir)
	if personaErr != nil || skillErr != nil {
		return slices.DeleteFunc(append(errs, personaErr, skillErr), func(err error) bool { return err == nil })
	}
	sw.printf("  %d personas and %d skills passed the caveman lint (<= %d prose words each).\n",
		personas, skills, AgentTextCeiling)
	return errs
}

// verifyAgentSurfaces verifies every persona and plugin skill projection under targetDir and
// returns every failure, each marked ErrAgentSurface. It returns the number of persona copies
// verified.
func verifyAgentSurfaces(ctx context.Context, sw *syncWriter, targetDir string) (int, error) {
	verified, personaErr := VerifyAgentProjections(ctx, targetDir)
	if personaErr == nil {
		personaErr = printNotApplicablePersonaDirs(ctx, sw.w, targetDir)
	}
	skills, skillErr := VerifyPluginSkills(ctx, targetDir)
	if skillErr == nil && skills > 0 {
		sw.printf("  %d plugin skill projections verified (%s).\n", skills, PluginSkillsRel)
	}
	return verified, errors.Join(markAgentSurface(prefixError("agent persona verification failed", personaErr)),
		markAgentSurface(prefixError("plugin skill verification failed", skillErr)))
}

// prefixError wraps err with prefix, and leaves nil nil so errors.Join drops it.
func prefixError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
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
// is written, so a refused target leaves source and every output unchanged. Once everything is
// written, the caveman gate compile-context --verify applies runs over AGENTS.md and every
// canonical persona and skill (lintAgentText): a failure is returned, so the run never reports
// success on text the next verify rejects. The CLI's compile-context and the MCP
// standards_compile_context write call both run it.
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
	if err := errors.Join(lintAgentText(ctx, sw, source, targetDir)...); err != nil {
		return fmt.Errorf("context written, but compile-context --verify will fail: %w", err)
	}
	sw.println("Cross-agent context transpilation completed successfully.")
	return sw.err
}

// PlanAgentSurfaces returns every copy CompileAgentSurfaces would write below targetDir as it
// is now, after the same refusals, and writes nothing. A caller that reports on the copies,
// such as adoption, reads what each target holds before CompileAgentSurfaces replaces it.
func PlanAgentSurfaces(ctx context.Context, targetDir string) ([]TargetFile, error) {
	plan, err := checkedAgentSurfaces(ctx, targetDir)
	if err != nil {
		return nil, err
	}
	return plan.targetFiles(), nil
}

// CompileAgentSurfaces writes what CompileContextProjections writes after the vendor files: the
// copy of every canonical persona in the persona directory of each agent client agent_clients
// selects (SelectPersonaDirs), and the plugin persona and skill copies when the repository ships
// the plugin (PluginManifestRel). A directory the selection leaves out is neither written nor
// removed. Every target is checked before the first is written, through the confined walk
// compile-context --verify reads through, so a symlinked persona or plugin directory is refused
// with nothing written. It returns the copies it wrote, in order. Adoption's agent-definitions
// step writes its copies here, so adopt and compile-context project the same set (#359).
func CompileAgentSurfaces(ctx context.Context, w io.Writer, targetDir string) ([]TargetFile, error) {
	plan, err := checkedAgentSurfaces(ctx, targetDir)
	if err != nil {
		return nil, err
	}
	sw := &syncWriter{w: w}
	if err := writeAgentSurfaces(ctx, sw, targetDir, plan); err != nil {
		return nil, err
	}
	return plan.targetFiles(), sw.err
}

// checkedAgentSurfaces is the plan PlanAgentSurfaces and CompileAgentSurfaces share: every copy,
// with every target checked, after refusing a nil or cancelled context.
func checkedAgentSurfaces(ctx context.Context, targetDir string) (agentSurfacePlan, error) {
	if ctx == nil {
		return agentSurfacePlan{}, errors.New("agent projection: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return agentSurfacePlan{}, fmt.Errorf("agent projection cancelled: %w", err)
	}
	plan, err := planAgentSurfaces(ctx, targetDir, nil)
	if err != nil {
		return agentSurfacePlan{}, fmt.Errorf("agent projection failed: %w", err)
	}
	return plan, nil
}

// agentSurfacePlan holds every persona and plugin copy one compile writes: the persona copies
// agent_clients selects, and the plugin persona and skill copies when the plugin ships.
type agentSurfacePlan struct {
	personas, pluginPersonas, pluginSkills []projectionFile
}

// targetFiles lists every copy of the plan in write order, as the compiled files a report reads.
func (p agentSurfacePlan) targetFiles() []TargetFile {
	files := make([]TargetFile, 0, len(p.personas)+len(p.pluginPersonas)+len(p.pluginSkills))
	for _, set := range [][]projectionFile{p.personas, p.pluginPersonas, p.pluginSkills} {
		for _, file := range set {
			files = append(files, TargetFile{RelativePath: file.rel, Content: string(file.data)})
		}
	}
	return files
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
