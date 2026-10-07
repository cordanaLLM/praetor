// The forge drivers and their callers, drawn from the code at the evidence anchors: the three
// praetorctl commands that write to a forge build the GitHub driver directly
// (reconcileRemoteForge, loadFleetIssues with transitionUnblocked and planningForgeFor,
// publishEpicToForge);
// forge.NewForge, the only constructor of the GitLab and Gitea drivers, is called from tests
// alone, and those drivers check locally that a token is set and fail every enforcement method
// with ErrNotImplemented.
import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Multi-forge federation',
  alt: 'praetorctl builds only the GitHub driver; GitLab and Gitea drivers come from forge.NewForge, which only tests call.',
  evidence: [
    'cmd/standardsctl/sync.go:reconcileRemoteForge',
    'cmd/standardsctl/issue.go:loadFleetIssues',
    'cmd/standardsctl/issue.go:transitionUnblocked',
    'cmd/standardsctl/issue_planning.go:planningForgeFor',
    'internal/forge/reconciler.go:ApplyPlanning',
    'cmd/standardsctl/needs.go:publishEpicToForge',
    'internal/needs/epic.go:PublishPreMigrationEpic',
    'internal/forge/issues.go:PrepareIssueBatch',
    'internal/forge/issues.go:Ensure',
    'internal/needs/epic.go:linkEpicChildren',
    'internal/forge/github.go:GetIssue',
    'internal/forge/github.go:EditIssueBody',
    'internal/forge/github.go:NewGitHubDriver',
    'internal/forge/github.go:ReconcileProtection',
    'internal/forge/github.go:ReconcileLabels',
    'internal/forge/ruleset.go:RepositoryRulesetRefs',
    'internal/forge/github.go:AddLabels',
    'internal/forge/github.go:RemoveLabel',
    'internal/forge/forge.go:NewForge',
    'internal/forge/forge.go:ErrNotImplemented',
    'internal/forge/forge_test.go:TestNewForge_Providers_Positive',
    'internal/forge/forge_test.go:TestStubDrivers_Negative_EveryEnforcementMethodIsUnsupported',
    'internal/forge/gitlab.go:GitLabDriver',
    'internal/forge/gitea.go:GiteaDriver',
  ],
  describe: [
    'The GitHub driver also implements PostStatusCheck and CreatePullRequest; no praetorctl command calls them.',
    'issue reconcile reads and closes milestones through the milestone package, which has its own GitHub client.',
    'forge.NewForge also returns the GitHub driver for provider github; only tests call it.',
  ],
  props: {
    speed: 1100,
    layout: {
      direction: 'column',
      gap: 40,
      children: [
        {
          id: 'prod',
          label: 'praetorctl commands',
          direction: 'row',
          gap: 120,
          align: 'center',
          children: [
            {
              direction: 'column',
              gap: 24,
              children: [
                { id: 'sync', label: 'sync --remote', sub: 'reconcileRemoteForge', width: 230 },
                { id: 'issue', label: 'issue reconcile', sub: 'loadFleetIssues', width: 230 },
                { id: 'epic', label: 'needs epic --publish', sub: 'publishEpicToForge', width: 230 },
              ],
            },
            { id: 'github', label: 'GitHub driver', sub: 'NewGitHubDriver, REST client', width: 250 },
          ],
        },
        {
          id: 'testonly',
          label: 'Tests only, no production caller',
          direction: 'row',
          gap: 130,
          align: 'center',
          children: [
            { id: 'tests', label: 'forge package tests', sub: 'forge_test.go', width: 200 },
            { id: 'factory', label: 'forge.NewForge', sub: 'provider switch', width: 200 },
            {
              direction: 'column',
              gap: 24,
              children: [
                { id: 'gitlab', label: 'GitLab driver', sub: 'token check only', width: 220 },
                { id: 'gitea', label: 'Gitea driver', sub: 'gitea or forgejo', width: 220 },
              ],
            },
          ],
        },
      ],
    },
    edges: [
      { from: 'sync', to: 'github', label: 'ruleset, labels, metadata' },
      { from: 'issue', to: 'github', label: 'list, relabel, tick, close' },
      { from: 'epic', to: 'github', label: 'list, create, link' },
      { from: 'tests', to: 'factory', label: 'NewForge' },
      { from: 'factory', to: 'gitlab', label: 'gitlab' },
      { from: 'factory', to: 'gitea', label: 'gitea, forgejo' },
    ],
    steps: [
      {
        label: 'sync --remote',
        caption: 'sync --remote writes the branch ruleset, the label taxonomy and the repository metadata through the GitHub driver.',
        flow: [
          {
            edges: 'sync->github',
            say: 'reconcileRemoteForge checks the token, repository and origin, then calls forge.NewGitHubDriver.',
          },
          {
            edges: 'sync->github',
            say: 'ReconcileProtection writes the ruleset for main and lts-* and reads it back.',
            show: {
              github: [{ tag: 'call', tone: 'blue', text: 'ReconcileProtection', meta: 'sync.go', mono: true }],
            },
          },
          {
            edges: 'sync->github',
            say: 'ReconcileLabels updates, or creates, each label from .config/labels.yaml.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ReconcileProtection', meta: 'sync.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'ReconcileLabels', meta: 'sync.go', mono: true },
              ],
            },
          },
          {
            edges: 'sync->github',
            say: 'ReconcileRepositoryMetadata writes the declared description, homepage and missing topics, and reports visibility drift.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ReconcileProtection', meta: 'sync.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'ReconcileLabels', meta: 'sync.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'ReconcileRepositoryMetadata', meta: 'sync.go', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'issue reconcile',
        caption: 'issue reconcile lists the issues of each selected repository, relabels the unblocked ones and runs the planning sync.',
        flow: [
          {
            edges: 'issue->github',
            say: 'loadFleetIssues builds a GitHub driver per repository and calls ListIssues.',
            show: {
              github: [{ tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issue.go', mono: true }],
            },
          },
          {
            edges: 'issue->github',
            say: 'With --apply, transitionUnblocked calls AddLabels, then RemoveLabel.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issue.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'AddLabels, RemoveLabel', meta: 'issue.go', mono: true },
              ],
            },
          },
          {
            edges: 'issue->github',
            say: 'With --apply, ApplyPlanning re-reads each parent, ticks closed children with EditIssueBody and closes a finished epic with UpdateIssue.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issue.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'AddLabels, RemoveLabel', meta: 'issue.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'GetIssue, EditIssueBody, UpdateIssue', meta: 'issue_planning.go', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'needs epic --publish',
        caption: 'needs epic --publish creates the missing epic issues through the GitHub driver and names each child in the parent.',
        flow: [
          {
            edges: 'epic->github',
            say: 'publishEpicToForge passes the GitHub driver to needs.PublishPreMigrationEpic.',
          },
          {
            edges: 'epic->github',
            say: 'PrepareIssueBatch checks the token and lists the existing issues by title.',
            show: {
              github: [{ tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issues.go', mono: true }],
            },
          },
          {
            edges: 'epic->github',
            say: 'IssueBatch.Ensure calls CreateIssue only for a title the forge does not have yet.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issues.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'CreateIssue', meta: 'issues.go', mono: true },
              ],
            },
          },
          {
            edges: 'epic->github',
            say: 'Once every child exists, linkEpicChildren writes a task-list line per child into the parent with EditIssueBody.',
            show: {
              github: [
                { tag: 'call', tone: 'blue', text: 'ListIssues("all")', meta: 'issues.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'CreateIssue', meta: 'issues.go', mono: true },
                { tag: 'call', tone: 'blue', text: 'EditIssueBody', meta: 'epic.go', mono: true },
              ],
            },
          },
        ],
      },
      {
        label: 'GitLab and Gitea',
        caption: 'Only tests reach the GitLab and Gitea drivers, and every enforcement method fails.',
        flow: [
          {
            edges: 'tests->factory',
            say: 'forge_test.go calls forge.NewForge; no praetorctl command calls it.',
          },
          {
            edges: 'factory->gitlab',
            say: 'Provider gitlab returns a GitLabDriver; Authenticate only checks that a token is set.',
          },
          {
            edges: 'factory->gitea',
            say: 'Provider gitea or forgejo returns a GiteaDriver with the same local token check.',
          },
          {
            say: 'Every enforcement method returns ErrNotImplemented, which wraps errors.ErrUnsupported.',
            light: ['gitlab', 'gitea'],
            show: {
              gitlab: [{ tag: 'error', tone: 'orange', text: 'ErrNotImplemented', meta: 'gitlab.go', mono: true }],
              gitea: [{ tag: 'error', tone: 'orange', text: 'ErrNotImplemented', meta: 'gitea.go', mono: true }],
            },
          },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
