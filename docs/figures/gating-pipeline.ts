// The gated pipeline behind `praetorctl gate run`, drawn from the code at the evidence anchors:
// the six stages and their order come from executeStages, the dry-run skips from each stage's
// cfg.dryRun branch, and the fail-closed receipt from runReceiptStage and lockdown.LoadSigningKey.
import type { PraetorFigure } from '../../tools/figures/types.ts';

const FAST = 450;

export default {
  title: 'Gated pipeline',
  alt: 'Six gate stages run in order; a failing stage rejects the change before an Exit-0 receipt is signed.',
  evidence: [
    'internal/gating/pipeline.go:executeStages',
    'internal/gating/tree.go:requireCleanTree',
    'internal/gating/prefetch.go:VerifyLockfiles',
    'internal/gating/pipeline.go:runHissStage',
    'internal/gating/pipeline.go:runSecurityStage',
    'internal/gating/pipeline.go:runTestStage',
    'internal/gating/pipeline.go:runReceiptStage',
    'internal/lockdown/keys.go:LoadSigningKey',
    'internal/lockdown/receipts.go:GateOutputVersion',
  ],
  describe: ['Only a failed stage stops the pipeline; a stage that ran nothing is skipped or not_applicable.'],
  props: {
    speed: 1100,
    layout: {
      gap: 48,
      children: [
        {
          direction: 'column',
          gap: 40,
          children: [
            { id: 'candidate', label: 'Candidate commit', sub: 'praetorctl gate run' },
            { id: 'key', label: 'Signing key', sub: 'PRAETOR_RECEIPT_KEY or key file', shape: 'store', width: 236 },
          ],
        },
        {
          id: 'pipeline',
          label: 'Gate stages, in order',
          direction: 'column',
          gap: 22,
          children: [
            { id: 's1', label: '1. Prefetch & Lockfiles', sub: '.standards.yaml, .standards.lock', width: 250 },
            { id: 's2', label: '2. HISS Invariant Scan', sub: 'ratchet against the debt baseline', width: 250 },
            { id: 's3', label: '3. Security & SCA Scan', sub: 'govulncheck, gosec', width: 250 },
            { id: 's4', label: '4. Flavor Conformance', sub: 'flavor audit', width: 250 },
            { id: 's5', label: '5. Race-Detector Tests', sub: 'go test -race in a worktree', width: 250 },
            { id: 's6', label: '6. Ed25519 Exit-0 Receipt', sub: 'signs the stage output', width: 250 },
          ],
        },
        {
          direction: 'column',
          gap: 40,
          children: [
            { id: 'reject', label: 'Rejected', sub: 'exit 1, stage named' },
            { id: 'receipt', label: '.standards-receipt.json', sub: 'Exit-0 receipt', shape: 'store' },
          ],
        },
      ],
    },
    edges: [
      { from: 'candidate', to: 's1', label: 'clean tree' },
      { from: 's1', to: 's2' },
      { from: 's2', to: 's3' },
      { from: 's3', to: 's4' },
      { from: 's4', to: 's5' },
      { from: 's5', to: 's6' },
      { from: 'key', to: 's6', label: 'LoadSigningKey' },
      { from: 's6', to: 'receipt', label: 'signed' },
      { id: 'hiss-fail', from: 's2', to: 'reject', label: 'new violation', quiet: true },
      { id: 'key-fail', from: 's6', to: 'reject', label: 'no signing key', quiet: true },
    ],
    steps: [
      {
        label: 'clean run',
        caption: 'Every stage passes and the receipt is signed.',
        flow: [
          { edges: 'candidate->s1', say: 'The tree matches HEAD.' },
          { edges: 's1->s2', say: 'Lockfiles found; modules verified.' },
          { edges: 's2->s3', say: 'No HISS violation beyond the baseline.' },
          { edges: 's3->s4', say: 'govulncheck and gosec pass.' },
          { edges: 's4->s5', say: 'Flavor conformance holds.' },
          { edges: 's5->s6', say: 'Race-detector tests pass.' },
          { edges: 'key->s6', say: 'LoadSigningKey returns the Ed25519 key.' },
          {
            edges: 's6->receipt',
            say: 'The stage output is signed for this commit.',
            show: {
              receipt: [
                { tag: 'signed', tone: 'green', text: 'praetor-gate-output/v2', mono: true },
                { text: 'commit and repository bound' },
              ],
            },
          },
        ],
      },
      {
        label: 'HISS violation',
        caption: 'A new violation fails stage 2; no later stage runs.',
        flow: [
          { edges: 'candidate->s1', say: 'The tree matches HEAD.' },
          { edges: 's1->s2', say: 'Stage 1 passes.' },
          {
            edges: 'hiss-fail',
            say: 'The ratchet finds a violation beyond the baseline; the gate exits 1.',
            show: { reject: [{ tag: 'failed', tone: 'orange', text: 'HISS Invariant Scan', meta: 'ratchet failed' }] },
          },
        ],
      },
      {
        label: 'dry run',
        caption: 'gate run --dry-run runs the read-only checks only.',
        flow: [
          { edges: 'candidate->s1', say: 'The tree may differ from HEAD.' },
          { edges: 's1->s2', say: 'Lockfiles checked; module download skipped.' },
          { edges: 's2->s3', say: 'The HISS scan runs.' },
          { edges: 's3->s4', say: 'Security scan skipped.' },
          { edges: 's4->s5', say: 'Flavor conformance runs.' },
          { edges: 's5->s6', say: 'Race-detector tests skipped.' },
          {
            say: 'No receipt is minted.',
            light: ['s6'],
            show: { receipt: [{ tag: 'skipped', tone: 'gray', text: 'no receipt', meta: 'dry run' }] },
          },
        ],
      },
      {
        label: 'no signing key',
        caption: 'Stage 6 fails closed without a key.',
        flow: [
          { edges: 'candidate->s1', say: 'Stages 1 to 5 pass.', ms: FAST },
          { edges: 's1->s2', ms: FAST },
          { edges: 's2->s3', ms: FAST },
          { edges: 's3->s4', ms: FAST },
          { edges: 's4->s5', ms: FAST },
          { edges: 's5->s6', ms: FAST },
          {
            edges: 'key-fail',
            say: 'No PRAETOR_RECEIPT_KEY and no key file: the stage fails and nothing is signed.',
            show: { reject: [{ tag: 'failed', tone: 'orange', text: 'Ed25519 Exit-0 Receipt', meta: 'cannot be signed' }] },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
