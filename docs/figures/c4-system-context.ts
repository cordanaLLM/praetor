// Praetor's system context, drawn from the code at the evidence anchors: the forge driver that
// reconciles branch protection, the runner policy that names ARC labels without dispatching jobs,
// the operational sync that prepares the fork and the .needs.yaml demand channel back (ADR-0012,
// decision 2). ARC scale sets and the GitOps Application are owner-only paths in the fork.
import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'System Context Diagram',
  alt: 'Praetor boundaries, agent interactions, git providers, and downstream infrastructure.',
  describe: [
    'demand is the .needs.yaml report praetorctl needs aggregates; sync is praetorctl operational, which prepares the fork from reviewed engine commits.',
    'The runner labels and the deployed chart meet owner-only fork paths; Praetor dispatches no jobs.',
  ],
  evidence: [
    'internal/forge/forge.go:NewForge',
    'internal/forge/forge.go:ReconcileProtection',
    'internal/runner/matrix.go:ResolveRunner',
    'cmd/standardsctl/compile_context.go:runCompileContext',
    'internal/operationalsync/overlay.go:ownerOnlyPrefixes',
    'cmd/standardsctl/audit.go:auditRunnerMatrix',
    'internal/config/hierarchy.go:DefaultRunnerPolicy',
    'cmd/standardsctl/operational.go:runOperationalSync',
    'internal/operationalsync/sync.go:Run',
    'cmd/standardsctl/needs.go:runNeeds',
    'deploy/helm/praetor/Chart.yaml:apiVersion',
  ],
  props: {
    layout: {
      direction: 'row',
      gap: 140,
      children: [
        {
          direction: 'column',
          gap: 60,
          align: 'end',
          children: [
            { id: 'developer', label: 'Engineer / Operator' },
            { id: 'agent', label: 'AI Coding Agent', sub: 'fed by compile-context' },
          ],
        },
        {
          direction: 'column',
          gap: 70,
          children: [
            {
              id: 'boundary',
              label: 'Praetor Boundary',
              children: [{ id: 'praetor', label: 'Praetor Engine', sub: 'praetorctl', width: 190 }],
            },
            { id: 'fork', label: 'Operational Fork', sub: 'operator data', width: 190 },
          ],
        },
        {
          direction: 'column',
          gap: 36,
          align: 'start',
          children: [
            { id: 'git_provider', label: 'Git Provider', sub: 'GitHub enforces' },
            { id: 'k8s_arc', label: 'ARC Runner Scale Sets', sub: 'fork deploy/arc/' },
            { id: 'gitops', label: 'GitOps Controller', sub: 'fork deploy/k8s/' },
          ],
        },
      ],
    },
    edges: [
      { from: 'developer', to: 'praetor', label: 'runs praetorctl' },
      { from: 'agent', to: 'praetor', label: 'reads AGENTS.md' },
      { from: 'fork', to: 'praetor', label: 'demand' },
      { from: 'praetor', to: 'fork', label: 'sync' },
      { from: 'praetor', to: 'git_provider', label: 'branch protection' },
      { from: 'praetor', to: 'k8s_arc', label: 'runner labels' },
      { from: 'gitops', to: 'praetor', label: 'deploys chart' },
    ],
    steps: [],
  },
} satisfies PraetorFigure;
