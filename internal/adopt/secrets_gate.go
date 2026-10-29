// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/secretscan"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/toolpins"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
	secretsassets "github.com/cordanaLLM/praetor/tools/secrets"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

// SecurityWorkflowPath is the relative path of the security scan workflow.
const SecurityWorkflowPath = ".github/workflows/security.yml"

// secretsAdoptionReason is the HISS-13 rationale recorded when adoption baselines existing secret findings.
const secretsAdoptionReason = "adoption baseline"

// reconcileSecretsGate baselines HISS-12 findings when gitleaks is on PATH (order
// engine-hiss-unenforced-checks-20261027), reconciles the standalone secrets gate program under
// tools/secrets, and renders any HISS-12 secrets exceptions from .standards.yaml into the workflow step.
func reconcileSecretsGate(ctx context.Context, s *adoptSession) error {
	if err := s.reconcileSecretsBaseline(ctx); err != nil {
		return err
	}
	tracksGo, err := apiassets.TracksModule(ctx, s.repoPath)
	if err != nil {
		return err
	}
	if !tracksGo {
		return nil
	}
	if err := s.scaffoldSecretsGateAssets(ctx); err != nil {
		return err
	}
	return s.renderSecretsExceptions(ctx)
}

func (s *adoptSession) reconcileSecretsBaseline(ctx context.Context) error {
	if _, err := exec.LookPath(secretscan.DefaultScanner); err != nil {
		line := fmt.Sprintf("HISS-12 secret scan baseline skipped: %s not found on PATH (pinned %s; install with %s)",
			secretscan.DefaultScanner, toolpins.GitleaksVersion, gitleaksInstallHint)
		s.report.recordNotApplicable("secrets", line)
		return nil
	}
	if inRepo, err := util.GitWorktreePresent(ctx, s.repoPath); err != nil || !inRepo {
		return err
	}
	scanCtx, cancel := context.WithTimeout(ctx, secretscan.ScanTimeout)
	defer cancel()
	scanner, err := secretscan.ResolveScanner(scanCtx)
	if err != nil {
		return fmt.Errorf("secrets baseline scan: %w", err)
	}
	opts := secretscan.Options{
		Dir:     s.repoPath,
		Scanner: []string{scanner},
		Run:     util.RunCommand,
	}
	if s.opts.DryRun || s.report.BaselineStatus == "existing" {
		rep, err := secretscan.Check(scanCtx, opts)
		if err != nil {
			return fmt.Errorf("secrets baseline scan: %w", err)
		}
		s.report.recordNotApplicable("secrets", fmt.Sprintf("Recorded %d HISS-12 secret finding(s) into %s (reason: %s)", len(rep.Blocking)+len(rep.Baselined), baselineFile, secretsAdoptionReason))
		return nil
	}
	return s.recordAdoptionSecretsBaseline(scanCtx, opts)
}

func (s *adoptSession) recordAdoptionSecretsBaseline(ctx context.Context, opts secretscan.Options) error {
	if !s.opts.RecordBaseline && !fileExists(filepath.Join(s.repoPath, baselineFile)) {
		rep, err := secretscan.Check(ctx, opts)
		if err != nil {
			return fmt.Errorf("secrets baseline scan: %w", err)
		}
		if len(rep.Blocking) == 0 {
			s.report.recordNotApplicable("secrets", fmt.Sprintf("Recorded 0 HISS-12 secret finding(s) into %s (reason: %s)", baselineFile, secretsAdoptionReason))
			return nil
		}
	}
	commit, err := state.RecordedCommit(ctx, s.repoPath)
	if err != nil {
		commit = ""
	}
	res, err := secretscan.RecordBaseline(ctx, secretscan.RecordOptions{
		Options:    opts,
		Reason:     secretsAdoptionReason,
		Repository: s.identity.coordinate(),
		CommitSHA:  commit,
	})
	if err != nil {
		return fmt.Errorf("record secrets baseline: %w", err)
	}
	s.report.recordNotApplicable("secrets", fmt.Sprintf("Recorded %d HISS-12 secret finding(s) into %s (reason: %s)", res.Recorded, baselineFile, secretsAdoptionReason))
	return nil
}

func (s *adoptSession) scaffoldSecretsGateAssets(ctx context.Context) error {
	names := secretsassets.Names()
	for i := 0; i < len(names) && i < secretsassets.MaxAssets; i++ {
		data, err := secretsassets.Read(names[i])
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(filepath.Join(secretsassets.Directory, names[i]))
		if _, err := s.scaffoldFile(ctx, scaffold{
			rel: rel, perm: filePerm, content: data, auditLocked: true,
			created:  "Scaffolded locked secrets gate program",
			verified: "Existing secrets gate program preserved; audit verifies canonical text",
		}); err != nil {
			return fmt.Errorf("reconcile %s: %w", rel, err)
		}
	}
	return nil
}

func (s *adoptSession) renderSecretsExceptions(ctx context.Context) error {
	full, err := repoFile(s.repoPath, SecurityWorkflowPath)
	if err != nil {
		return err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	plain, ok := secretsassets.StripSettings(string(actual))
	if !ok {
		plain = string(actual)
	}
	raw, err := s.secretsExceptions(ctx)
	if err != nil {
		return err
	}
	rendered, err := secretsassets.RenderWorkflow(plain, makeSecretsEntries(raw))
	if err != nil {
		return err
	}
	if rendered == string(actual) {
		return nil
	}
	return s.applySecretsExceptionsWorkflow(full, actual, rendered)
}

func (s *adoptSession) secretsExceptions(ctx context.Context) ([]config.Exception, error) {
	if s.policy != nil && s.policy.Manifest != nil {
		return config.ExceptionsFor(s.policy.Manifest.Exceptions, config.ExceptionRuleSecrets), nil
	}
	return config.LoadExceptionsFor(ctx, s.repoPath, config.ExceptionRuleSecrets)
}

func makeSecretsEntries(raw []config.Exception) []rules.Entry {
	var entries []rules.Entry
	for i := 0; i < len(raw) && i < rules.MaxJudgedEntries; i++ {
		entries = append(entries, rules.Entry{
			Path:        raw[i].Path,
			RuleID:      raw[i].Tool,
			Fingerprint: raw[i].Fingerprint,
			Reason:      raw[i].Reason,
			Expires:     raw[i].Expires,
			Category:    raw[i].Category,
			Entered:     raw[i].Entered,
		})
	}
	return entries
}

func (s *adoptSession) applySecretsExceptionsWorkflow(full string, actual []byte, rendered string) error {
	_, crlf := util.NormalizeLineEndings(string(actual))
	content := []byte(util.RestoreLineEndings(rendered, crlf))
	if !s.opts.DryRun {
		if err := writeRepoFile(full, content, filePerm); err != nil {
			return fmt.Errorf("write %s: %w", SecurityWorkflowPath, err)
		}
	}
	s.planDryRunWrite(SecurityWorkflowPath, content)
	s.report.recordReconciled(SecurityWorkflowPath, "Rendered SECRETS_EXCEPTIONS into secrets gate step")
	return nil
}
