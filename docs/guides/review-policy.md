# Independent review and single-maintainer operation

Branch protection normally requires independent review. Its default
`review_mode` is `independent`, including when the field is omitted. Numeric
reviewer overrides retain their existing minimum semantics.

An owner can explicitly select `single_maintainer` when no other eligible
maintainer or review bot is available:

```yaml
overrides:
  branch_protection:
    required_approving_reviewers: 1
    review_mode: single_maintainer
```

The configured reviewer minimum is retained. This mode renders zero required
approvals and disables the code-owner approval requirement. Pull requests,
review-thread resolution, required status checks, signed commits, linear history,
deletion protection, and force-push protection remain governed by their existing
settings. It does not disable Lefthook, receipts, lint, or tests. Unknown review
modes are configuration errors.

The mode also renders one bypass actor: the repository admin role
(`actor_type: RepositoryRole`, `actor_id: 5`) in bypass mode `pull_request`. The
one maintainer can then merge a pull request whose rules cannot be met, such as a
required check that no run reports, and still cannot push to the branch past the
rules. Every other review mode renders no bypass actor
(`forge.rulesetBypassActors` in `internal/forge/ruleset.go`,
`TestRenderRepositoryRuleset_BypassFollowsReviewMode`). `sync --remote` writes the
entry only into a ruleset it creates. A live ruleset keeps its own bypass actors,
so add or remove one there by hand
(`TestGitHubDriver_ReconcileProtection_BypassOnlyInANewRuleset`).

An adopter might configure this temporary mode when a single author
is currently the only eligible repository account and no review bot is installed.
Local agent review remains useful evidence but does not count as an independent
forge approval.

Restore `review_mode: independent` when a second maintainer or review bot has
the required access and can submit an approving review. Regenerate the ruleset,
verify it against the manifest, reconcile the hosted review settings, and read
them back. The retained `required_approving_reviewers` value restores the minimum.
Do not infer restoration merely from installing an app or adding an account.

`praetorctl sync --remote` preserves existing ruleset branch scopes and additional
parameters, and sets the review settings it renders to the declared values. Switching
to `single_maintainer` therefore reconciles the hosted ruleset to zero required
approvals and no code-owner approval; sync prints each setting it lowers as
`[LOWERED]` and then reads the branch protection back
([sync --remote](adoption-verification.md#writing-the-ruleset-labels-and-repository-metadata-to-github-with-sync-remote),
`TestGitHubDriver_ReconcileProtection_Boundary_SingleMaintainerLowersLiveReviews`).
An approval requirement that another ruleset or legacy branch protection sets stays
and reads back as `[STRICTER]`; remove it on GitHub by hand, then run
`praetorctl plan --remote` to confirm the readback matches the declaration.
Hosted readback is required; a locally generated ruleset alone does not establish
enforcement.
