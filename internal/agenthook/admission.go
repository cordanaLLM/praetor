// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Host admission for heavy commands (#1131, docs/guides/admission.md). With
// hooks.admission.enforce set, the pre-tool policy refuses a command that starts a recognised
// heavy step outside an admitted form, and the refusal names the admitted form. The command
// is read the way the shell scanner reads a script (hiss.ShellCode): quoted text, comments and
// here-document bodies are not commands, so a commit message that mentions `make` is not a
// heavy step. A command the reader cannot follow is refused while enforcement is on.

const (
	// admissionMaxLines, admissionMaxSegments and admissionMaxWords bound the reading of one
	// command (HISS-02); the hook input is already bounded to 1 MiB.
	admissionMaxLines    = 4096
	admissionMaxSegments = 4096
	admissionMaxWords    = 256
	// admissionMaxWrappers bounds how many wrapper commands (nice, timeout, ...) are looked
	// through to the command they run.
	admissionMaxWrappers = 8
	// admissionShapeWords is how many words of a command its shapes see.
	admissionShapeWords = 16
	// admissionLabelEnv is a segment-local label for a live-judged isolated push.
	admissionLabelEnv = "PRAETOR_ADMIT"
	gitPushShape      = `^git( -[Cc] [^ ]+)* push( |$)`
)

// builtinHeavyShapes are the heavy steps the engine recognises: builds, test suites, gates and
// pushes, whose pre-push gate is a full verification. Each is RE2, matched against a command's
// words joined by one space, from the command's base name on. hooks.admission.commands adds
// shapes in the same form.
var builtinHeavyShapes = []string{
	`^(make|gmake|ninja)( |$)`,
	`^go (build|install|test)( |$)`,
	`^cargo (bench|build|check|clippy|install|test)( |$)`,
	`^meson (compile|test)( |$)`,
	`^cmake --build( |$)`,
	`^(npm|pnpm|yarn) (build|test|run build)( |$)`,
	`^(pytest|tox|nox)( |$)`,
	`^(praetorctl|standardsctl) gate run( |$)`,
	gitPushShape,
	`^(docker|podman|buildah) (build|bud)( |$)`,
	`^(bazel|bazelisk) (build|test)( |$)`,
	`^(gradle|gradlew|mvn)( |$)`,
}

// wrapperSpec describes a command that runs another one: the options that take a separate
// value, and how many positional words precede the command it runs.
type wrapperSpec struct {
	values           []string
	positional       int
	optionalPriority bool
}

// admissionWrappers are the commands looked through to the command they run, so `nice -n 10
// make` and `systemd-run --user --scope -p MemoryMax=8G -- go test ./...` are judged as make
// and go test. Required data arguments follow installed coreutils/findutils
// --help, util-linux --help, time(1), and Bash help. Optional xargs arguments
// (-e/-i/-l, --eof/--replace) attach to their option; no following word consumed.
// chrt's priority is optional and numeric; taskset's mask remains positional.
var admissionWrappers = map[string]wrapperSpec{
	"chrt":    {values: []string{"-T", "--sched-runtime", "-P", "--sched-period", "-D", "--sched-deadline"}, positional: 1, optionalPriority: true},
	"command": {},
	"builtin": {},
	"env":     {values: []string{"-u", "--unset", "-C", "--chdir", "-a", "--argv0", "--env0-from"}},
	"exec":    {values: []string{"-a"}},
	"flock":   {values: []string{"-w", "--timeout", "-E", "--conflict-exit-code"}, positional: 1},
	"ionice":  {values: []string{"-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid"}},
	"nice":    {values: []string{"-n", "--adjustment"}},
	"nohup":   {},
	"setsid":  {},
	"stdbuf":  {values: []string{"-i", "--input", "-o", "--output", "-e", "--error"}},
	"systemd-run": {values: []string{"-p", "--property", "-u", "--unit", "-E", "--setenv", "--slice", "-M", "--machine", "-H", "--host",
		"--uid", "--gid", "--nice", "--working-directory", "--description"}},
	"taskset": {positional: 1},
	"time":    {values: []string{"-f", "--format", "-o", "--output"}},
	"timeout": {values: []string{"-k", "--kill-after", "-s", "--signal"}, positional: 1},
	"xargs": {values: []string{"-a", "--arg-file", "-d", "--delimiter", "-E", "-I", "-L", "--max-lines", "-n", "--max-args",
		"-P", "--max-procs", "-s", "--max-chars", "--process-slot-var"}},
}

// admissionPrelude are the shell words that may stand before a command name in one segment.
var admissionPrelude = []string{"!", "if", "then", "else", "elif", "do", "while", "until"}

