// Every error is handled: logged with context, or turned into a typed result.
export function tryParse(text: string, log: (message: string) => void): unknown {
  try {
    return JSON.parse(text);
  } catch (error: unknown) {
    log("parse failed: " + String(error));
    return undefined;
  }
}
