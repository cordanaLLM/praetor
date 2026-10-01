// A named no-op handler swallows the rejection as surely as an empty arrow does.
const noop = () => undefined;

export function warm(cache) {
  cache.load().catch(noop);
}