var (
	assignmentWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	priorityWord   = regexp.MustCompile(`^[0-9]+$`)
	labelWord      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,30}$`)
	gitPushPattern = regexp.MustCompile(gitPushShape)
	// segmentSeparators end one simple command: lists, pipes, subshells, groups and command
	// substitutions (the lexer writes a backquoted one as $(...)).
	segmentSeparators = strings.NewReplacer(";", "\n", "&", "\n", "|", "\n", "(", "\n", ")", "\n", "{", "\n", "}", "\n")
)

const (
	pushTargetTimeout  = 15 * time.Second
	MaxPushAllBranches = 32
)

// pushJudgement groups the wrapper and command context needed to judge one heavy step or
// git push invocation.
type pushJudgement struct {
	workDir       string
	name          string
	shape         string
	args          []string
	admitted      bool
	sawSystemdRun bool
	scopeMissing  []string
	pushLabel     string
	notice        string
}

// PushTarget identifies the repository directory, parsed network remote, forge kind and
// destination branch of a judged git push.
type PushTarget struct {
	RepoDir string
	Remote  util.GitRemote
	Forge   config.Forge
	Branch  string
}

// PullRequestHeadReader queries the forge for pull requests whose head is target.Branch.
type PullRequestHeadReader func(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error)

// admissionCheck holds the compiled shapes and scope/job settings of an enforcing policy
// (order engine-hook-build-scope-gate-20261027).
type admissionCheck struct {
	shapes           []*regexp.Regexp
	scopeForm        bool
	divisor          int
	workDir          string
	readHost         func() (sentinel.HostStats, error)
	numCPU           func() int
	readPullRequests PullRequestHeadReader
}

// newAdmissionCheck compiles the built-in shapes and hooks.admission.commands; it returns nil
// when the settings do not enforce admission.
func newAdmissionCheck(settings config.AdmissionSettings) (*admissionCheck, error) {
	if !settings.Enforce {
		return nil, nil
	}
	if err := config.ValidateAdmissionCommands(settings.Commands); err != nil {
		return nil, err
	}
	divisor := settings.MaxJobsDivisor
	if divisor == 0 {
		divisor = config.DefaultAdmissionMaxJobsDivisor
	}
	if err := config.ValidateAdmissionMaxJobsDivisor(divisor); err != nil {
		return nil, err
	}
	sources := append(slices.Clone(builtinHeavyShapes), settings.Commands...)
	check := &admissionCheck{
		shapes:           make([]*regexp.Regexp, 0, len(sources)),
		scopeForm:        settings.ScopeForm,
		divisor:          divisor,
		readHost:         admission.HostReader("."),
		numCPU:           runtime.NumCPU,
		readPullRequests: defaultPullRequestHeadReader,
	}
	for index, source := range sources {
		compiled, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("admission shape %d: %w", index, err)
		}
		check.shapes = append(check.shapes, compiled)
	}
	return check, nil
}

// defaultPullRequestHeadReader queries the forge driver for pull requests by head branch.
func defaultPullRequestHeadReader(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
	declaredHost := declaredForgejoHost(ctx, target.RepoDir)
	return forge.ReadPullRequestsByHead(ctx, target.Forge, target.Remote, target.Branch, declaredHost)
}

// declaredForgejoHost resolves the repository's declared Forgejo host from origin remote when forge is declared.
func declaredForgejoHost(ctx context.Context, repoDir string) string {
	declared, err := declaredManifestForge(ctx, repoDir)
	if err != nil && declared == "" {
		return ""
	}
	if declared != config.ForgeForgejo {
		return ""
	}
	origin, err := util.ReadOriginRemote(ctx, repoDir)
	if err != nil {
		return ""
	}
	return origin.Host
}

// CheckAdmission evaluates command against the admission settings alone
// (order engine-hook-build-scope-gate-20261027).
func CheckAdmission(settings config.AdmissionSettings, command string) (Verdict, error) {
	check, err := newAdmissionCheck(settings)
	if err != nil {
		return Verdict{Outcome: Deny}, err
	}
	if check == nil {
		return Verdict{Outcome: Allow}, nil
	}
	return check.judge(command), nil
}

// WithAdmissionHost overrides the host reader and CPU count of an enforcing policy in tests
// (order engine-hook-build-scope-gate-20261027).
func (p *Policy) WithAdmissionHost(readHost func() (sentinel.HostStats, error), cores int) *Policy {
	if p == nil || p.admission == nil {
		return p
	}
	if readHost != nil {
		p.admission.readHost = readHost
	}
	if cores > 0 {
		p.admission.numCPU = func() int { return cores }
	}
	return p
}

// WithAdmissionPullRequests overrides the default repository directory and forge pull-request
// head reader of an enforcing policy in tests.
func (p *Policy) WithAdmissionPullRequests(workDir string, read PullRequestHeadReader) *Policy {
	if p == nil || p.admission == nil {
		return p
	}
	if workDir != "" {
		p.admission.workDir = workDir
	}
	if read != nil {
		p.admission.readPullRequests = read
	}
	return p
}

// judge refuses command when one of its simple commands is a heavy step outside `praetorctl
// admit`, an admitted scope or a live-judged isolated push, exceeds the job cap, or cannot be read.
func (c *admissionCheck) judge(command string) Verdict {
	ctx, cancel := context.WithTimeout(context.Background(), pushTargetTimeout)
	defer cancel()
	return c.judgeInDir(ctx, "", command)
}

// judgeInDir evaluates simple commands with workDir as the base repository directory.
func (c *admissionCheck) judgeInDir(ctx context.Context, workDir, command string) Verdict {
	lines, ok := hiss.ShellCode(command, admissionMaxLines)
	if !ok {
		return Verdict{Outcome: Deny, Reason: refusal("HISS", "heavy-step check: command unreadable (open quote, open here-document "+
			"or over bound); split command or write it to file first")}
	}
	segments := strings.Split(segmentSeparators.Replace(strings.Join(lines, "\n")), "\n")
	if len(segments) > admissionMaxSegments {
		return Verdict{Outcome: Deny, Reason: refusal("HISS", fmt.Sprintf("heavy-step check: over %d simple commands; split command", admissionMaxSegments))}
	}
	var notice string
	for index := 0; index < len(segments) && index < admissionMaxSegments; index++ {
		reason, segNotice := c.segment(ctx, workDir, strings.Fields(segments[index]))
		notice = appendLaneNotice(notice, segNotice)
		if reason != "" {
			return Verdict{Outcome: Deny, Reason: reason, Notice: notice}
		}
	}
	return Verdict{Outcome: Allow, Notice: notice}
}

