package compiler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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

// ErrAgentSurface marks every persona and skill projection failure VerifyCompiledContext returns,
// whatever its cause, so a caller tells a persona or skill copy apart from AGENTS.md and the
// vendor files without matching text (errors.Is): adoption records it on the agent-definitions
// step, which writes those copies.
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
// directory the block names (CheckEvidenceIgnored), the cache stability of the compiled bands
// (VerifyStableContext), the six transpiled vendor files, the caveman
// lint over AGENTS.md, every tracked nested AGENTS.md and every canonical persona and skill, and
// every persona and plugin skill projection, without writing anything. Every check runs and every failure is returned, so one
// run names each fix instead of hiding the later failures behind the first; each projection
// failure is marked ErrAgentSurface. The CLI's compile-context --verify and the MCP
// standards_compile_context verify_only call both run it.
func VerifyCompiledContext(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	sw.printf("Verifying agent context synchronization against %s...\n", source)
	vendorErr := verifyVendorContext(ctx, sw, tr, source, targetDir)
	readOnlyErr := prefixError("context verification failed", VerifyReadOnlyContext(ctx, source, targetDir))
	lintErrs := lintAgentText(ctx, sw, source, targetDir)
	for i := range lintErrs {
		lintErrs[i] = prefixError("context verification failed", lintErrs[i])
	}
	verified, surfaceErr := verifyAgentSurfaces(ctx, sw, targetDir)
	stableErr := prefixError("context verification failed", VerifyStableContext(ctx, sw.w, tr, source))
	if err := errors.Join(vendorErr, readOnlyErr, errors.Join(lintErrs...), surfaceErr, stableErr); err != nil {
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
// other canonical agent source under targetDir: each tracked nested AGENTS.md, persona and
// skill (lintAgentSources). It prints the verdict of each surface that passed and returns one
// error per surface that failed. compile-context runs it after writing and compile-context
// --verify runs it read-only, so both hold the text to the same gate.
func lintAgentText(ctx context.Context, sw *syncWriter, source, targetDir string) []error {
	var errs []error
	if lint, err := LintContext(ctx, source); err != nil {
		errs = append(errs, err)
	} else {
		sw.printf("  %s: %s.\n", source, lint.Summary())
	}
	sources, failures := lintAgentSources(ctx, targetDir, source)
	if len(failures) > 0 {
		return append(errs, failures...)
	}
	sw.printf("  %s.\n", sources.summary())
	return errs
}

// verifyAgentSurfaces verifies every persona, plugin skill and client skill projection under
// targetDir and returns every failure, each marked ErrAgentSurface. It returns the number of
// persona copies verified.
func verifyAgentSurfaces(ctx context.Context, sw *syncWriter, targetDir string) (int, error) {
	verified, personaErr := VerifyAgentProjections(ctx, targetDir)
	if personaErr == nil {
		personaErr = printNotApplicablePersonaDirs(ctx, sw.w, targetDir)
	}
	skills, skillErr := VerifyPluginSkills(ctx, targetDir)
	if skillErr == nil && skills > 0 {
		sw.printf("  %d plugin skill projections verified (%s).\n", skills, PluginSkillsRel)
	}
	clientSkills, clientErr := VerifyClientSkills(ctx, targetDir)
	if clientErr == nil && clientSkills > 0 {
		sw.printf("  %d client skill projections verified (identical to %s).\n", clientSkills, CanonicalSkillsRel)
	}
	return verified, errors.Join(markAgentSurface(prefixError("agent persona verification failed", personaErr)),
		markAgentSurface(prefixError("plugin skill verification failed", skillErr)),
		markAgentSurface(prefixError("client skill verification failed", clientErr)))
}

// prefixError wraps err with prefix, and leaves nil nil so errors.Join drops it.
func prefixError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// CompileVendorTargets splices the text register block into the canonical source, then
// compiles and writes the six vendor files and the read-only projection (ReadOnlyFile). It
// reads source once and computes everything it writes from that snapshot before the first
// write (compileSource), so a failure leaves source and every output unchanged. The splice
// comes first so that every target receives the block through the unchanged renderer. The
// manifest that governs the block is the one beside the source, wherever the targets are
// written. It returns the files it wrote.
func CompileVendorTargets(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) (*CompileResult, error) {
	compiled, err := compileSource(ctx, tr, source)
	if err != nil {
		return nil, err
	}
	if err := writeCompiledSource(ctx, w, tr, source, targetDir, compiled); err != nil {
		return nil, err
	}
	return compiled.result, nil
}

// compiledSource is what CompileVendorTargets writes, computed from one read of the source:
// the source after the text register splice, whether the splice changed it, and the vendor
// files followed by the read-only projection.
type compiledSource struct {
	spliced string
	changed bool
	result  *CompileResult
}

// compileSource reads source once and computes the splice, the vendor files and the read-only
// projection from that one snapshot, writing nothing. Every failure reads "compilation failed".
func compileSource(ctx context.Context, tr *Transpiler, source string) (compiledSource, error) {
	root := filepath.Dir(source)
	data, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: failed to read source %s: %w", source, err)
	}
	_, block, err := loadRegister(ctx, root, nil)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: text register: %w", err)
	}
	spliced, changed, _, err := spliceRegister(string(data), block)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: text register: %s: %w", source, err)
	}
	selected, err := tr.forRepository(ctx, root)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: %w", err)
	}
	res, err := selected.CompileContent(spliced)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: %w", err)
	}
	readOnly, err := agentcontext.ReadOnlyTarget(spliced)
	if err != nil {
		return compiledSource{}, fmt.Errorf("compilation failed: read-only projection: %w", err)
	}
	res.SourcePath = source
	res.Files = append(res.Files, readOnly)
	return compiledSource{spliced: spliced, changed: changed, result: res}, nil
}

