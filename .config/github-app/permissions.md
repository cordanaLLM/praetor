# cordana-standards[bot] Permissions and Scope Specification

## Purpose

`cordana-standards[bot]` is a dedicated, organization-level GitHub App designed to eliminate self-review circularity (e.g. operator accounts requesting PR approvals from themselves) and automate high-integrity repository maintenance.

## Status

Nothing provisions, installs or authenticates as this App today. No workflow, command or
runtime reads `manifest.json`, and `internal/forge/pr.go` only names `cordana-standards[bot]`
as a requested reviewer. The manifest and this matrix are the operator's input for creating
the App by hand (below); `docs/guides/operational-configuration.md` points operators here.

## Required Permissions Matrix

One row per key of `default_permissions` in `manifest.json`, at the level the manifest
requests (`write` is Read & Write, `read` is Read-only). `scripts/test_github_app_permissions.py`
(`make github-app-test`) fails when the two disagree.

| Scope | Access Level | Operational Rationale |
| :--- | :--- | :--- |
| **`checks`** | Read & Write | Publishes detailed SARIF diagnostic check runs and cryptographic Ed25519 receipts. |
| **`contents`** | Read & Write | Merges pull requests, applies automated AST refactorings, and pushes `.standards.lock` updates. |
| **`pull_requests`** | Read & Write | Opens migration campaign PRs, posts distilled review comments, and resolves semantic AST conflicts. |
| **`repository_rules`** | Read & Write | Reconciles unbypassable main-branch and release-tag rulesets declaratively. |
| **`issues`** | Read & Write | Creates and updates tracking issues, adds and removes their labels, and reconciles the label taxonomy (`CreateIssue`, `UpdateIssue`, `AddLabels`, `RemoveLabel`, `ReconcileLabels` in `internal/forge/github.go`); GitHub gates the labels endpoints on this permission. |
| **`statuses`** | Read & Write | Posts commit statuses for gate results (`PostStatusCheck` in `internal/forge/github.go`, `POST /repos/{owner}/{repo}/statuses/{sha}`). |
| **`workflows`** | Read & Write | Dispatches and audits reusable CI workflow runs across downstream consumer repositories. |
| **`metadata`** | Read-only | Queries repository details, topics, and contributor memberships. |

## 1-Click Provisioning Workflow

1. Navigate to Organization Settings $\to$ Developer settings $\to$ GitHub Apps.
2. Select **New GitHub App from manifest**.
3. Upload or paste `.config/github-app/manifest.json`.
4. Generate and download the private RSA key (`cordana-standards.pem`).
5. Record the `App ID` and `Installation ID` in your organization secrets as `STANDARDS_BOT_APP_ID` and `STANDARDS_BOT_PRIVATE_KEY`.
