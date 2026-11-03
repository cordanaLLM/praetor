// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Match the launcher's line-leading declarations, including shell assignments. Restrict
// rewrites to commit ids; release tags and moving branches belong to the adopter.
const workflowPinPattern = `(?m)^[\t ]*(%s)[\t ]*[:=][\t ]*["']?(?<currentDigest>%s)["']?[\t ]*(#[^\r\n]*)?\r?$`

var workflowPinDeclaration = regexp.MustCompile(fmt.Sprintf(workflowPinPattern, "PRAETOR_COMMIT|PRAETOR_REF", `[0-9a-fA-F]{7,40}`))

var fullEngineCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type workflowEnginePin struct {
	file, name, sha  string
	line, start, end int
	data             []byte
}

// String formats the workflow engine pin location, variable name, and SHA target.
func (p workflowEnginePin) String() string {
	return fmt.Sprintf("%s:%d %s=%s", p.file, p.line, p.name, p.sha)
}

// readWorkflowEnginePins scans workflow manifests in .github/workflows for Praetor engine commit declarations.
func readWorkflowEnginePins(ctx context.Context, s *adoptSession) ([]workflowEnginePin, error) {
	dir, err := repoFile(s.repoPath, ghworkflow.Dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read engine pins in %s: %w", ghworkflow.Dir, err)
	}
	paths, err := workflowEnginePinPaths(entries)
	if err != nil {
		return nil, err
	}
	pins := []workflowEnginePin{}
	for i := 0; i < len(paths) && i < ghworkflow.MaxFiles; i++ {
		data, _, err := observeAdoptionInput(ctx, s, paths[i])
		if err != nil {
			return nil, err
		}
		pins = append(pins, workflowPinsIn(paths[i], data)...)
		if len(pins) > contextopt.MaxSourceBytes {
			return nil, fmt.Errorf("engine pin discovery exceeds %d declarations", contextopt.MaxSourceBytes)
		}
	}
	return pins, nil
}

// workflowEnginePinPaths filters directory entries to workflow manifests under .github/workflows and enforces ghworkflow.MaxFiles.
func workflowEnginePinPaths(entries []os.DirEntry) ([]string, error) {
	paths := []string{}
	for i := 0; i < len(entries) && i < contextopt.MaxSourceBytes; i++ {
		rel := ghworkflow.Dir + "/" + entries[i].Name()
		if entries[i].IsDir() || !ghworkflow.IsWorkflowPath(rel) {
			continue
		}
		if len(paths) >= ghworkflow.MaxFiles {
			return nil, fmt.Errorf("engine pin discovery exceeds %d workflow entries", ghworkflow.MaxFiles)
		}
		paths = append(paths, rel)
	}
	return paths, nil
}

// workflowPinsIn locates PRAETOR_COMMIT and PRAETOR_REF declarations and byte offsets in workflow content.
func workflowPinsIn(rel string, data []byte) []workflowEnginePin {
	pins := []workflowEnginePin{}
	matches := workflowPinDeclaration.FindAllSubmatchIndex(data, -1)
	for i := 0; i < len(matches) && i < contextopt.MaxSourceBytes; i++ {
		m := matches[i]
		name, sha := string(data[m[2]:m[3]]), string(data[m[4]:m[5]])
		if name == "PRAETOR_COMMIT" && (len(sha) != 40 || sha != strings.ToLower(sha)) {
			continue
		}
		pins = append(pins, workflowEnginePin{file: rel, name: name, sha: sha,
			line: bytes.Count(data[:m[0]], []byte("\n")) + 1, start: m[4], end: m[5], data: data})
	}
	return pins
}

// reconcileWorkflowEnginePin verifies and advances declared workflow engine commit pins to the adopting commit.
func reconcileWorkflowEnginePin(ctx context.Context, s *adoptSession) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	// Commit inspection must stay local, including in a partial clone, and must judge the
	// recorded DAG rather than replace refs. These overrides do not alter credential settings.
	ctx, err := util.WithCommandEnvironment(ctx, util.InheritedEnvironment([]string{
		"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1",
	}))
	if err != nil {
		return err
	}
	pins, err := readWorkflowEnginePins(ctx, s)
	if err != nil {
		return err
	}
	if len(pins) == 0 {
		s.report.recordNotApplicable(ghworkflow.Dir, "No workflow engine commit pin declared; untouched")
		return nil
	}
	if err := singleWorkflowEnginePin(pins); err != nil {
		return err
	}
	if s.opts.LockSourceRoot == "" {
		recordUnevaluatedWorkflowPins(s, pins, "moving it needs the adopting engine Git source")
		return nil
	}
	head, err := adoptingEngineCommit(ctx, s)
	if err != nil {
		recordUnevaluatedWorkflowPins(s, pins, err.Error()+"; pin untouched")
		return nil
	}
	old, err := resolveWorkflowEngineCommit(ctx, s.opts.LockSourceRoot, pins[0].sha)
	if err != nil {
		return fmt.Errorf("engine pin %s: %w", pins[0], err)
	}
	if old == head {
		s.report.recordSkipped(pins[0].file, "Engine pin already at adopting commit "+head+"; untouched")
		return nil
	}
	if err := ancestorWorkflowEnginePin(ctx, s.opts.LockSourceRoot, pins[0], head); err != nil {
		return err
	}
	return publishWorkflowEnginePins(ctx, s, pins, head)
}

