## Operational Rules

1. **State Ledger Discipline (HISS-17)**:
   Agents MUST maintain the local `.workingdir` session state ledger on every turn. Never stage
   its contents, including with force. Publish reviewed documentation under `docs/` instead.
   At the end of the turn, execute the sync:
   ```bash
   praetorctl state sync .
   ```

2. **No Evasion Tolerated**:
   Do not attempt `--no-verify` or modifying `.git/hooks`. Follow the
   [checkpoint workflow](docs/guides/checkpoint-cadence.md) for policy and blockers.

<!-- praetor:register:start -->
<!-- praetor:register:end -->
