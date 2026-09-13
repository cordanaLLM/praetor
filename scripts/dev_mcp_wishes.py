"""Private wish and poll lifecycle through the real development MCP wire."""

import json


def wish_checks(client, root, tool_text, require):
    store = "wish-probe/wishes.json"
    read_args = {"store": store}
    tool_text(client.call("standards_wishes_status", read_args), error=True)

    def apply(request, *, error=False):
        args = dict(read_args, request_json=json.dumps(request))
        text = tool_text(client.call("standards_wishes_update", args), error=error)
        return None if error else json.loads(text)

    ledger = apply({"action": "init"})
    ledger = apply({"action": "add-wish", "expected_revision": ledger["revision"],
                    "wish": {"id": "fixture-wish", "kind": "template",
                             "target": "fixture/repo", "title": "Private template wish",
                             "description": "Disposable fixture wish content"}})
    ledger = apply({"action": "open-poll", "expected_revision": ledger["revision"],
                    "poll": {"id": "fixture-poll", "wish_id": "fixture-wish",
                             "question": "Prioritize this template?",
                             "choices": [{"id": "yes", "label": "Yes"},
                                         {"id": "later", "label": "Later"}]}})
    vote = {"action": "vote", "poll_id": "fixture-poll", "actor_id": "local:fixture",
            "choice_ids": ["yes"], "expected_revision": ledger["revision"]}
    ledger = apply(vote)
    apply(vote, error=True)
    ledger = apply(dict(vote, expected_revision=ledger["revision"], choice_ids=["later"]))
    ballots = list(ledger["current_ballots"].values())
    require(len(ballots) == 1 and ballots[0]["choice_id"] == "later",
            "vote change duplicated or lost the current ballot")
    ledger = apply({"action": "withdraw", "poll_id": "fixture-poll",
                    "actor_id": "local:fixture", "expected_revision": ledger["revision"]})
    require(not ledger["current_ballots"] and ledger["wishes"][0]["status"] == "proposed",
            "vote withdrawal did not remove only the ballot")
    ledger = apply(dict(vote, expected_revision=ledger["revision"]))
    ledger = apply({"action": "close-poll", "poll_id": "fixture-poll",
                    "expected_revision": ledger["revision"]})
    apply(dict(vote, expected_revision=ledger["revision"]), error=True)
    before = (root / store).read_bytes()
    observed = json.loads(tool_text(client.call("standards_wishes_status", read_args)))
    require(observed == ledger and (root / store).read_bytes() == before,
            "wish status disagrees with mutation result or rewrote the store")
    require(observed["polls"][0]["closed"] and len(observed["current_ballots"]) == 1,
            "closed poll lost its final ballot")
    require((root / store).stat().st_mode & 0o777 == 0o600,
            "wish store is not private")
    for args in ({"store": "../outside-wishes.json", "request_json": '{"action":"init"}'},
                 dict(read_args, request_json='{"action":"init","action":"init"}'),
                 dict(read_args, request_json='{"action":"init"}', publish=True)):
        tool_text(client.call("standards_wishes_update", args), error=True)
    require((root / store).read_bytes() == before, "rejected wish operation changed the ledger")
    return ["wish and poll lifecycle persisted and read back",
            "vote changes and withdrawals preserve wish decisions",
            "stale revisions and closed polls reject votes",
            "private wish permissions and confined strict requests verified"]