// recordUnevaluatedWorkflowPins records skipped status notes for each unevaluated engine pin declaration.
func recordUnevaluatedWorkflowPins(s *adoptSession, pins []workflowEnginePin, reason string) {
	for i := 0; i < len(pins) && i < contextopt.MaxSourceBytes; i++ {
		s.report.recordSkipped(pins[i].file, fmt.Sprintf("Engine pin %s not evaluated: %s; "+
			"rerun with --lock-source-root=<engine checkout> using a Git checkout of the adopting engine", pins[i], reason))
	}
}

// singleWorkflowEnginePin ensures all discovered engine pins reference the same commit SHA before advancing.
func singleWorkflowEnginePin(pins []workflowEnginePin) error {
	declarations := make([]string, 0, len(pins))
	different := false
	for i := 0; i < len(pins) && i < contextopt.MaxSourceBytes; i++ {
		declarations = append(declarations, pins[i].String())
		different = different || !strings.EqualFold(pins[i].sha, pins[0].sha)
	}
	if different {
		return fmt.Errorf("several different engine pins declared; refusing to move: %s", strings.Join(declarations, "; "))
	}
	return nil
}

// adoptingEngineCommit determines and resolves the target engine commit from adoption session options.
func adoptingEngineCommit(ctx context.Context, s *adoptSession) (string, error) {
	ref := s.opts.Engine.Revision
	if ref == "" {
		ref = s.opts.Engine.Release
	}
	if ref == "" {
		ref = "HEAD"
	}
	return resolveWorkflowEngineCommit(ctx, s.opts.LockSourceRoot, ref)
}

// resolveWorkflowEngineCommit verifies and resolves a Git revision into a full 40-character commit SHA.
func resolveWorkflowEngineCommit(ctx context.Context, source, ref string) (string, error) {
	// ResolveGitCommit selects an isolated Git configuration. This path must preserve credential
	// configuration, so it uses the same bounded runner without those configuration overrides.
	out, err := util.RunGitBytes(ctx, source, 4096, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve engine commit %s in %s: %w", ref, source, util.CommandDiagnostic(err, out.Stderr))
	}
	head := strings.TrimSpace(string(out.Stdout))
	if !fullEngineCommit.MatchString(head) {
		return "", fmt.Errorf("adopting engine did not resolve to a full commit: %q", head)
	}
	return head, nil
}

// ancestorWorkflowEnginePin verifies that the existing engine pin is an ancestor of the target adopting commit.
func ancestorWorkflowEnginePin(ctx context.Context, source string, pin workflowEnginePin, head string) error {
	_, err := util.RunGitBytes(ctx, source, 4096, "merge-base", "--is-ancestor", pin.sha, head)
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return fmt.Errorf("engine pin %s is not an ancestor of adopting engine %s; refusing diverged pin", pin, head)
	}
	return fmt.Errorf("cannot establish ancestry of engine pin %s against %s: %w", pin, head, err)
}

// publishWorkflowEnginePins applies updated engine commit SHAs to workflow manifests in place.
func publishWorkflowEnginePins(ctx context.Context, s *adoptSession, pins []workflowEnginePin, head string) error {
	// Apply offsets backwards so repeated declarations in one file keep their original positions.
	updates := make(map[string][]byte)
	for i := len(pins) - 1; i >= 0 && i < contextopt.MaxSourceBytes; i-- {
		pin := pins[i]
		data, found := updates[pin.file]
		if !found {
			data = append([]byte(nil), pin.data...)
		}
		// Only the value span may change; comments and all surrounding bytes are preserved.
		updates[pin.file] = append(append(append([]byte(nil), data[:pin.start]...), head...), data[pin.end:]...)
	}
	for i := 0; i < len(pins) && i < contextopt.MaxSourceBytes; i++ {
		pin := pins[i]
		data, found := updates[pin.file]
		if !found {
			continue
		}
		if !s.opts.DryRun {
			full := filepath.Join(s.repoPath, filepath.FromSlash(pin.file))
			if err := contextopt.ReplaceSnapshot(ctx, full, data, contextopt.ReplaceOptions{Expected: pin.data, Exists: true, Mode: filePerm}); err != nil {
				return fmt.Errorf("move engine pin in %s: %w", pin.file, err)
			}
		}
		s.planDryRunWrite(pin.file, data)
		s.report.recordReconciled(pin.file, fmt.Sprintf("Moved engine pin %s -> %s (ancestry verified in %s)", pin.sha, head, s.opts.LockSourceRoot))
		delete(updates, pin.file)
	}
	return nil
}