// segment reports whether one simple command is a heavy step, looking through the wrappers
// before it, or a privilege escalator, and returns the refusal and any non-blocking notice.
func (c *admissionCheck) segment(ctx context.Context, workDir string, words []string) (string, string) {
	words = words[:min(len(words), admissionMaxWords)]
	i := skipPrelude(words)
	step := pushJudgement{
		workDir:   workDir,
		pushLabel: assignedAdmissionLabel(words[:i]),
	}
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		name := commandName(words[i])
		if privilegeInvocationPattern.MatchString(strings.Join(append([]string{name}, words[i+1:]...), " ")) {
			return refusal("HISS", evasionMessage+"; privilege escalation prohibited: "+name), ""
		}
		if nextStep, isAdmit := admitStep(name, words, i); isAdmit {
			if nextStep < 0 {
				return "", ""
			}
			step.admitted, i = true, nextStep
			continue
		}
		if shape, ok := c.shape(name, words[i+1:]); ok {
			step.name, step.shape, step.args = name, shape, words[i+1:]
			return c.judgeStep(ctx, &step), step.notice
		}
		spec, wrapper := admissionWrappers[name]
		if !wrapper {
			return "", ""
		}
		next := commandAfter(words, i, spec)
		if name == "systemd-run" {
			step.sawSystemdRun = true
			step.scopeMissing = missingScopeArgs(words[i+1 : min(next, len(words))])
		}
		i = next
	}
	return "", ""
}

// admitStep reports whether the command at i is `praetorctl admit` and, when it is, the index of
// the step it admits: everything after admit's `--` (docs/guides/admission.md), so the step's job
// count is judged without a second copy of admit's flag list; -1 when admit names no step.
func admitStep(name string, words []string, i int) (int, bool) {
	if (name != "praetorctl" && name != "standardsctl") || i+1 >= len(words) || words[i+1] != "admit" {
		return 0, false
	}
	separator := slices.Index(words[i+2:], "--")
	if separator < 0 {
		return -1, true
	}
	return i + 2 + separator + 1, true
}

// judgeStep evaluates a matched heavy step against the job cap and its admitted forms.
func (c *admissionCheck) judgeStep(ctx context.Context, step *pushJudgement) string {
	if reason := c.jobCapRefusal(step.shape, step.args); reason != "" {
		return reason
	}
	if gitPushPattern.MatchString(step.shape) {
		return c.judgePush(ctx, step)
	}
	if step.admitted {
		return ""
	}
	label := stepLabel(step.name, step.shape, step.args)
	if c.scopeForm && step.sawSystemdRun {
		return c.judgeScope(step.shape, label, step.scopeMissing)
	}
	return admissionRefusal(step.shape, label)
}

// assignedAdmissionLabel reads only the prelude already identified by skipPrelude. The last
// assignment wins, as in the shell; invalid and empty labels do not admit a push.
func assignedAdmissionLabel(prelude []string) string {
	label := ""
	for i := 0; i < len(prelude) && i < admissionMaxWords; i++ {
		if value, ok := strings.CutPrefix(prelude[i], admissionLabelEnv+"="); ok {
			label = value
		}
	}
	if !labelWord.MatchString(label) {
		return ""
	}
	return label
}

// judgePush checks host admission for the push (via praetorctl admit, PRAETOR_ADMIT=<label>, or
// an admitted systemd-run scope) and then verifies that the destination branch on the remote
// forge is not headed by a MERGED or CLOSED pull request without an OPEN one.
func (c *admissionCheck) judgePush(ctx context.Context, step *pushJudgement) string {
	if reason := c.judgePushAdmission(step); reason != "" {
		return reason
	}
	return c.judgePushHead(ctx, step)
}

// judgePushAdmission verifies whether the push command has valid host admission.
func (c *admissionCheck) judgePushAdmission(step *pushJudgement) string {
	if step.admitted {
		return ""
	}
	if step.pushLabel != "" {
		return c.judgePushHost(step.shape, step.pushLabel)
	}
	if c.scopeForm && step.sawSystemdRun {
		return c.judgeScope(step.shape, "git-push", step.scopeMissing)
	}
	return admissionRefusal(step.shape, "git-push")
}

// judgePushHost admits the assignment form against live memory, swap and load readings. Unlike
// praetorctl admit it refuses immediately under pressure so the client can retry later.
func (c *admissionCheck) judgePushHost(shape, label string) string {
	stats, err := c.readHost()
	if err != nil {
		return refusal("HISS", fmt.Sprintf("heavy step isolated push host reading failed for %s (%s): %v; "+
			"retry when host readings are available (docs/guides/admission.md)", shape, label, err))
	}
	verdict := admission.Judge(stats, admission.HostCappedPolicy(stats))
	if !verdict.Admit {
		return refusal("HISS", fmt.Sprintf("heavy step isolated push refused for %s (%s): %s (readings: %s); "+
			"wait for headroom and retry (docs/guides/admission.md)", shape, label, verdict.Reason, admission.ReadingsText(stats)))
	}
	return ""
}

// judgePushHead evaluates whether git push target branches are headed by terminal pull requests.
func (c *admissionCheck) judgePushHead(ctx context.Context, step *pushJudgement) string {
	ctx, cancel := context.WithTimeout(ctx, pushTargetTimeout)
	defer cancel()
	baseDir := step.workDir
	if baseDir == "" {
		baseDir = c.workDir
	}
	targets, notice, skip, err := resolvePushTargets(ctx, baseDir, step.args)
	step.notice = appendLaneNotice(step.notice, notice)
	if err != nil {
		return refusal("HISS", fmt.Sprintf("git push target resolution failed for %s: %v; "+
			"verify repository, remote and branch before pushing (docs/guides/admission.md)", step.shape, err))
	}
	if skip {
		return ""
	}
	reader := c.readPullRequests
	if reader == nil {
		reader = defaultPullRequestHeadReader
	}
	for i := 0; i < len(targets) && i < admissionMaxWords; i++ {
		if reason := checkPushTargetHead(ctx, step.shape, targets[i], reader); reason != "" {
			return reason
		}
	}
	return ""
}

