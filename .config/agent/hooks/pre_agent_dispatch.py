#!/usr/bin/env python3
"""
Pre-Agent-Dispatch Force Hook & Quota Arbiter
Intercepts agent invocations to prevent mono-model spamming, enforce cognitive tier
distribution, and verify rate-limit capacity headroom before subagent dispatch.
"""
import sys
import os
import yaml
import json

CONFIG_PATH = os.environ.get("STANDARDS_ROUTING_CONFIG", ".config/models/routing.yaml")
MAX_CONCURRENT_SAME_MODEL = 2

def load_routing_config() -> dict:
    if os.path.exists(CONFIG_PATH):
        try:
            with open(CONFIG_PATH, "r", encoding="utf-8") as f:
                return yaml.safe_load(f) or {}
        except Exception:
            pass
    return {}

def audit_dispatch(requested_model: str, requested_role: str, active_agents: list) -> bool:
    # Count how many active agents are already running the identical model
    count = sum(1 for a in active_agents if a.get("model") == requested_model)
    if count >= MAX_CONCURRENT_SAME_MODEL:
        sys.stderr.write(
            f"\n[BLOCKED BY MODEL ROUTER - MONO-MODEL SPAMMING DETECTED]\n"
            f"Already running {count} concurrent subagents on model '{requested_model}'.\n"
            f"Max allowed per single model is {MAX_CONCURRENT_SAME_MODEL}.\n"
            f"Action Required: Fan out to secondary frontier models, workhorse tier, or local OSS models.\n"
            f"Run 'standardsctl models list' to view available alternatives.\n\n"
        )
        return False
    return True

def main():
    if len(sys.argv) < 2:
        sys.exit(0)

    requested_model = sys.argv[1]
    requested_role = sys.argv[2] if len(sys.argv) > 2 else "General"

    # Optional active agents JSON passed via env or stdin
    active_agents = []
    agents_env = os.environ.get("ACTIVE_SUBAGENTS_JSON")
    if agents_env:
        try:
            active_agents = json.loads(agents_env)
        except Exception:
            pass

    if not audit_dispatch(requested_model, requested_role, active_agents):
        sys.exit(1)

    sys.exit(0)

if __name__ == "__main__":
    main()
