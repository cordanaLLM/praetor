// An empty catch block swallows the error: nothing handles, rethrows or wraps it.
export function parse(text) {
  try {
    return JSON.parse(text);
  } catch (error) {
  }
  return null;
}
