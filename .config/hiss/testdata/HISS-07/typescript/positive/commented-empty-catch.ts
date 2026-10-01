// A comment does not handle the error; the catch block is still empty.
export function tryParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch (error: unknown) {
    // ignored
  }
  return undefined;
}
