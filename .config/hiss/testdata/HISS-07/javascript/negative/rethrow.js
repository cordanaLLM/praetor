// The error is wrapped with context and rethrown, and the rejection is reported.
export function parse(text) {
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new Error("parse config: " + String(error));
  }
}

export function warm(cache, report) {
  cache.load().catch((error) => report(error));
}
