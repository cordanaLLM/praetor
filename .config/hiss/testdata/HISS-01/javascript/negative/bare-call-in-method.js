// A bare call inside a method reaches the module function load, not the method itself.
function load(key) {
  return fetchValue(key);
}

export class Cache {
  load(key) {
    return load(key);
  }
}