// checkPushTargetHead checks a single push target for blocking terminal pull requests.
func checkPushTargetHead(ctx context.Context, shape string, target PushTarget, reader PullRequestHeadReader) string {
	pulls, err := reader(ctx, target)
	if err != nil {
		return refusal("HISS", fmt.Sprintf("git push forge pull request check failed for %s (%s/%s branch %s): %v; "+
			"retry when the forge is reachable (docs/guides/admission.md)",
			shape, target.Remote.Owner, target.Remote.Repo, target.Branch, err))
	}
	if pr, blocked := terminalHeadPullRequest(pulls, target.Branch); blocked {
		return refusal("HISS", fmt.Sprintf("git push to branch %s on %s/%s refused: pull request #%d is %s; "+
			"push to a new branch instead (docs/guides/admission.md)",
			target.Branch, target.Remote.Owner, target.Remote.Repo, pr.Number, pr.State))
	}
	return ""
}

// terminalHeadPullRequest finds the blocking terminal pull request (preferring MERGED over CLOSED)
// heading branch, or reports false when an OPEN pull request heads it or no pull request was found.
func terminalHeadPullRequest(pulls []forge.PullRequestHead, branch string) (forge.PullRequestHead, bool) {
	var closed, merged forge.PullRequestHead
	for i := 0; i < len(pulls) && i < forge.MaxHeadPullRequests; i++ {
		pr := pulls[i]
		if pr.HeadBranch != "" && pr.HeadBranch != branch {
			continue
		}
		if isOpen, done := recordTerminalPull(pr, branch, &merged, &closed); done {
			if isOpen {
				return forge.PullRequestHead{}, false
			}
		}
	}
	return selectTerminalPull(merged, closed)
}

// recordTerminalPull records the first MERGED or CLOSED pull request, or signals an OPEN pull request.
func recordTerminalPull(pr forge.PullRequestHead, branch string, merged, closed *forge.PullRequestHead) (open, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(pr.State)) {
	case forge.PullRequestStateOpen:
		return true, true
	case forge.PullRequestStateMerged:
		if merged.Number == 0 {
			*merged = forge.PullRequestHead{Number: pr.Number, State: forge.PullRequestStateMerged, HeadBranch: branch}
		}
	case forge.PullRequestStateClosed:
		if closed.Number == 0 {
			*closed = forge.PullRequestHead{Number: pr.Number, State: forge.PullRequestStateClosed, HeadBranch: branch}
		}
	}
	return false, false
}

// selectTerminalPull returns the chosen terminal pull request, prioritizing MERGED over CLOSED.
func selectTerminalPull(merged, closed forge.PullRequestHead) (forge.PullRequestHead, bool) {
	if merged.Number > 0 {
		return merged, true
	}
	if closed.Number > 0 {
		return closed, true
	}
	return forge.PullRequestHead{}, false
}

// pushCommandSpec holds the parsed options and positional arguments of a git push invocation.
type pushCommandSpec struct {
	remote      string
	refspecs    []string
	allBranches bool
	tags        bool
	delete      bool
	skip        bool
}

// resolvePushTargets determines the repo directory, destination branches, forge details, and any
// non-blocking manifest notice for git push.
func resolvePushTargets(ctx context.Context, baseDir string, gitArgs []string) ([]PushTarget, string, bool, error) {
	repoDir, pushArgs, err := parseGitPushArgs(baseDir, gitArgs)
	if err != nil {
		return nil, "", false, err
	}
	spec, err := parsePushCommand(pushArgs)
	if err != nil || spec.skip {
		return nil, "", spec.skip, err
	}
	remoteName, branches, err := resolvePushBranches(ctx, repoDir, spec)
	if err != nil {
		return nil, "", false, err
	}
	if len(branches) == 0 {
		return nil, "", true, nil
	}
	remote, forgeKind, notice, err := resolvePushRemoteAndForge(ctx, repoDir, remoteName)
	if err != nil {
		return nil, notice, false, err
	}
	targets := make([]PushTarget, 0, len(branches))
	for i := 0; i < len(branches) && i < admissionMaxWords; i++ {
		targets = append(targets, PushTarget{
			RepoDir: repoDir,
			Remote:  remote,
			Forge:   forgeKind,
			Branch:  branches[i],
		})
	}
	return targets, notice, false, nil
}

// resolvePushBranches resolves the remote name and destination branch list for a parsed push command.
func resolvePushBranches(ctx context.Context, repoDir string, spec pushCommandSpec) (string, []string, error) {
	if spec.allBranches {
		branches, err := listAllLocalBranches(ctx, repoDir)
		return spec.remote, branches, err
	}
	if len(spec.refspecs) == 0 {
		remoteName, branch, err := resolveOmittedPushRefspec(ctx, repoDir, spec.remote)
		if err != nil {
			return "", nil, err
		}
		return remoteName, []string{branch}, nil
	}
	branches, err := resolveDestinationBranches(ctx, repoDir, spec.refspecs)
	return spec.remote, branches, err
}

// parseGitPushArgs separates the git directory flags from push subcommand arguments.
func parseGitPushArgs(baseDir string, gitArgs []string) (repoDir string, pushArgs []string, err error) {
	repoDir = baseDir
	if repoDir == "" {
		repoDir = "."
	}
	for i := 0; i < len(gitArgs) && i < admissionMaxWords; i++ {
		if gitArgs[i] == "push" {
			return repoDir, gitArgs[i+1:], nil
		}
		repoDir, i, err = stepGitOptionBeforePush(repoDir, gitArgs, i)
		if err != nil {
			return "", nil, err
		}
	}
	return "", nil, errors.New("missing push subcommand")
}

