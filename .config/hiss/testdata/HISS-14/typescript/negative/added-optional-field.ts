// Append-only: a new optional field on an exported interface plus a new exported
// function. Every existing caller keeps type-checking, so no breaking indicator and no
// Migration: footer are required.
export interface Conn {
  addr: string;
  deadlineMs?: number;
}

export function ping(): string {
  return "pong";
}
