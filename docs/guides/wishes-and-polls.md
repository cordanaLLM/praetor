# Wishes and polls

Praetor wishes provide a small, private ledger for collecting and reviewing
ideas about repositories, skills, templates, libraries, frameworks, and
extensions. The ledger is local and bounded. It is not a GitHub, Gitea, GitLab,
or Discord connector, and it does not claim that any external poll has been
created or read.

The core API uses requests with an `action` and mandatory `expected_revision`.
`init` is the only action that does not require an existing revision. The
commands are:

```bash
praetorctl wishes status --store /private/wishes.json
praetorctl wishes apply --store /private/wishes.json --request /private/init.json
```

The request file is strict JSON. A request can initialize the store:

```json
{
  "action": "init",
  "policy": {
    "max_wishes": 256,
    "max_polls": 128,
    "max_voters": 1024,
    "allow_vote_changes": false,
    "allow_withdrawal": true
  }
}
```

Omitting `policy` selects the defaults: at most 256 wishes, 128 polls, and
1,024 current ballots, with changes and withdrawals enabled. A supplied policy
must include all five fields. Limits can be lowered when initializing a store;
`max_voters` bounds current ballots across all polls, not distinct people.
The same actor voting on two polls consumes two slots.

The complete JSON request and persisted ledger are each limited to 1 MiB.
IDs contain 1–128 ASCII letters, digits, dots, underscores or hyphens. Human
text supports UTF-8, spaces and line breaks, up to 8,192 bytes per field.
Polls have 2–10 choices. Their definitions remain fixed after creation;
closing a poll freezes both voting and withdrawal.

A wish has an `id`, a `kind` (`repository`, `skill`, `template`, `library`,
`framework`, or `extension`), a target, title, and description. Add one with a
new expected revision:

```json
{
  "action": "add-wish", "expected_revision": 1,
  "wish": {"id":"wish-001", "kind":"template", "target":"praetor",
    "title":"Shared onboarding template", "description":"Review a reusable seed."}
}
```

Open a poll with stable choice IDs. Version 1 is single-choice:

```json
{
  "action":"open-poll", "expected_revision":2,
  "poll":{"id":"poll-001", "wish_id":"wish-001",
    "question":"Should this be prioritized?",
    "choices":[{"id":"yes","label":"Yes"},{"id":"later","label":"Later"}]}
}
```

A vote identifies only a locally trusted actor and one choice ID:

```json
{"action":"vote", "expected_revision":3, "poll_id":"poll-001",
 "actor_id":"local:reviewer-1", "choice_ids":["yes"]}
```

Vote withdrawal targets the poll and actor; it removes that actor's current
ballot. Closure targets the poll. Both are explicit requests. The status vocabulary is
`proposed`, `accepted`, `planned`, `implemented`, and `declined`; status
changes name the wish and supply the expected revision:

```json
{"action":"set-status", "expected_revision":4,
 "wish_id":"wish-001", "status":"planned"}
```

```json
{"action":"withdraw", "expected_revision":5,
 "poll_id":"poll-001", "actor_id":"local:reviewer-1"}
```

```json
{"action":"close-poll", "expected_revision":6, "poll_id":"poll-001"}
```

Revision mismatches must fail without changing the store. Read status after
every accepted change and retain the returned revision. `actor_id` is limited
to the exact `local:<id>` form in this version; it is an assertion for a local
trusted caller, not authentication. Credentials, external identities, and
public account linking belong to a later integration boundary.

Every accepted mutation advances the revision, including setting the same
status again. Replaying the original request fails with a stale revision.
Read the current state before deciding whether another request is needed.
Cooperating concurrent writers use atomic snapshot replacement with a second
comparison before publication; a competing writer receives a conflict or busy
error. This protects a trusted local store, not against an administrator editing
files outside the protocol.

The store is a mutable canonical snapshot in this phase. It is not an
append-only audit log. A later event phase may add immutable request and
observation receipts, but must preserve the snapshot revision and replay
semantics. Counts should distinguish submitted wishes, open/closed polls,
withdrawn votes, and unknown or incomplete external observations. A count is
not proof that a ballot was eligible or uniquely linked to a person.

## MCP surface

The read tool is `standards_wishes_status`; its optional `store` argument is a
private local ledger path. The update tool is `standards_wishes_update`; it
accepts an optional `store` and a required `request_json` string containing one
strict JSON request, for example:

```json
{
  "store": ".workingdir/wishes.json",
  "request_json": "{\"action\":\"init\"}"
}
```

MCP status returns the private ledger contents needed for local readback. It
does not publish the description, expose credentials, call GitHub or Discord,
or authenticate `local:<id>` actors as platform users. Keep the store path in
private ignored artifacts and treat returned descriptions and actor IDs as
private data.

The supported workflow is: initialize, add and moderate a wish, open a local
poll, collect or withdraw votes, read the status, and close the poll. Each CLI
request example above is a separate JSON file; strict decoding rejects
duplicate or unknown JSON fields. Reviewers then decide whether a wish becomes
a package or governance task. External
projections may be proposed later, but no current command sends messages,
creates polls, reads remote votes, or deploys a bot.