// stepGitOptionBeforePush consumes a global git flag (-C or -c) preceding the push subcommand.
func stepGitOptionBeforePush(repoDir string, gitArgs []string, i int) (string, int, error) {
	switch gitArgs[i] {
	case "-C":
		if i+1 >= len(gitArgs) {
			return "", i, errors.New("git -C requires a directory argument")
		}
		return joinRepoDir(repoDir, gitArgs[i+1]), i + 1, nil
	case "-c":
		if i+1 >= len(gitArgs) {
			return "", i, errors.New("git -c requires a configuration argument")
		}
		if err := validateGitPushConfigArg(gitArgs[i+1]); err != nil {
			return "", i, err
		}
		return repoDir, i + 1, nil
	default:
		return "", i, fmt.Errorf("unexpected git argument %q before push", gitArgs[i])
	}
}

// validateGitPushConfigArg refuses `git -c` configuration keys that can redirect or alter
// the push target (remote, remotes, url, branch, push, include, includeIf).
func validateGitPushConfigArg(raw string) error {
	key, _, _ := strings.Cut(strings.TrimSpace(raw), "=")
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(raw, "'\"$") {
		return fmt.Errorf("git -c %s overrides the push target; configure the repository instead", raw)
	}
	section, _, _ := strings.Cut(strings.ToLower(key), ".")
	switch section {
	case "remote", "remotes", "url", "branch", "push", "include", "includeif":
		return fmt.Errorf("git -c %s overrides the push target; configure the repository instead", key)
	default:
		return nil
	}
}

// joinRepoDir resolves a relative repository path against baseDir.
func joinRepoDir(baseDir, next string) string {
	if filepath.IsAbs(next) || baseDir == "" || baseDir == "." {
		return next
	}
	return filepath.Join(baseDir, next)
}

// parsePushCommand extracts the destination remote, refspecs, or skip determination from git push arguments.
func parsePushCommand(pushArgs []string) (pushCommandSpec, error) {
	var (
		positionals []string
		spec        pushCommandSpec
		endOpts     bool
	)
	for i := 0; i < len(pushArgs) && i < admissionMaxWords; i++ {
		arg := pushArgs[i]
		if !endOpts && arg == "--" {
			endOpts = true
			continue
		}
		if endOpts || !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		nextI, err := parsePushOption(pushArgs, i, &spec)
		if err != nil {
			return pushCommandSpec{}, err
		}
		i = nextI
	}
	if err := finalizePushCommand(&spec, positionals); err != nil {
		return pushCommandSpec{}, err
	}
	return spec, nil
}

// parsePushOption parses an option at index i of pushArgs, updating spec and returning the new index.
func parsePushOption(pushArgs []string, i int, spec *pushCommandSpec) (int, error) {
	arg := pushArgs[i]
	if arg == "--mirror" {
		return i, errors.New("git push --mirror rewrites every branch on the remote; push branches one by one")
	}
	if arg == "--all" || arg == "--branches" {
		spec.allBranches = true
		return i, nil
	}
	if arg == "--tags" {
		spec.tags = true
		return i, nil
	}
	if isDeleteFlag(arg) {
		spec.delete = true
		spec.skip = true
		return i, nil
	}
	return parsePushValueOption(pushArgs, i, spec)
}

// parsePushValueOption parses push options that consume a value (--repo, -o, --push-option, etc.).
func parsePushValueOption(pushArgs []string, i int, spec *pushCommandSpec) (int, error) {
	arg := pushArgs[i]
	if val, ok := strings.CutPrefix(arg, "--repo="); ok {
		spec.remote = val
		return i, nil
	}
	if arg == "--repo" {
		if i+1 >= len(pushArgs) {
			return i, errors.New("git push --repo requires a repository argument")
		}
		spec.remote = pushArgs[i+1]
		return i + 1, nil
	}
	if pushFlagTakesValue(arg) {
		if i+1 >= len(pushArgs) {
			return i, fmt.Errorf("git push %s requires an argument", arg)
		}
		return i + 1, nil
	}
	return i, nil
}

// isDeleteFlag reports whether a command-line flag denotes a delete push.
func isDeleteFlag(arg string) bool {
	if arg == "-d" || arg == "--delete" {
		return true
	}
	if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-o") {
		return false
	}
	return strings.ContainsRune(arg[1:], 'd')
}

// pushFlagTakesValue reports whether a push flag consumes the following argument as its value.
func pushFlagTakesValue(arg string) bool {
	return arg == "-o" || arg == "--push-option" || arg == "--receive-pack" || arg == "--exec"
}

// finalizePushCommand derives the remote and refspecs from positional arguments and flags.
func finalizePushCommand(spec *pushCommandSpec, positionals []string) error {
	if spec.allBranches && (spec.delete || spec.tags) {
		return errors.New("git push --all cannot be combined with --delete or --tags")
	}
	if spec.skip {
		return nil
	}
	if spec.allBranches {
		return finalizeAllBranchesPush(spec, positionals)
	}
	if len(positionals) == 0 {
		if spec.tags {
			spec.skip = true
		}
		return nil
	}
	spec.remote = positionals[0]
	refspecs, droppedTag := dropTagRefspecs(positionals[1:])
	if len(refspecs) == 0 && (spec.tags || droppedTag) {
		spec.skip = true
		return nil
	}
	spec.refspecs = refspecs
	return nil
}

// dropTagRefspecs drops each "tag <name>" pair from args and reports whether any tag pair was dropped.
func dropTagRefspecs(args []string) ([]string, bool) {
	var (
		refspecs []string
		dropped  bool
	)
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if args[i] == "tag" && i+1 < len(args) {
			dropped = true
			i++
			continue
		}
		refspecs = append(refspecs, args[i])
	}
	return refspecs, dropped
}

// finalizeAllBranchesPush validates positional arguments for a `git push --all` invocation.
func finalizeAllBranchesPush(spec *pushCommandSpec, positionals []string) error {
	if len(positionals) > 1 {
		return errors.New("git push --all cannot be combined with explicit refspecs")
	}
	if len(positionals) == 1 {
		spec.remote = positionals[0]
	}
	if spec.remote == "" {
		spec.remote = "origin"
	}
	return nil
}

