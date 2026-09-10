#!/usr/bin/env python3
"""
Fuzzy Semantic Anti-Loop Interceptor
Detects repetitive agent reasoning loops by tracking AST diff hashes and error categories.
Terminates with exit code 42 upon >= 3 consecutive identical failure states.
"""
import sys
import os
import json
import hashlib
from typing import Dict, Any

STATE_FILE = os.environ.get("STANDARDS_LOOP_STATE_FILE", "/tmp/standards_agent_loop_state.json")
MAX_REPEATS = 3

def load_state() -> Dict[str, Any]:
    if os.path.exists(STATE_FILE):
        try:
            with open(STATE_FILE, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            pass
    return {"history": []}

def save_state(state: Dict[str, Any]):
    try:
        with open(STATE_FILE, "w", encoding="utf-8") as f:
            json.dump(state, f, indent=2)
    except Exception:
        pass

def compute_state_hash(diff_content: str, error_category: str) -> str:
    hasher = hashlib.sha256()
    # Normalize whitespace to ensure semantic hashing rather than trivial text variations
    normalized_diff = "".join(diff_content.split())
    hasher.update(normalized_diff.encode("utf-8"))
    hasher.update(error_category.strip().lower().encode("utf-8"))
    return hasher.hexdigest()

def record_and_evaluate(diff_content: str, error_category: str) -> bool:
    state_hash = compute_state_hash(diff_content, error_category)
    state = load_state()
    history = state.get("history", [])

    history.append(state_hash)
    # Keep rolling window of last 10 entries
    if len(history) > 10:
        history = history[-10:]
    state["history"] = history
    save_state(state)

    # Check if last N hashes are identical
    if len(history) >= MAX_REPEATS:
        recent = history[-MAX_REPEATS:]
        if all(h == state_hash for h in recent):
            sys.stderr.write(
                f"\n[LOOP INTERCEPTOR ACTIVATED - EXIT CODE 42]\n"
                f"Agent detected in a repeated failure loop ({MAX_REPEATS} identical cycles).\n"
                f"Error Category: {error_category}\n"
                f"Halting tool execution to prevent context exhaustion and hallucination.\n"
                f"Action Required: Revisit architectural assumptions, read authoritative documentation, or escalate to operator.\n\n"
            )
            return False
    return True

def reset_state():
    if os.path.exists(STATE_FILE):
        try:
            os.remove(STATE_FILE)
        except Exception:
            pass

def main():
    if len(sys.argv) > 1 and sys.argv[1] == "--reset":
        reset_state()
        sys.exit(0)

    # Read diff and error category from stdin or args
    error_cat = sys.argv[1] if len(sys.argv) > 1 else "UnknownError"
    diff_text = sys.stdin.read() if not sys.stdin.isatty() else ""

    if not record_and_evaluate(diff_text, error_cat):
        sys.exit(42)

    sys.exit(0)

if __name__ == "__main__":
    main()

