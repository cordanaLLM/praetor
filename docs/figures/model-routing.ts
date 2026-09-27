import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Model routing filter chain',
  alt: 'Models route filters candidates by target task, capabilities, capacity headroom, and lowest configured token cost.',
  evidence: [
    'internal/router/config.go:LoadRoutingConfigContext',
    'internal/router/config.go:ValidateRoutingConfig',
    'internal/router/labels.go:DeclaredTaskLabels',
    'internal/router/arbiter.go:NewModelCapacityArbiter',
    'internal/router/arbiter.go:ModelCapacityArbiter',
    'internal/router/task.go:SelectForTask',
    'internal/router/task.go:hasRoutingTag',
    'internal/router/task.go:hasTaskCapabilities',
    'internal/router/task.go:betterTaskRoute',
    'internal/router/capacity.go:projectTaskCapacity',
    'internal/router/capacity.go:quotaEligible',
    'cmd/standardsctl/models_route.go:handleModelsRoute',
    'cmd/standardsctl/models_route.go:modelRouteRequest',
    'cmd/standardsctl/models_route.go:modelRouteTracker',
  ],
  describe: ['Filter chain evaluates candidate tiers and models in strict sequential order without fallback escalation.'],
  props: {
    speed: 1100,
    // Edge labels sit at the middle of each edge, so the gaps beside the chain are wider than the
    // longest label: no label reaches into a box or its card. The inputs line up with the top of
    // the chain, so each meets its filter without crossing another input's edge even when the
    // cards make the chain tall. The winning route drops out of the chain's last filter into
    // Selected below it, and Excluded sits level with the middle filters, so the three rejection
    // edges run nearly straight and no edge crosses another edge's label.
    layout: {
      gap: 190,
      children: [
        {
          direction: 'row',
          gap: 190,
          align: 'start',
          children: [
            {
              direction: 'column',
              gap: 32,
              children: [
                { id: 'cli', label: 'models route', sub: 'task, tokens & capabilities', width: 230 },
                { id: 'config', label: 'routing.yaml', sub: '.config/models/routing.yaml', shape: 'store', width: 230 },
                { id: 'tracker', label: 'LimitTracker', sub: 'in-process or --usage snapshot', shape: 'store', width: 230 },
              ],
            },
            {
              direction: 'column',
              gap: 44,
              children: [
                {
                  id: 'chain',
                  label: 'Filter chain, in code order',
                  direction: 'column',
                  gap: 20,
                  children: [
                    { id: 'f_task', label: '1. hasRoutingTag', sub: 'tier.TargetTasks match', width: 250 },
                    { id: 'f_caps', label: '2. hasTaskCapabilities', sub: 'model.Capabilities match', width: 250 },
                    { id: 'f_quota', label: '3. quotaEligible', sub: 'RPM/TPM & 429 cooldown', width: 250 },
                    { id: 'f_cost', label: '4. betterTaskRoute', sub: 'lowest cost & ID tie-break', width: 250 },
                  ],
                },
                { id: 'selected', label: 'Selected TaskRoute', sub: 'model, tier, cost, headroom', width: 250 },
              ],
            },
          ],
        },
        { id: 'excluded', label: 'Excluded / Skipped', sub: 'ineligible or higher cost', width: 230 },
      ],
    },
    edges: [
      { from: 'cli', to: 'f_task', label: 'TaskRequest' },
      { from: 'config', to: 'f_task', label: 'LoadRoutingConfigContext' },
      { from: 'f_task', to: 'f_caps', label: 'task eligible' },
      { from: 'f_caps', to: 'f_quota', label: 'caps match' },
      { from: 'tracker', to: 'f_quota', label: 'usage counters' },
      { from: 'f_quota', to: 'f_cost', label: 'within quota' },
      { from: 'f_cost', to: 'selected', label: 'best route' },
      { id: 'caps-fail', from: 'f_caps', to: 'excluded', label: 'missing capability', quiet: true },
      { id: 'quota-fail', from: 'f_quota', to: 'excluded', label: 'limit or cooldown', quiet: true },
      { id: 'cost-skip', from: 'f_cost', to: 'excluded', label: 'cost or tie-break', quiet: true },
    ],
    steps: [
      {
        label: 'candidate selected',
        caption: 'Candidate passes task, capability, and quota checks, winning on configured cost.',
        flow: [
          {
            edges: ['cli->f_task', 'config->f_task'],
            say: 'LoadRoutingConfigContext loads .config/models/routing.yaml; hasRoutingTag finds candidate tiers.',
            show: {
              f_task: [
                { tag: 'candidates', tone: 'blue', text: 'midweight tier (4 models)', meta: '.config/models/routing.yaml' },
                { text: 'gpt-oss-small, Qwen3.8, qwen-2.5, codestral' },
              ],
            },
          },
          {
            edges: 'f_task->f_caps',
            say: 'hasTaskCapabilities verifies all requested capabilities are declared on candidate models.',
            show: {
              f_caps: [
                { tag: 'passed', tone: 'green', text: 'gpt-oss-small, Qwen3.8-27B', meta: '.config/models/routing.yaml' },
                { tag: 'passed', tone: 'green', text: 'qwen-2.5-coder, codestral-2501', meta: '.config/models/routing.yaml' },
              ],
            },
          },
          {
            edges: ['f_caps->f_quota', 'tracker->f_quota'],
            say: 'quotaEligible checks projected RPM, TPM, and ensures no active 30s 429 cooldown.',
            show: {
              f_quota: [
                { tag: 'headroom', tone: 'green', text: 'projected usage within limits', meta: 'LimitTracker' },
                { text: '50000 RPM / 20M TPM limit' },
              ],
            },
          },
          {
            edges: 'f_quota->f_cost',
            say: 'betterTaskRoute compares estimated token costs to find the lowest-cost candidate.',
            show: {
              f_cost: [
                { tag: 'eval', tone: 'blue', text: 'gpt-oss-small: $0.00', meta: '$0/$0 per M' },
                { tag: 'eval', tone: 'blue', text: 'Qwen3.8-27B: $0.00', meta: '$0/$0 per M' },
                { tag: 'eval', tone: 'gray', text: 'qwen-2.5-coder: $0.0005', meta: '$0.20/$0.60 per M' },
                { tag: 'eval', tone: 'gray', text: 'codestral-2501: $0.00075', meta: '$0.30/$0.90 per M' },
              ],
            },
          },
          {
            edges: 'f_cost->selected',
            say: 'gpt-oss-small wins with lowest configured cost ($0) and ID tie-break; TaskRoute is returned.',
            show: {
              selected: [
                { tag: 'selected', tone: 'green', text: 'gpt-oss-small', meta: '.config/models/routing.yaml', mono: true },
                { tag: 'cost', tone: 'blue', text: '$0.00 estimated cost', mono: true },
                { text: 'tier: midweight, basis: lowest configured cost' },
              ],
            },
          },
        ],
      },
      {
        label: 'capability missing',
        caption: 'A candidate lacking requested capabilities is excluded by hasTaskCapabilities.',
        flow: [
          {
            edges: ['cli->f_task', 'config->f_task'],
            say: 'The request requires specific capabilities: tools, json.',
            show: {
              f_task: [
                { tag: 'request', tone: 'blue', text: 'task: implement, caps: tools, json', meta: 'TaskRequest' },
              ],
            },
          },
          {
            edges: 'f_task->f_caps',
            say: 'hasTaskCapabilities checks each model in the tier against required capabilities.',
            show: {
              f_caps: [
                { tag: 'eval', tone: 'orange', text: 'cheap-incapable', meta: 'internal/router/task_test.go' },
                { text: 'declared: [tools]; missing: json' },
              ],
            },
          },
          {
            edges: 'caps-fail',
            say: 'Candidate missing requested capabilities is excluded from further evaluation.',
            show: {
              excluded: [
                { tag: 'excluded', tone: 'orange', text: 'cheap-incapable', meta: 'internal/router/task_test.go', mono: true },
                { tag: 'reason', tone: 'gray', text: 'missing capability: json', mono: true },
                { text: 'hasTaskCapabilities returns false; candidate dropped' },
              ],
            },
          },
        ],
      },
      {
        label: 'limit or cooldown',
        caption: 'A candidate exceeding projected capacity or in 429 cooldown is skipped.',
        flow: [
          {
            edges: ['cli->f_task', 'f_task->f_caps'],
            say: 'Candidate matches declared target task and requested capabilities.',
            show: {
              f_caps: [
                { tag: 'eligible', tone: 'green', text: 'cheap', meta: 'internal/router/task_test.go' },
              ],
            },
          },
          {
            edges: ['f_caps->f_quota', 'tracker->f_quota'],
            say: 'projectTaskCapacity projects +1 RPM and request TPM against exhaustion threshold.',
            show: {
              f_quota: [
                { tag: 'counter', tone: 'orange', text: 'current: 8 RPM, projected: 9 RPM', meta: '10 RPM limit' },
                { text: 'projected 9 > 8 (80% of 10)' },
              ],
            },
          },
          {
            edges: 'quota-fail',
            say: 'quotaEligible returns false; over-limit or cooling candidate is skipped.',
            show: {
              excluded: [
                { tag: 'skipped', tone: 'orange', text: 'cheap', meta: 'internal/router/task_test.go', mono: true },
                { tag: 'reason', tone: 'gray', text: 'projected RPM > 80% of limit, or >= limit', mono: true },
                { text: 'active 429 cooldown (< 30s) also skips candidate' },
              ],
            },
          },
        ],
      },
      {
        label: 'cost tie-break',
        caption: 'Equal configured costs tie-break deterministically by model ID, then tier.',
        flow: [
          {
            edges: ['cli->f_task', 'f_task->f_caps', 'f_caps->f_quota'],
            say: 'Multiple candidates in midweight tier pass task, capability, and quota checks.',
            show: {
              f_quota: [
                { tag: 'candidates', tone: 'blue', text: 'gpt-oss-small & Qwen3.8-27B', meta: '.config/models/routing.yaml' },
                { text: 'both have $0 input and $0 output cost' },
              ],
            },
          },
          {
            edges: 'f_quota->f_cost',
            say: 'betterTaskRoute detects equal estimated cost ($0.00) and evaluates model ID.',
            show: {
              f_cost: [
                { tag: 'tie', tone: 'purple', text: 'cost tie: $0.00 == $0.00', meta: 'betterTaskRoute' },
                { text: 'gpt-oss-small < hf.co/unsloth/Qwen3.8...' },
              ],
            },
          },
          {
            edges: ['f_cost->selected', 'cost-skip'],
            say: 'Lexicographically smaller model ID wins the deterministic tie-break.',
            show: {
              selected: [
                { tag: 'selected', tone: 'green', text: 'gpt-oss-small', meta: '.config/models/routing.yaml', mono: true },
                { tag: 'tie-break', tone: 'purple', text: 'lexicographical model ID wins', mono: true },
              ],
              excluded: [
                { tag: 'tie-lost', tone: 'gray', text: 'hf.co/unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_M', meta: '.config/models/routing.yaml', mono: true },
              ],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