// resolveOmittedPushRefspec resolves an omitted push refspec (and omitted remote) the way git
// does, via `git rev-parse --symbolic-full-name @{push}` in repoDir.
func resolveOmittedPushRefspec(ctx context.Context, repoDir, explicitRemote string) (string, string, error) {
	if !util.DirExists(repoDir) {
		return "", "", fmt.Errorf("repository directory %q does not exist", repoDir)
	}
	out, err := util.RunGit(ctx, repoDir, "rev-parse", "--symbolic-full-name", "@{push}")
	if err != nil {
		return "", "", fmt.Errorf("resolve @{push} in %q: %w", repoDir, err)
	}
	return parseSymbolicPushRef(out, repoDir, explicitRemote)
}

// parseSymbolicPushRef extracts the destination remote and branch from a `refs/remotes/<remote>/<branch>` ref.
func parseSymbolicPushRef(out, repoDir, explicitRemote string) (string, string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(out), "refs/remotes/")
	if !ok {
		return "", "", fmt.Errorf("unexpected @{push} ref %q in %q", out, repoDir)
	}
	if explicitRemote != "" && strings.HasPrefix(rest, explicitRemote+"/") {
		branch := strings.TrimPrefix(rest, explicitRemote+"/")
		if branch == "" {
			return "", "", fmt.Errorf("empty branch in @{push} ref %q in %q", out, repoDir)
		}
		return explicitRemote, branch, nil
	}
	pushRemote, pushBranch, hasSlash := strings.Cut(rest, "/")
	if !hasSlash || pushRemote == "" || pushBranch == "" {
		return "", "", fmt.Errorf("unexpected @{push} ref %q in %q", out, repoDir)
	}
	if explicitRemote != "" {
		return explicitRemote, pushBranch, nil
	}
	return pushRemote, pushBranch, nil
}

