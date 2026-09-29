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
settings. It does not grant bypass privileges or disable Lefthook, receipts,
lint, or tests. Unknown review modes are configuration errors.

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
parameters, and never lowers a hosted review setting: a live approving review
count above the declared one, and code-owner review, last-push approval or stale
review dismissal switched on, are kept and reported as `[STRICTER]`
([sync --remote](adoption-verification.md#writing-the-ruleset-labels-and-repository-metadata-to-github-with-sync-remote)).
Switching to `single_maintainer` therefore does not remove the approval
requirement on GitHub by itself: lower it in the hosted ruleset by hand, then run
`praetorctl plan --remote` to confirm the readback matches the declaration.
Hosted readback is required; a locally generated ruleset alone does not establish
enforcement.
