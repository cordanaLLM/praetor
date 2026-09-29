// A while (true) loop carries no scalar upper bound.
export function drain(queue) {
  while (true) {
    const item = queue.shift();
    if (item === undefined) {
      return;
    }
  }
}