// listAllLocalBranches lists local branches under refs/heads in repoDir via git for-each-ref,
// failing closed when none exist or when the count exceeds MaxPushAllBranches.
func listAllLocalBranches(ctx context.Context, repoDir string) ([]string, error) {
	if !util.DirExists(repoDir) {
		return nil, fmt.Errorf("repository directory %q does not exist", repoDir)
	}
	countArg := fmt.Sprintf("--count=%d", MaxPushAllBranches+1)
	out, err := util.RunGit(ctx, repoDir, "for-each-ref", countArg, "--format=%(refname)", "refs/heads")
	if err != nil {
		return nil, fmt.Errorf("list local branches for git push --all in %q: %w", repoDir, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	branches := make([]string, 0, min(len(lines), MaxPushAllBranches+1))
	for i := 0; i < len(lines) && i <= MaxPushAllBranches; i++ {
		name := strings.TrimPrefix(strings.TrimSpace(lines[i]), "refs/heads/")
		if name != "" {
			branches = append(branches, name)
		}
	}
	if len(branches) == 0 {
		return nil, fmt.Errorf("git push --all found no local branches in %q", repoDir)
	}
	if len(branches) > MaxPushAllBranches {
		return nil, fmt.Errorf("git push --all has more than %d local branches in %q; push branches one by one",
			MaxPushAllBranches, repoDir)
	}
	return branches, nil
}

// resolveDestinationBranches resolves destination branch names for the given refspecs.
func resolveDestinationBranches(ctx context.Context, repoDir string, refspecs []string) ([]string, error) {
	branches := make([]string, 0, len(refspecs))
	for i := 0; i < len(refspecs) && i < admissionMaxWords; i++ {
		branch, skip, err := classifyRefspec(refspecs[i])
		if err != nil {
			return nil, err
		}
		if skip || isLocalTagOnly(ctx, repoDir, refspecs[i]) {
			continue
		}
		if branch == "HEAD" || branch == "@" {
			if branch, err = resolveCurrentBranch(ctx, repoDir); err != nil {
				return nil, err
			}
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

// classifyRefspec inspects a git refspec to extract the target branch or skip indicator.
func classifyRefspec(refspec string) (branch string, skip bool, err error) {
	spec := strings.TrimPrefix(strings.TrimSpace(refspec), "+")
	if err := validatePushRefspec(spec, refspec); err != nil {
		return "", false, err
	}
	src, dst, hasColon := strings.Cut(spec, ":")
	if hasColon && src == "" {
		return "", true, nil
	}
	target := src
	if hasColon && dst != "" {
		target = dst
	}
	if strings.HasPrefix(src, "refs/tags/") || strings.HasPrefix(target, "refs/tags/") {
		return "", true, nil
	}
	return trimBranchRefPrefix(target, refspec)
}

// validatePushRefspec rejects empty, wildcard, pattern, or negative git push refspecs.
func validatePushRefspec(spec, raw string) error {
	if spec == "" || spec == ":" {
		return fmt.Errorf("empty git push refspec %q", raw)
	}
	if strings.ContainsAny(spec, "*?['\"$") || strings.HasPrefix(spec, "^") {
		return fmt.Errorf("wildcard git push refspec %q is not supported; push branches one by one", raw)
	}
	return nil
}

// trimBranchRefPrefix strips git ref prefixes (refs/heads/, refs/for/) from the target.
func trimBranchRefPrefix(target, refspec string) (string, bool, error) {
	switch {
	case strings.HasPrefix(target, "refs/heads/"):
		target = strings.TrimPrefix(target, "refs/heads/")
	case strings.HasPrefix(target, "refs/for/"):
		target = strings.TrimPrefix(target, "refs/for/")
	case strings.HasPrefix(target, "refs/"):
		return "", false, fmt.Errorf("unsupported destination ref %q", target)
	}
	if target == "" {
		return "", false, fmt.Errorf("empty destination branch in refspec %q", refspec)
	}
	return target, false, nil
}

// isLocalTagOnly reports whether an unqualified refspec's source refers to a local git tag
// without a matching local branch.
func isLocalTagOnly(ctx context.Context, repoDir, rawRefspec string) bool {
	if strings.Contains(rawRefspec, "refs/") || !util.DirExists(repoDir) {
		return false
	}
	spec := strings.TrimPrefix(strings.TrimSpace(rawRefspec), "+")
	src, _, _ := strings.Cut(spec, ":")
	if src == "" || src == "HEAD" || src == "@" {
		return false
	}
	if _, err := util.RunGit(ctx, repoDir, "show-ref", "--verify", "--quiet", "refs/tags/"+src); err != nil {
		return false
	}
	_, headErr := util.RunGit(ctx, repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+src)
	return headErr != nil
}

// resolveCurrentBranch reads the current checked-out branch in repoDir via symbolic-ref.
func resolveCurrentBranch(ctx context.Context, repoDir string) (string, error) {
	if !util.DirExists(repoDir) {
		return "", fmt.Errorf("repository directory %q does not exist", repoDir)
	}
	branch, err := util.RunGit(ctx, repoDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve current branch in %q: %w", repoDir, err)
	}
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD in %q", repoDir)
	}
	return branch, nil
}

// resolvePushRemoteAndForge identifies the remote network details, target forge kind, and any
// non-blocking manifest notice.
func resolvePushRemoteAndForge(ctx context.Context, repoDir, remoteName string) (util.GitRemote, config.Forge, string, error) {
	if !util.DirExists(repoDir) {
		return util.GitRemote{}, "", "", fmt.Errorf("repository directory %q does not exist", repoDir)
	}
	remote, err := util.ReadGitPushRemote(ctx, repoDir, remoteName)
	if err != nil {
		return util.GitRemote{}, "", "", err
	}
	forgeKind, notice, err := resolvePushForge(ctx, repoDir, remote)
	if err != nil {
		return util.GitRemote{}, "", notice, err
	}
	return remote, forgeKind, notice, nil
}

// resolvePushForge resolves the forge kind from manifest declaration or remote hostname,
// retaining usable declarations with a notice when unrelated manifest fields cannot be decoded.
// An unknown host without a usable declaration is refused rather than assigned a guessed forge.
func resolvePushForge(ctx context.Context, repoDir string, remote util.GitRemote) (config.Forge, string, error) {
	declared, err := declaredManifestForge(ctx, repoDir)
	var notice string
	if err != nil {
		notice = fmt.Sprintf("git push target resolution: %v; using repository.forge declaration %s", err, declared)
		if declared == "" {
			notice = fmt.Sprintf("git push target resolution: %v; falling back to remote host %s", err, remote.Host)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", notice, err
		}
	}
	if declared == "" && isForgejoHost(remote.Host) {
		return config.ForgeForgejo, notice, nil
	}
	forgeKind, forgeErr := config.RepositoryForge(declared, remote.Host)
	return forgeKind, notice, forgeErr
}

// declaredManifestForge reads the forge declared in .standards.yaml if present through the
// structural decoder and a forge-only projection so unrelated policy types cannot block it.
func declaredManifestForge(ctx context.Context, repoDir string) (config.Forge, error) {
	manifestPath := filepath.Clean(filepath.Join(repoDir, config.ManifestFileName))
	data, err := readPushManifest(ctx, manifestPath, contextopt.ReadSnapshot)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", manifestPath, err)
	}
	declared, err := decodePushManifestForge(data)
	if err != nil {
		return declared, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	return declared, nil
}

// isForgejoHost checks whether a hostname indicates a Forgejo or Codeberg host.
func isForgejoHost(host string) bool {
	return host == "codeberg.org" || strings.HasPrefix(host, "forgejo.") || strings.Contains(host, ".forgejo.")
}

// judgeScope verifies a systemd-run scope wrapper and checks live host memory and swap readings
// through internal/admission (order engine-hook-build-scope-gate-20261027).
func (c *admissionCheck) judgeScope(shape, label string, missing []string) string {
	if len(missing) > 0 {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope incomplete for %s: missing %s; "+
			"run: systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no <command> or "+
			"praetorctl admit --label %s -- <command> (docs/guides/admission.md)",
			shape, strings.Join(missing, ", "), label))
	}
	stats, err := c.readHost()
	if err != nil {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope host reading failed for %s: %v (docs/guides/admission.md)", shape, err))
	}
	policy := admission.HostCappedPolicy(stats)
	policy.MaxLoad1 = 0
	verdict := admission.Judge(stats, policy)
	if !verdict.Admit {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope refused for %s: %s (readings: %s; docs/guides/admission.md)",
			shape, verdict.Reason, admission.ReadingsText(stats)))
	}
	return ""
}

// missingScopeArgs returns the required systemd-run scope arguments absent from args
// (order engine-hook-build-scope-gate-20261027).
func missingScopeArgs(args []string) []string {
	user, scope, memHigh, noExpand := false, false, false, false
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		switch args[i] {
		case "--user":
			user = true
		case "--scope":
			scope = true
		case "--expand-environment=no":
			noExpand = true
		default:
			if hasMemoryHighArg(args[i], args, i) {
				memHigh = true
			}
		}
	}
	var missing []string
	if !user {
		missing = append(missing, "--user")
	}
	if !scope {
		missing = append(missing, "--scope")
	}
	if !memHigh {
		missing = append(missing, "MemoryHigh")
	}
	if !noExpand {
		missing = append(missing, "--expand-environment=no")
	}
	return missing
}

func hasMemoryHighArg(arg string, args []string, index int) bool {
	if hasMemoryHighValue(strings.TrimPrefix(arg, "-p")) || hasMemoryHighValue(strings.TrimPrefix(arg, "--property=")) {
		return true
	}
	return (arg == "-p" || arg == "--property") && index+1 < len(args) && hasMemoryHighValue(args[index+1])
}

