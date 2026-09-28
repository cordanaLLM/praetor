// Verification ladder from editor LSP diagnostics through git hooks, gate stages, and CI PR admission,
// drawn from the code at the evidence anchors across cmd/standards-lsp, internal/gating, internal/forge, .config/lefthook, and CI workflows.
import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Verification ladder',
  alt: 'Four verification tiers from editor LSP to git hooks, local gate stages, and CI PR admission re-checking Exit-0 receipts.',
  evidence: [
    'cmd/standards-lsp/server.go:NewServer',
    '.config/lefthook/scripts/hooks.py:pre_push',
    '.config/lefthook/scripts/checks.py:run_full_gate',
    'internal/gating/pipeline.go:executeStages',
    'internal/gating/pipeline.go:runReceiptStage',
    'internal/lockdown/keys.go:LoadSigningKey',
    'internal/lockdown/keys.go:VerifyPinnedReceiptFile',
    'internal/lockdown/receipts.go:GateOutputVersion',
    'internal/forge/pr.go:ReceiptFenceToken',
    'internal/forge/pr.go:ValidatePRChecklistWithPolicy',
    'internal/forge/pr.go:verifyReceiptEnvelope',
    'internal/forge/pr.go:applyReceiptVerification',
    'cmd/standardsctl/forge.go:runForgeValidatePR',
    '.github/workflows/ci.yml:validate',
  ],
  describe: [
    'Verification escalates through four tiers: real-time editor diagnostics, local git hooks, the six-stage gate pipeline, and CI PR admission.',
  ],
  props: {
    speed: 1000,
    // The ladder reads top to bottom: tiers 1-3 run down the workstation card, tier 4 down the
    // remote card below it, and the receipt drops straight into the pull request. A hook failure
    // stops on the workstation (lefthook exits 1 before anything is pushed), so it has its own box
    // beside the hooks; the signing key sits beside the gate that loads it. Neighbouring boxes keep
    // every edge short, so no edge crosses a box or another label.
    layout: {
      direction: 'column',
      gap: 48,
      children: [
        {
          id: 'workstation',
          label: 'Local Workstation',
          direction: 'column',
          align: 'end',
          gap: 26,
          children: [
            { id: 'editor', label: '1. Editor LSP', sub: 'cmd/standards-lsp', width: 220 },
            {
              direction: 'row',
              gap: 110,
              children: [
                { id: 'blocked', label: 'Blocked locally', sub: 'commit or push refused', width: 190 },
                { id: 'hooks', label: '2. Git Hooks', sub: 'lefthook pre-commit & pre-push', width: 220 },
              ],
            },
            {
              direction: 'row',
              gap: 110,
              children: [
                { id: 'key', label: 'Signing Key', sub: 'env var or key file', shape: 'store', width: 190 },
                { id: 'gate', label: '3. Gate Pipeline', sub: '6 stages in internal/gating', width: 220 },
              ],
            },
            { id: 'receipt', label: '.standards-receipt.json', sub: 'Exit-0 receipt', shape: 'store', width: 220 },
          ],
        },
        {
          id: 'remote',
          label: 'Remote CI & Admission',
          direction: 'column',
          gap: 30,
          children: [
            { id: 'pr', label: 'Pull Request', sub: 'checklist & ```receipt block', width: 220 },
            { id: 'ci', label: '4. CI Re-check', sub: 'standardsctl forge validate-pr', width: 220 },
            {
              direction: 'row',
              gap: 24,
              children: [
                { id: 'admit', label: 'Admitted', sub: 'PR check passes', width: 170 },
                { id: 'reject', label: 'Rejected', sub: 'validate-pr exits 1', width: 170 },
              ],
            },
          ],
        },
      ],
    },
    edges: [
      { from: 'editor', to: 'hooks', label: 'save / stage' },
      { from: 'hooks', to: 'gate', label: 'commit / push' },
      { from: 'key', to: 'gate', label: 'LoadSigningKey' },
      { from: 'gate', to: 'receipt', label: 'signed v2' },
      { from: 'receipt', to: 'pr', label: 'fenced in body' },
      { from: 'pr', to: 'ci', label: 'validate-pr' },
      { from: 'ci', to: 'admit', label: 'valid receipt' },
      { id: 'hook-fail', from: 'hooks', to: 'blocked', label: 'hook exit 1' },
      { id: 'ci-fail', from: 'ci', to: 'reject', label: 'receipt invalid' },
    ],
    steps: [
      {
        label: 'clean change -> admitted',
        caption: 'A clean change passes all four tiers and is admitted by CI.',
        flow: [
          { edges: 'editor->hooks', say: 'Editor LSP reports zero diagnostics as code is written.' },
          { edges: 'hooks->gate', say: 'Pre-commit and pre-push hooks run local checks and linters.' },
          { edges: 'key->gate', say: 'Gate runs 6 stages; Ed25519 signing key is loaded.' },
          {
            edges: 'gate->receipt',
            say: 'Stage output signed; .standards-receipt.json minted for HEAD commit.',
            show: { receipt: [{ tag: 'signed', tone: 'green', text: 'praetor-gate-output/v2', mono: true }] },
          },
          { edges: 'receipt->pr', say: 'PR is opened with checked HISS boxes and fenced receipt block.' },
          { edges: 'pr->ci', say: 'CI runs standardsctl forge validate-pr on the PR description.' },
          {
            edges: 'ci->admit',
            say: 'Signature, clean worktree, and head SHA match: PR is admitted.',
            show: { admit: [{ tag: 'passed', tone: 'green', text: 'ADMITTED', meta: 'exit 0' }] },
          },
        ],
      },
      {
        label: 'a hook blocks locally',
        caption: 'A local hook blocks commit or push before CI runs.',
        flow: [
          { edges: 'editor->hooks', say: 'Change introduces an error or invariant violation.' },
          {
            edges: 'hook-fail',
            say: 'Lefthook pre-commit or pre-push hook rejects the change locally.',
            show: { blocked: [{ tag: 'failed', tone: 'orange', text: 'lefthook exit 1', meta: 'nothing pushed' }] },
          },
        ],
      },
      {
        label: 'CI rejects invalid receipt',
        caption: 'CI rejects a pull request with a missing or invalid receipt.',
        flow: [
          { edges: 'editor->hooks', say: 'Code is edited and committed.' },
          { edges: 'hooks->gate', say: 'Local checks pass.' },
          { edges: 'receipt->pr', say: 'PR submitted with missing, unpinned, or mismatched receipt.' },
          { edges: 'pr->ci', say: 'CI step runs standardsctl forge validate-pr.' },
          {
            edges: 'ci-fail',
            say: 'Receipt verification fails: commit mismatch, dirty tree, or missing block.',
            show: { reject: [{ tag: 'failed', tone: 'orange', text: 'validate-pr failed', meta: 'exit 1' }] },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
