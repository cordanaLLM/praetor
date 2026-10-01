// A floating promise: the call's rejection is never observed.
export function persist(save: (value: string) => Promise<void>, value: string): void {
  save(value);
}
