// Calling a function of the same name on another object is delegation, not recursion,
// and $read is a different identifier.
export function read(store, key) {
  return store.read(key) ?? $read(key);
}
