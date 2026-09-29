// A class field holding an arrow function is reached through this.
export class Poller {
  poll = async (attempt: number): Promise<void> => {
    if (attempt > 0) {
      await this.poll(attempt - 1);
    }
  };
}
