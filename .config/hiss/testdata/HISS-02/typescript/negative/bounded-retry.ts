// A retry loop bounded by a scalar attempt count.
export async function retry(task: () => Promise<boolean>, attempts: number): Promise<boolean> {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    if (await task()) {
      return true;
    }
  }
  return false;
}
