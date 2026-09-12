# User Decisions & Questions Buffer

> Questions buffered by autonomous agents for operator review.

| ID | Question | Options | Status | Decision |
| :--- | :--- | :--- | :--- | :--- |
| `Q-001` | ADR-0008: ratify the single-dependency interpretation - kin-openapi confined to a separate tools/specc Go module; root go.mod stays at gopkg.in/yaml.v3 only | accept, reject, alternative | pending |  |
| `Q-002` | ADR-0008: supersede-vs-fix sequencing for the ~30 hand-written forge transport findings (the G07a/G07b fixes are already done on their branches and serve as the differential-test oracle for milestone 1) | merge fixes now and supersede later, hold the fixes, other | pending |  |
| `Q-003` | ADR-0008: confirm GitLab and Forgejo v1 scope after the Phase 0 spec audits (GitLab openapi_v3.yaml has documented coverage gaps; Forgejo is Swagger 2.0 only) | GitHub first then others after milestone 1, all three in v1, GitHub and GitLab | pending |  |
