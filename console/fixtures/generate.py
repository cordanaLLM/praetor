#!/usr/bin/env python3
"""Synthetic console fixtures: cards.json, graph.json and status.json written to console.schema.json.

Every value is made up from fixed word lists (repositories example/alpha ... example/theta, nodes N1 ..., cards C1 ...),
so a fixture never carries real planning data; tools/schemacheck/console_test.go validates each file against its
$defs entry. Output is deterministic for the same arguments, so a regenerated fixture shows as no diff.

Usage: generate.py [--out DIR] [--cards N] [--nodes N] [--sessions N]   defaults: this folder, 24, 60, 8
"""
import argparse
import json
from pathlib import Path

GENERATED_AT = "2026-01-15T09:30:00Z"
REPOS = ("example/alpha", "example/beta", "example/gamma", "example/delta", "example/epsilon", "example/zeta",
         "example/eta", "example/theta")
VERBS = ("Review", "Merge", "Approve", "Rotate", "Answer", "Check", "Renew", "Confirm")
OBJECTS = ("the release notes", "the dependency update", "the storage quota", "the test matrix", "the API token scope",
           "the backup schedule", "the docs build", "the licence notice")
STATES = ("now", "wait", "later")
WHERE = ("anywhere", "controller", "office", "home")
PROBES = ("path", "command", "repo_secret", "pr_merged", "operator-word")
STATUS = ("ready", "active", "blocked", "waiting")
CLASSES = ("survival", "continuity", "growth")
MAX_ROWS = 500  # the schema's largest maxItems


def pick(seq, i):
    return seq[i % len(seq)]


def card(i):
    """One synthetic operator card; every third carries a command, every fourth a link."""
    verb, obj = pick(VERBS, i), pick(OBJECTS, i * 3)
    row = {"id": f"C{i + 1}", "title": f"{verb} {obj}", "state": pick(STATES, i), "where": pick(WHERE, i),
           "act": f"{verb.lower()} {obj} in {pick(REPOS, i)}", "say": f"{verb.lower()} done",
           "detail": f"Synthetic card {i + 1}: {verb.lower()} {obj}. Nothing here is real data.",
           "why_now": f"{obj.capitalize()} blocks {1 + i % 4} follow-up items.",
           "consequence_if_skipped": "The follow-up items stay blocked.", "est_minutes": 1 + (i * 7) % 60,
           "source": f"fixture:{i + 1}", "probe_kind": pick(PROBES, i)}
    if i % 3 == 0:
        row["cmd"] = f"bash /opt/example/step-{i + 1}.sh --dry-run"
    if i % 4 == 0:
        row["link"] = f"https://example.invalid/{pick(REPOS, i)}/pull/{100 + i}"
    return row


def graph(nodes):
    totals = {s: {"nodes": n, "units": float(n * 3)} for s, n in
              (("waiting", 7), ("active", 12), ("ready", nodes), ("blocked", 20), ("done", 41))}
    wins = [{"kind": pick(("node", "steering", "research"), i), "id": f"N{i + 1}", "title": f"{pick(VERBS, i)} {pick(OBJECTS, i + 2)}",
             "gain": 1 + i % 5, "effort": 1 + (i * 2) % 5, "clarity": pick(("clear", "partial"), i), "reason": "synthetic",
             "model": "example-model", "repo": pick(REPOS, i)} for i in range(min(nodes, 20))]
    frontier = [{"id": f"N{i + 1}", "title": f"{pick(VERBS, i + 1)} {pick(OBJECTS, i)}", "status": pick(STATUS, i),
                 "score": round(10 - i * 0.13, 2), "goal": pick("ABCDE", i), "class": pick(CLASSES, i)} for i in range(min(nodes, 200))]
    return {"version": 1, "generated_at": GENERATED_AT, "totals": totals, "quick_wins": wins, "unestimated": 5, "frontier": frontier}


def status(sessions):
    rows = [{"repo": pick(REPOS, i), "state": pick(("active", "quiet", "not running"), i), "kind": "session", "pid": 4000 + i,
             "gb": round(0.4 + i * 0.1, 1), "idle_min": i * 4, "latest": f"REPORT example-{i + 1}: synthetic line"} for i in range(sessions)]
    decisions = [{"id": f"example-decision-{i + 1}", "kind": pick(("DECISION", "ASK"), i), "at": GENERATED_AT,
                  "from": f"session-{pick(REPOS, i).split('/')[1]}"} for i in range(4)]
    return {"version": 1, "generated_at": GENERATED_AT, "sessions": rows, "decisions": decisions,
            "holds": [{"id": "example-hold-1", "since": GENERATED_AT}]}


def write(out, cards, nodes, sessions):
    out.mkdir(parents=True, exist_ok=True)
    files = {"cards": {"version": 1, "generated_at": GENERATED_AT, "cards": [card(i) for i in range(cards)]},
             "graph": graph(nodes), "status": status(sessions)}
    for name, doc in files.items():
        (out / f"{name}.json").write_text(json.dumps(doc, indent=1) + "\n", encoding="utf-8")
    return sorted(files)


def main():
    parser = argparse.ArgumentParser(description=(__doc__ or "").splitlines()[0])
    parser.add_argument("--out", type=Path, default=Path(__file__).resolve().parent)
    for name, default in (("cards", 24), ("nodes", 60), ("sessions", 8)):
        parser.add_argument(f"--{name}", type=int, default=default, choices=range(0, MAX_ROWS + 1), metavar=f"0..{MAX_ROWS}")
    args = parser.parse_args()
    print("wrote", ", ".join(write(args.out, args.cards, args.nodes, args.sessions)), "to", args.out)


if __name__ == "__main__":
    main()
