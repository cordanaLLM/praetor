// The Function constructor compiles a runtime string into code.
export function compile(source) {
  return new Function("input", source);
}
