# cordana-standards[bot] Permissions and Scope Specification

## Purpose
`cordana-standards[bot]` is a dedicated, organization-level GitHub App designed to eliminate self-review circularity (e.g. operator accounts requesting PR approvals from themselves) and automate high-integrity repository maintenance.

## Required Permissions Matrix

| Scope | Access Level | Operational Rationale |
| :--- | :--- | :--- |
| **`checks`** | Read & Write | Publishes detailed SARIF diagnostic check runs and cryptographic Ed25519 receipts. |
| **`contents`** | Read & Write | Merges pull requests, applies automated AST refactorings, and pushes `.standards.lock` updates. |
| **`pull_requests`** | Read & Write | Opens migration campaign PRs, posts distilled review comments, and resolves semantic AST conflicts. |
| **`repository_rules`** | Read & Write | Reconciles unbypassable main-branch and release-tag rulesets declaratively. |
| **`workflows`** | Read & Write | Dispatches and audits reusable CI workflow runs across downstream consumer repositories. |
| **`metadata`** | Read-only | Queries repository details, topics, and contributor memberships. |

## 1-Click Provisioning Workflow
1. Navigate to Organization Settings $\to$ Developer settings $\to$ GitHub Apps.
2. Select **New GitHub App from manifest**.
3. Upload or paste `.config/github-app/manifest.json`.
4. Generate and download the private RSA key (`cordana-standards.pem`).
5. Record the `App ID` and `Installation ID` in your organization secrets as `STANDARDS_BOT_APP_ID` and `STANDARDS_BOT_PRIVATE_KEY`.

