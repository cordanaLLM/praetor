// this.store.save is another object's method, not this method calling itself.
export class Repo {
  constructor(private readonly store: { save(item: string): void }) {}

  save(item: string): void {
    this.store.save(item);
  }
}
