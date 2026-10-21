# ADR-0020: Forge App Installation Tokens

## Status

Proposed — 2026-10-21.

## Context

The `gh` CLI stores its OAuth login token in the desktop session's login keyring via the Secret Service API.
When the desktop session is locked (after a system reboot before desktop login, or under a lock policy), every
unattended `gh` invocation and every `git` call using `gh auth git-credential` hangs or fails. On 2026-10-21, an
adopter workstation had its keyring locked for close to four hours; all forge-reading flows of its planning
runner stalled, and branch pushes waited.

GitHub App installation access tokens (`POST /app/installations/{installation_id}/access_tokens`) expire one
hour after creation. The endpoint accepts `repositories` or `repository_ids` (up to 500) and a `permissions`
map to scope the token; a token can never exceed the permissions granted to the installation. The `gh` CLI reads
`GH_TOKEN` (or `GITHUB_TOKEN`) from the environment ahead of any stored keyring credential
(`internal/util/util.go:ResolveAuthTokenContext`).

On Linux, `systemd-creds encrypt --user` (user-scoped credentials; checked on systemd 262) encrypts a credential
bound to the host key and, when a TPM2 chip is present, the TPM (`--with-key=host+tpm2`). A user service reads the credential via
`LoadCredentialEncrypted=` and decrypts it without access to the desktop keyring. In CI, the engine already mints
GitHub App tokens for `release-please` (`docs/guides/release-token.md`, `internal/forge/release_token.go`,
`actions/create-github-app-token`), establishing familiarity with the App delegation model.

This design must adhere to core fleet governance rules:

- Credentials of operator accounts are created, rotated, and revoked by the operator, never by an autonomous agent.
  The engine names each such act as an operator step and waits for it.
- Function permissions are computed strictly from engine call sites, never prompted interactively.
- No silent fallback: a missing credential fails immediately by name rather than falling back to an unverified source.
- Secrets must never appear in argument vectors, logs, or persistent files outside `$XDG_RUNTIME_DIR`.
- Platform neutrality (HISS-21): Linux, macOS, and Windows runners execute or skip with an explicit rationale.

## Decision

### 1. One GitHub App per adopter organisation

The operator creates and installs one GitHub App per adopter organisation, an operator step the engine names and
waits for. The App holds the union of all permissions required across engine functions.
Autonomous agents never mint or manage the App registration directly.

### 2. Key storage encrypted at rest with systemd-creds

The App private key is encrypted at rest using `systemd-creds encrypt --user` (`--with-key=host+tpm2` when a TPM2 is
detected, `--with-key=host` otherwise, printing the selected choice). Decryption occurs exclusively inside a user unit
via `LoadCredentialEncrypted=`. Plain-file private keys are rejected on startup, never permitted as a fallback.
Support for the macOS System keychain and Windows DPAPI is out of scope for this record and tracked as a named gap.

### 3. Token helper `praetorctl forge token --function=<name>`

A dedicated token helper `praetorctl forge token --function=<name>` mints an installation token scoped to the
permissions of that specific function and to its required repository set. The helper caches tokens in
`$XDG_RUNTIME_DIR` (tmpfs, mode 0600) until 10 minutes before expiry. It emits the token to stdout without
surrounding text or logging. Callers inject it into isolated subprocesses:

```bash
GH_TOKEN=$(praetorctl forge token --function=read) gh api rate_limit
```

### 4. Rights computed per function

A declarative mapping in `internal/forge/` maps each engine function to its minimal permissions:

- Read flows (`--function=read`): `metadata: read`, `contents: read`, `issues: read`, `pull_requests: read`,
  `actions: read`.
- Push flows (`--function=push`): `contents: write`.
- Pull request lifecycle (`--function=pr`): `pull_requests: write`.

A unit test inspects engine forge call sites against this mapping and fails if an operation executes without its
required permission declared in the function row.

### 5. Verification and check

In an unattended environment with the desktop keyring locked, `GH_TOKEN=$(praetorctl forge token --function=read) gh api rate_limit`
exits 0 and returns rate limit data. When an encrypted credential is absent, the helper exits non-zero and reports the
missing credential by name (`missing credential: forge-app-key`), with no fallback to `gh auth token`.

## Consequences

### Positive

- Unattended background jobs and git operations proceed reliably without a desktop session or unlocked keyring.
- Tokens follow least privilege: scoped to the invoking function and limited to targeted repositories.
- The App private key remains encrypted at rest bound to host hardware; tokens reside solely in tmpfs.
- Keeps the operator boundary: the operator creates and rotates the App key; permissions are computed, never asked.

### Negative / Trade-offs

- Requires initial operator setup to create and install the GitHub App.
- Requires a systemd with user-scoped credentials (`systemd-creds --user`) for decryption, leaving non-Linux platforms
  as a named gap.
- Short token lifespan (1 hour) requires caching logic and periodic minting.

### Neutral

- CI workflows continue using their existing Actions App token steps (`actions/create-github-app-token`).
- `internal/util/util.go:ResolveAuthTokenContext` retains its precedence ladder for explicit tokens and environment variables.

## Alternatives considered

- **Fine-grained personal access token (PAT) stored in a configuration file:** Rejected. PATs are tied to individual
  user accounts rather than organisations, cannot be narrowed per function call, and sit in plaintext on disk.
- **Automatic keyring unlock at boot (e.g. pam_keyring with an empty passphrase):** Rejected. Subverts workstation
  security policies and weakens the desktop session login boundary.
- **Status quo (relying on `gh` desktop keyring token):** Rejected. Keyring locks stall unattended runners and block
  automated background merges.

## Open questions

- **App key rotation cadence:** Which interval should prompt the operator to rotate the App private key?
- **Token audit logging:** Should token minting and function scope usage be recorded in a local journal or audit stream without logging token values?

## References

- `docs/guides/release-token.md`: Engine GitHub App token gate for release workflows.
- `internal/util/util.go`: `ResolveAuthTokenContext` credential resolution order.
- `internal/forge/release_token.go`: AST analysis of GitHub Actions token minting steps.
- GitHub REST API: `POST /app/installations/{installation_id}/access_tokens`.
- systemd documentation: `systemd-creds(1)` user credential encryption and `systemd.exec(5)` `LoadCredentialEncrypted=`.
