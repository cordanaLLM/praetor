// Only the positive dimension. The negative dimension (NaN, an inverted range) and the
// boundary dimension (value exactly at low and at high, Number.MAX_SAFE_INTEGER) are absent.
// node --test reports a passing suite and nothing measures the missing dimensions.
import { test } from "node:test";
import assert from "node:assert";
import { clamp } from "./untested-exported-function";

test("clamp returns the value inside the range", () => {
  assert.strictEqual(clamp(5, 0, 10), 5);
});
