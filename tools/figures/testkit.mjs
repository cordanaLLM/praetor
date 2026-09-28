// Helpers the figure tests share (figures.test.mjs, checks.test.mjs): a temporary directory that is
// always removed, a writer that creates parent directories, and console capture. Only the tests
// import this module, and its name matches none of the file patterns `node --test` runs.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

/**
 * Runs `fn` with a fresh temporary directory and removes the directory afterwards: when `fn`
 * returns, when it throws, and, for an async `fn`, when its promise settles.
 */
export function withTempDir(fn) {
  const dir = mkdtempSync(join(tmpdir(), 'praetor-figures-test-'));
  const remove = () => rmSync(dir, { recursive: true, force: true });
  let result;
  try {
    result = fn(dir);
  } catch (error) {
    remove();
    throw error;
  }
  if (result instanceof Promise) return result.finally(remove);
  remove();
  return result;
}

/** Writes `text` to `path`, creating the parent directories. */
export function write(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}

/** Runs `fn` with console.log and console.error captured; returns its result, the lines, and the lines joined. */
export async function capture(fn) {
  const lines = [];
  const [log, error] = [console.log, console.error];
  console.log = (line) => lines.push(line);
  console.error = (line) => lines.push(line);
  try {
    return { result: await fn(), lines, output: lines.join('\n') };
  } finally {
    [console.log, console.error] = [log, error];
  }
}
