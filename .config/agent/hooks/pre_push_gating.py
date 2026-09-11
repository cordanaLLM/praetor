#!/usr/bin/env python3
"""
Pre-Push Gating Hook
Blocks direct, ungated pushes to 'main' without an Ed25519 Exit-0 verification receipt.
"""
import sys
import os
import subprocess

def main():
    lines = sys.stdin.readlines()
    if not lines:
        sys.exit(0)

    is_push_to_main = False
    for line in lines:
        parts = line.strip().split()
        if len(parts) >= 4:
            remote_ref = parts[2]
            if remote_ref in ("refs/heads/main", "main"):
                is_push_to_main = True
                break

    if is_push_to_main:
        print("[HISS-16 Sentinel] Intercepted direct push to 'main'. Initiating gating pipeline...")
        cmd = ["go", "run", "./cmd/standardsctl", "gate", "run", "--path=."]
        res = subprocess.run(cmd)
        if res.returncode != 0:
            sys.stderr.write(
                "\n[BLOCKED BY HISS-16] Gating pipeline rejected direct push to main!\n"
                "All changes entering main must pass hermetic gates and carry an Ed25519 Exit-0 receipt.\n"
                "Use a PR branch or AGit topic push: git push origin HEAD:refs/for/main -o topic=<issue-id>\n\n"
            )
            sys.exit(1)
        print("[HISS-16 Sentinel] Gating pipeline admitted commit with verified Ed25519 receipt.")

    sys.exit(0)

if __name__ == "__main__":
    main()
