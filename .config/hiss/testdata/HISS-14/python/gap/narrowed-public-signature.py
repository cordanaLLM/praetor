"""HISS-14: publish() previously accepted (topic, payload). It now requires a leading
ctx argument, so every existing caller raises TypeError. Committed with an ordinary
"fix(api): thread a ctx through the published entry point" header nothing notices."""


def publish(ctx: dict, topic: str, payload: bytes) -> int:
    if not payload:
        return 0
    return len(topic) + int(ctx.get("deadline_ms", 0))
