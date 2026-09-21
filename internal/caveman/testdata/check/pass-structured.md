# The ledger of the session, which is a heading and never linted

verdict: pass. changed: internal/caveman/check.go. ran: go test ./internal/caveman/. open: none.
evidence: .workingdir/evidence/run.log sha256:0123456789ab lines:412

| Register | Where | Form |
| :--- | :--- | :--- |
| social | forge maintainer | full sentence permitted by context profile |

### [2026-09-18 05:59:36 UTC] Commit `a7c646a` on `main`
- **Activity**: resync after rebase
- **Tasks**: 130 open, 42 completed | **Open Bugs**: 569 | **Pending Questions**: 4
- **Git State**: available | **Clean**: false | **Dirty Paths**: 1

<!-- praetor-state:v1 sha256:08ef0524fb3a5a02b855b2934d5f4092792fe5c723dd7332dffb69edfe41e52c -->

<!--
A multi-line comment is written for a human reader, and it may well be the kind of prose
that the lint would reject anywhere else in the file.
-->

PRAETOR_CHECKPOINT_RESULT=due

```bash
# Note that the comment in a code block is probably prose, and it is never linted.
go test -race -count=1 ./internal/caveman
```

~~~text
The fence with tildes is code too; the lint just skips it.
~~~

Social sample, quoted for contrast:

<!-- caveman:off -->
I think the gate probably failed because the receipt was written after the sync, so it is
important to note that a resync is basically all that is needed in order to push again.
<!-- caveman:on -->

Links keep text only: [checkpoint workflow](docs/guides/checkpoint-cadence.md), see https://example.com/the/a/an/path.
