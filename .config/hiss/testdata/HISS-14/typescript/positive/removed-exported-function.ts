// HISS-14: the exported function connect(addr: string): Conn, published in the previous
// release, has been deleted and replaced by dial. Every importer breaks at build time,
// so the public contract is not append-only. Only the commit message decides the outcome.
export interface Conn {
  addr: string;
}

export function dial(addr: string): Conn {
  return { addr };
}