func hasMemoryHighValue(prop string) bool {
	val, ok := strings.CutPrefix(prop, "MemoryHigh=")
	return ok && val != ""
}

// jobCapRefusal refuses a heavy step whose -j/--jobs flag exceeds floor(cores/divisor) or is
// unbounded (order engine-hook-build-scope-gate-20261027).
func (c *admissionCheck) jobCapRefusal(shape string, args []string) string {
	raw, bare, found := findJobsArg(args)
	if !found {
		return ""
	}
	cores := max(c.numCPU(), 1)
	limit := max(cores/c.divisor, 1)
	if bare {
		return refusal("HISS", fmt.Sprintf("heavy step job count unbounded for %s: bare %s on %d cores (limit %d, cores/%d); "+
			"pass -j%d or lower (docs/guides/admission.md)", shape, raw, cores, limit, c.divisor, limit))
	}
	jobs, err := strconv.Atoi(raw)
	if err != nil || jobs < 1 || jobs > limit {
		return refusal("HISS", fmt.Sprintf("heavy step job count above cap for %s: %s jobs on %d cores (limit %d, cores/%d); "+
			"pass -j%d or lower (docs/guides/admission.md)", shape, raw, cores, limit, c.divisor, limit))
	}
	return ""
}

func findJobsArg(args []string) (value string, bare bool, found bool) {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if args[i] == "--" {
			return "", false, false
		}
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		if value, bare, found := parseJobsToken(args[i], next); found {
			return value, bare, true
		}
	}
	return "", false, false
}

func parseJobsToken(arg, next string) (value string, bare bool, found bool) {
	if arg == "-j" || arg == "--jobs" {
		if allDigits(next) {
			return next, false, true
		}
		return arg, true, true
	}
	if rest, ok := strings.CutPrefix(arg, "--jobs="); ok {
		if rest == "" {
			return "--jobs", true, true
		}
		return rest, false, true
	}
	if strings.HasPrefix(arg, "-j") && allDigits(arg[2:]) {
		return arg[2:], false, true
	}
	return "", false, false
}

func allDigits(text string) bool {
	if text == "" || len(text) > 16 {
		return false
	}
	for i := 0; i < len(text) && i < 16; i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// shape returns the matched part of the first shape the command matches.
func (c *admissionCheck) shape(name string, args []string) (string, bool) {
	text := strings.Join(append([]string{name}, args[:min(len(args), admissionShapeWords)]...), " ")
	for index := 0; index < len(c.shapes) && index < len(builtinHeavyShapes)+config.MaxAdmissionCommands; index++ {
		if match := c.shapes[index].FindString(text); match != "" {
			return strings.TrimSpace(match), true
		}
	}
	return "", false
}

// skipPrelude returns the index of the first word that is neither a variable assignment nor a
// shell keyword that may stand before a command name.
func skipPrelude(words []string) int {
	i := 0
	for i < len(words) && i < admissionMaxWords && (assignmentWord.MatchString(words[i]) || slices.Contains(admissionPrelude, words[i])) {
		i++
	}
	return i
}

// advanceOptionWord checks for options or terminator and advances the index if a value is consumed.
func advanceOptionWord(word string, j int, spec wrapperSpec) (newJ int, done bool, isOpt bool) {
	if word == "--" {
		return j, true, true
	}
	if strings.HasPrefix(word, "-") && len(word) > 1 {
		if slices.Contains(spec.values, word) {
			j++
		}
		return j, false, true
	}
	return j, false, false
}

// commandAfter returns the index of the command a wrapper at index i runs: past its options,
// the values they take, assignments and its positional words.
func commandAfter(words []string, i int, spec wrapperSpec) int {
	positional, optionsDone := spec.positional, false
	j := i + 1
	for ; j < len(words) && j < admissionMaxWords; j++ {
		word := words[j]
		if !optionsDone {
			var isOpt bool
			j, optionsDone, isOpt = advanceOptionWord(word, j, spec)
			if isOpt {
				continue
			}
		}
		switch {
		case assignmentWord.MatchString(word):
		case wrapperPositional(word, positional, spec):
			positional--
		default:
			return j
		}
	}
	return j
}

// wrapperPositional distinguishes chrt's optional priority from its command name.
func wrapperPositional(word string, remaining int, spec wrapperSpec) bool {
	return remaining > 0 && (!spec.optionalPriority || priorityWord.MatchString(word))
}

// commandName is the base name of a command word, without a Windows .exe suffix.
func commandName(word string) string {
	return strings.TrimSuffix(path.Base(strings.ReplaceAll(word, `\`, "/")), ".exe")
}

// stepLabel names the step for `praetorctl admit --label`: the command and the last word of a
// shape that spans more than one word (go-test, git-push), or else the command and its first
// plain argument (make-verify-all).
func stepLabel(name, shape string, args []string) string {
	if words := strings.Fields(shape); len(words) > 1 && labelWord.MatchString(words[len(words)-1]) {
		return name + "-" + words[len(words)-1]
	}
	for index := 0; index < len(args) && index < admissionShapeWords; index++ {
		if labelWord.MatchString(args[index]) {
			return name + "-" + args[index]
		}
		if !strings.HasPrefix(args[index], "-") {
			break
		}
	}
	return name
}

// admissionRefusal names the heavy step and the admitted form of the command.
func admissionRefusal(shape, label string) string {
	isolated := ""
	if gitPushPattern.MatchString(shape) {
		isolated = "; in a worktree-isolated session where git under a launcher is refused, run: " +
			admissionLabelEnv + "=" + label + " git push ... (live host judgment; retry under pressure)"
	}
	return refusal("HISS", "heavy step needs host admission: "+shape+"; run: praetorctl admit --label "+label+
		" -- <command>, in background; admit waits for memory, swap and load headroom"+isolated+" (docs/guides/admission.md)")
}
