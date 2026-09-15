// HISS-14: publish previously had the signature (topic: string, payload: Uint8Array).
// A required leading ctx parameter was added, so every existing call site fails to
// type-check. Committed as "fix(api): thread a ctx through the published entry point"
// nothing notices: no analyzer diffs the exported surface against the last release.
export interface Ctx {
  deadlineMs: number;
}

export function publish(ctx: Ctx, topic: string, payload: Uint8Array): number {
  if (payload.length === 0) {
    return 0;
  }
  return ctx.deadlineMs + topic.length;
}
