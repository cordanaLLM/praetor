// Retrying from a promise callback by calling itself is still direct recursion.
export function retry(task, attempts) {
  return task().catch((error) => (attempts > 0 ? retry(task, attempts - 1) : Promise.reject(error)));
}
