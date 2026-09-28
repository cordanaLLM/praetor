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
    'internal/gating/prefetch.go:prefetchDependencies',
    'internal/gating/pipeline.go:runPrefetchStage',
    'internal/gating/pipeline.go:runHissStage',
    'internal/gating/pipeline.go:runSecurityStage',
    'internal/gating/pipeline.go:requireScanner',
    'internal/gating/pipeline.go:runFlavorStage',
    'internal/flavor/audit.go:passingScore',
    'internal/gating/pipeline.go:runTestStage',
    'internal/gating/cargo.go:runCargoTests',
    'internal/gating/pipeline.go:runReceiptStage',
    'internal/gating/languages.go:requireVerification',
    'internal/lockdown/keys.go:LoadSigningKey',
    'internal/lockdown/receipts.go:GateOutputVersion',
  ],
  describe: [
    'Only a failed stage stops the pipeline; a stage that ran nothing is skipped or not_applicable.',
    'Stages 1, 3 and 5 run Go where a go.mod is present and Cargo where a Cargo.lock is.',
    'Stage failures: 1, a missing or empty manifest or lockfile, or a failed go mod verify, go mod download or cargo fetch --locked; ' +
      '2, debt beyond the baseline, an incomplete scan or an unreadable baseline; 3, a govulncheck, gosec or cargo audit finding, ' +
      'or a missing Go scanner or .gosec.json; 4, a flavor score below 80% or a missing template; ' +
      '5, a failing go test -race, cargo test or cargo clippy; 6, no signing key, or no toolchain stage ran for any language.',
  ],
  props: {
    speed: 1100,
    layout: {
      gap: 110,
      children: [
        { id: 'key', label: 'Signing key', sub: 'env var or key file', shape: 'store' },
        {
          gap: 240,
          children: [
            {
              direction: 'column',
              gap: 40,
              children: [
                { id: 'candidate', label: 'Candidate commit', sub: 'praetorctl gate run' },
                {
                  id: 'pipeline',
                  label: 'Gate stages',
                  direction: 'column',
                  gap: 22,
                  children: [
                    { id: 's1', label: '1. Prefetch & Lockfiles', sub: 'lockfiles, go mod verify', width: 190 },
                    { id: 's2', label: '2. HISS Invariant Scan', sub: 'debt-baseline ratchet', width: 190 },
                    { id: 's3', label: '3. Security & SCA Scan', sub: 'govulncheck, gosec', width: 190 },
                    { id: 's4', label: '4. Flavor Conformance', sub: 'flavor audit', width: 190 },
                    { id: 's5', label: '5. Race-Detector Tests', sub: 'go test -race, worktree', width: 190 },
                    { id: 's6', label: '6. Exit-0 Receipt', sub: 'Ed25519-signed output', width: 190 },
                  ],
                },
                { id: 'receipt', label: '.standards-receipt.json', sub: 'Exit-0 receipt', shape: 'store' },
              ],
            },
            { id: 'reject', label: 'Rejected', sub: 'exit 1, stage named' },
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
      { id: 's1-fail', from: 's1', to: 'reject', label: 'lockfiles' },
      { id: 'hiss-fail', from: 's2', to: 'reject', label: 'new debt' },
      { id: 's3-fail', from: 's3', to: 'reject', label: 'findings' },
      { id: 's4-fail', from: 's4', to: 'reject', label: 'below bar' },
      { id: 's5-fail', from: 's5', to: 'reject', label: 'tests fail' },
      { id: 'key-fail', from: 's6', to: 'reject', label: 'no key' },
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
            say: 'The ratchet finds a violation beyond the baseline (an incomplete scan fails too); the gate exits 1.',
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