// lintReadOnlyTarget runs the caveman gate VerifyReadOnlyContext applies over the read-only
// projection among the written files, so compile-context fails on a projection verify rejects.
func lintReadOnlyTarget(written *CompileResult) error {
	for _, f := range written.Files {
		if f.RelativePath == ReadOnlyFile {
			_, err := LintContextText(ReadOnlyFile, f.Content)
			return err
		}
	}
	return nil
}

// writeCompiledSource checks every output before it writes the splice into source, then writes
// the outputs and reports each.
func writeCompiledSource(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string, compiled compiledSource) error {
	sw := &syncWriter{w: w}
	files := make([]projectionFile, 0, len(compiled.result.Files))
	for _, f := range compiled.result.Files {
		files = append(files, projectionFile{rel: f.RelativePath, data: []byte(f.Content)})
	}
	if err := checkProjectionFiles(ctx, targetDir, files); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	if compiled.changed {
		if err := contextopt.WriteSnapshot(ctx, source, []byte(compiled.spliced), 0o644); err != nil {
			return fmt.Errorf("compilation failed: text register: failed to write %s: %w", source, err)
		}
		sw.printf("  [SPLICED] %s text register\n", source)
	}
	if err := tr.WriteOutputsContext(ctx, compiled.result, targetDir); err != nil {
		return fmt.Errorf("failed to write compiled files: %w", err)
	}
	for _, f := range compiled.result.Files {
		sw.printf("  [COMPILED] %-35s (%d lines, budget <= %d)\n", f.RelativePath, f.LineCount, MaxLineBudget)
	}
	if err := printNotApplicableTargets(w, compiled.result); err != nil {
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

// CompileContextProjections writes the vendor context files, the read-only projection, every
// persona projection and the plugin persona and skill copies; any projection failure is an
// error, never a silently skipped success line. Every canonical persona and skill is read, the
// source is read once and compiled (compileSource), and every target is checked
// (checkProjectionFiles), before the text register splice into source and before the first file
// is written, so a refused target leaves source and every output unchanged. Once everything is
// written, the caveman gate compile-context --verify applies runs over AGENTS.md, every tracked
// nested AGENTS.md, every canonical persona and skill (lintAgentText) and the read-only
// projection (lintReadOnlyTarget): a failure is returned, so the run never reports success on
// text the next verify rejects. The CLI's compile-context and the MCP
// standards_compile_context write call both run it.
func CompileContextProjections(ctx context.Context, w io.Writer, tr *Transpiler, source, targetDir string) error {
	sw := &syncWriter{w: w}
	sw.printf("Compiling agent context from canonical %s...\n", source)
	vendor, err := tr.vendorTargetFiles(ctx, filepath.Dir(source))
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	plan, err := planAgentSurfaces(ctx, targetDir, append(vendor, projectionFile{rel: ReadOnlyFile}), PendingSources{})
	if err != nil {
		return fmt.Errorf("agent projection failed: %w", err)
	}
	written, err := CompileVendorTargets(ctx, w, tr, source, targetDir)
	if err != nil {
		return err
	}
	if err := writeAgentSurfaces(ctx, sw, targetDir, plan); err != nil {
		return err
	}
	lintErrs := append(lintAgentText(ctx, sw, source, targetDir), lintReadOnlyTarget(written))
	if err := errors.Join(lintErrs...); err != nil {
		return fmt.Errorf("context written, but compile-context --verify will fail: %w", err)
	}
	sw.println("Cross-agent context transpilation completed successfully.")
	return sw.err
}

// PlanAgentSurfaces returns every copy CompileAgentSurfaces would write below targetDir as it
// is now, after the same refusals, and writes nothing. A caller that reports on the copies,
// such as adoption, reads what each target holds before CompileAgentSurfaces replaces it.
func PlanAgentSurfaces(ctx context.Context, targetDir string) ([]TargetFile, error) {
	return PlanAgentSurfacesOver(ctx, targetDir, PendingSources{})
}

// PendingSources holds the canonical sources a caller writes before it projects them.
type PendingSources struct {
	// Personas maps the file name of a canonical persona (repo-auditor.md) to the bytes the
	// caller writes at CanonicalAgentsRel.
	Personas map[string][]byte
	// Skills maps the name of a skill Praetor ships (config.RegisterSkillBundle) to the bytes of
	// the SKILL.md the caller writes under CanonicalSkillsRel.
	Skills map[string][]byte
	// Licenses maps the name of a skill Praetor ships to the bytes of the LICENSE the caller
	// writes beside its SKILL.md.
	Licenses map[string][]byte
}

// PlanAgentSurfacesOver is PlanAgentSurfaces for the tree a caller is about to leave: a pending
// persona or skill is planned with its pending bytes whether or not it exists yet; every other
// one is read from disk. Adoption's dry run plans with the personas and skills its real run
// writes, so it lists the copies that run projects (#366). A pending persona name that is not a
// persona file name, and a pending skill that is not one Praetor ships, are refused.
func PlanAgentSurfacesOver(ctx context.Context, targetDir string, pending PendingSources) ([]TargetFile, error) {
	plan, err := checkedAgentSurfaces(ctx, targetDir, pending)
	if err != nil {
		return nil, err
	}
	return plan.targetFiles(), nil
}

// CompileAgentSurfaces writes what CompileContextProjections writes after the vendor files: the
// copy of every canonical persona in the persona directory of each agent client agent_clients
// selects (SelectPersonaDirs), the copy of every skill Praetor ships that the repository carries
// in the skill directory of each selected client that does not read .agents/skills
// (SelectSkillDirs, clientSkillProjections), and the plugin persona and skill copies when the
// repository ships the plugin (PluginManifestRel). A directory the selection leaves out is
// neither written nor removed. Every target is checked before the first is written, through the confined walk
// compile-context --verify reads through, so a symlinked persona or plugin directory is refused
// with nothing written. It returns the copies it wrote, in order. Adoption's agent-definitions
// step writes its copies here, so adopt and compile-context project the same set (#359).
func CompileAgentSurfaces(ctx context.Context, w io.Writer, targetDir string) ([]TargetFile, error) {
	plan, err := checkedAgentSurfaces(ctx, targetDir, PendingSources{})
	if err != nil {
		return nil, err
	}
	sw := &syncWriter{w: w}
	if err := writeAgentSurfaces(ctx, sw, targetDir, plan); err != nil {
		return nil, err
	}
	return plan.targetFiles(), sw.err
}

// checkedAgentSurfaces is the plan PlanAgentSurfacesOver and CompileAgentSurfaces share: every
// copy, with every target checked, after refusing a nil or cancelled context. pending holds the
// canonical sources a caller writes before projecting (PlanAgentSurfacesOver); empty reads disk.
func checkedAgentSurfaces(ctx context.Context, targetDir string, pending PendingSources) (agentSurfacePlan, error) {
	if ctx == nil {
		return agentSurfacePlan{}, errors.New("agent projection: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return agentSurfacePlan{}, fmt.Errorf("agent projection cancelled: %w", err)
	}
	plan, err := planAgentSurfaces(ctx, targetDir, nil, pending)
	if err != nil {
		return agentSurfacePlan{}, fmt.Errorf("agent projection failed: %w", err)
	}
	return plan, nil
}

// agentSurfacePlan holds every persona and skill copy one compile writes: the persona and client
// skill copies agent_clients selects, and the plugin persona and skill copies when the plugin
// ships.
type agentSurfacePlan struct {
	personas, clientSkills, pluginPersonas, pluginSkills []projectionFile
	// staleLicenses are the LICENSE copies the write removes (staleClientLicenses,
	// stalePluginLicenses), planned and checked with the copies it writes.
	staleLicenses []string
}

// sets lists the copies of the plan by kind, in write order.
func (p agentSurfacePlan) sets() [][]projectionFile {
	return [][]projectionFile{p.personas, p.clientSkills, p.pluginPersonas, p.pluginSkills}
}

// targetFiles lists every copy of the plan in write order, as the compiled files a report reads.
func (p agentSurfacePlan) targetFiles() []TargetFile {
	files := make([]TargetFile, 0, len(p.personas)+len(p.clientSkills)+len(p.pluginPersonas)+len(p.pluginSkills)+len(p.staleLicenses))
	for _, set := range p.sets() {
		for _, file := range set {
			files = append(files, TargetFile{RelativePath: file.rel, Content: string(file.data)})
		}
	}
	for _, rel := range p.staleLicenses {
		files = append(files, TargetFile{RelativePath: rel, Remove: true})
	}
	return files
}

// planAgentSurfaces reads every canonical persona and skill and checks the target of every
// vendor file in vendor and of every persona and skill copy (checkProjectionFiles), writing
// nothing. The persona, skill and plugin directories are the ones verify reads
// (agentProjectionDirs, SelectSkillDirs), so write and verify refuse the same trees. pending
// overrides or adds canonical personas (personaProjections) and skills Praetor ships
// (clientSkillProjections).
func planAgentSurfaces(ctx context.Context, targetDir string, vendor []projectionFile, pending PendingSources) (agentSurfacePlan, error) {
	plan, err := planPersonaCopies(ctx, targetDir, pending.Personas)
	if err != nil {
		return plan, err
	}
	skillDirs, _, err := SelectSkillDirs(ctx, targetDir)
	if err != nil {
		return plan, err
	}
	if plan.clientSkills, err = clientSkillProjections(ctx, targetDir, skillDirs, pending); err != nil {
		return plan, err
	}
	if plan.pluginSkills, err = pluginSkillProjections(ctx, targetDir); err != nil {
		return plan, err
	}
	for _, files := range append([][]projectionFile{vendor}, plan.sets()...) {
		if err := checkProjectionFiles(ctx, targetDir, files); err != nil {
			return plan, err
		}
	}
	if plan.staleLicenses, err = plannedStaleLicenses(ctx, targetDir, skillDirs, pending); err != nil {
		return plan, err
	}
	return plan, checkProjectionFiles(ctx, targetDir, removalFiles(plan.staleLicenses))
}

// plannedStaleLicenses returns the stale LICENSE copies one write removes: the plugin's and the
// selected client directories' (stalePluginLicenses, staleClientLicenses).
func plannedStaleLicenses(ctx context.Context, targetDir string, skillDirs []string, pending PendingSources) ([]string, error) {
	plugin, err := stalePluginLicenses(ctx, targetDir)
	if err != nil {
		return nil, err
	}
	client, err := staleClientLicenses(ctx, targetDir, skillDirs, pending)
	if err != nil {
		return nil, err
	}
	return append(plugin, client...), nil
}

// removalFiles lists rels as projection files without content, so the writer's refusals run over
// the files a write removes.
func removalFiles(rels []string) []projectionFile {
	files := make([]projectionFile, len(rels))
	for i := range rels {
		files[i] = projectionFile{rel: rels[i]}
	}
	return files
}

// planPersonaCopies returns a plan holding the persona copies agent_clients selects and the
// plugin persona copies, with pending overriding or adding canonical personas.
func planPersonaCopies(ctx context.Context, targetDir string, pending map[string][]byte) (agentSurfacePlan, error) {
	var plan agentSurfacePlan
	dirs, _, err := SelectPersonaDirs(ctx, targetDir)
	if err != nil {
		return plan, err
	}
	if plan.personas, err = personaProjections(ctx, targetDir, dirs, pending); err != nil {
		return plan, err
	}
	plan.pluginPersonas, err = personaProjections(ctx, targetDir, pluginPersonaDirs(targetDir), pending)
	return plan, err
}

// writeAgentSurfaces writes a checked plan and reports each surface it wrote.
func writeAgentSurfaces(ctx context.Context, sw *syncWriter, targetDir string, plan agentSurfacePlan) error {
	if err := removeStaleSkillLicenses(ctx, targetDir, plan.staleLicenses); err != nil {
		return fmt.Errorf("stale skill licence removal failed: %w", err)
	}
	if n := len(plan.staleLicenses); n > 0 {
		sw.printf("  [COMPILED] removed %d stale skill licence copies (%s).\n", n, strings.Join(plan.staleLicenses, ", "))
	}
	if err := writePersonaSurfaces(ctx, sw, targetDir, plan); err != nil {
		return err
	}
	return writeSkillSurfaces(ctx, sw, targetDir, plan)
}

func writePersonaSurfaces(ctx context.Context, sw *syncWriter, targetDir string, plan agentSurfacePlan) error {
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
	return nil
}

func writeSkillSurfaces(ctx context.Context, sw *syncWriter, targetDir string, plan agentSurfacePlan) error {
	if err := writeProjectionFiles(ctx, targetDir, plan.clientSkills); err != nil {
		return fmt.Errorf("client skill projection failed: %w", err)
	}
	if n := len(plan.clientSkills); n > 0 {
		sw.printf("  [COMPILED] %d client skill projections of %s.\n", n, CanonicalSkillsRel)
	}
	if err := writeProjectionFiles(ctx, targetDir, plan.pluginSkills); err != nil {
		return fmt.Errorf("plugin skill projection failed: %w", err)
	}
	if n := len(plan.pluginSkills); n > 0 {
		sw.printf("  [COMPILED] %d plugin skill projections (%s).\n", n, PluginSkillsRel)
	}
	return nil
}
