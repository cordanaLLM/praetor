// A local const of the same name shadows the function, so the call reaches the local.
export function resolve(key: string): string {
  const resolve = (k: string): string => k.trim();
  return resolve(key);
}
