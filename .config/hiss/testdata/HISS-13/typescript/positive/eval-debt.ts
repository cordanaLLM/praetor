// Real HISS-08 debt: dynamic eval of a runtime string. The script scanner reports it, so it
// enters V_total and the ratchet refuses it in a touched file.
export function run(expr: string): unknown {
  return eval(expr);
}
