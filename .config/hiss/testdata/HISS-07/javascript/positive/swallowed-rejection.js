// A promise .catch with an empty handler swallows the rejection.
export function warm(cache) {
  cache.load().catch(() => {});
}
