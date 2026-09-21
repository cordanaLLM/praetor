import assert from "node:assert/strict";
import { execPath } from "node:process";
import { mkdtemp, access, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { cliInvocation, runCLI, validateExecutable } from "./runner";

test("CLI invocation keeps arguments separate", () => {
  const item = cliInvocation("praetorctl", ["audit", "--root", "/tmp/a;echo denied"], "/tmp/a path");
  assert.deepEqual(item.args, ["audit", "--root", "/tmp/a;echo denied"]);
  assert.equal(item.cwd, "/tmp/a path");
});
test("empty executable is rejected", () => assert.throws(() => validateExecutable("  ")));
test("NUL executable is rejected", () => assert.throws(() => validateExecutable("praetor\0ctl")));
test("runner returns bounded process output", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write('ok')"], process.cwd());
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "ok");
  assert.equal(result.timedOut, false);
});
test("runner terminates a timed out process", async () => {
  const result = await runCLI(execPath, ["-e", "setTimeout(() => {}, 1000)"], process.cwd(), { timeoutMs: 10 });
  assert.equal(result.timedOut, true);
  assert.notEqual(result.exitCode, 0);
});
test("runner reports nonzero and preserves unicode bytes", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write('🙂'); process.exit(7)"], process.cwd());
  assert.equal(result.exitCode, 7);
  assert.equal(result.stdout, "🙂");
});
test("runner rejects bounded output overflow", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write('🙂'.repeat(20))"], process.cwd(), { maxOutput: 16 });
  assert.equal(result.outputExceeded, true);
  assert.equal(result.timedOut, false);
  assert.notEqual(result.exitCode, 0);
  assert.equal(Buffer.byteLength(result.stdout) + Buffer.byteLength(result.stderr) <= 16, true);
});
test("runner does not expand an incomplete UTF-8 prefix", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write('🙂')"], process.cwd(), { maxOutput: 1 });
  assert.equal(result.outputExceeded, true);
  assert.equal(result.timedOut, false);
  assert.equal(Buffer.byteLength(result.stdout) + Buffer.byteLength(result.stderr) <= 1, true);
  assert.equal(result.stdout, "");
});
test("pre-aborted runner does not spawn", async () => {
  const directory = await mkdtemp(join(tmpdir(), "praetor-runner-"));
  const marker = join(directory, "spawned");
  const controller = new AbortController();
  controller.abort();
  try {
    const result = await runCLI(execPath, ["-e", "require('node:fs').writeFileSync(process.argv[1], 'spawned')", marker], process.cwd(), { signal: controller.signal });
    assert.equal(result.cancelled, true);
    assert.equal(result.exitCode, 1);
    await assert.rejects(access(marker), { code: "ENOENT" });
  } finally { await rm(directory, { recursive: true, force: true }); }
});
test("invalid output never reports successful completion", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write(Buffer.from([255]))"], process.cwd());
  assert.equal(result.invalidOutput, true);
  assert.notEqual(result.exitCode, 0);
});
test("noninteractive runner closes input", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdin.resume(); process.stdin.on('end', () => process.stdout.write('closed'))"], process.cwd());
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "closed");
});
test("missing executable rejects and invalid limits fail before spawn", async () => {
  await assert.rejects(runCLI("praetor-executable-does-not-exist", [], process.cwd()), { code: "ENOENT" });
  for (const timeoutMs of [0, -1, 600_001, NaN, Infinity, 1.5]) {
    assert.throws(() => runCLI(execPath, [], process.cwd(), { timeoutMs }));
  }
});
test("runner keeps shell metacharacters as one argument", async () => {
  const result = await runCLI(execPath, ["-e", "process.stdout.write(process.argv[1])", "$(denied); spaces"], process.cwd());
  assert.equal(result.stdout, "$(denied); spaces");
});
test("runner cancellation settles when child ignores TERM", async () => {
  const controller = new AbortController();
  const running = runCLI(execPath, ["-e", "process.on('SIGTERM',()=>{}); setTimeout(()=>{},10000)"], process.cwd(), { timeoutMs: 5000, signal: controller.signal });
  setTimeout(() => controller.abort(), 20);
  const result = await running;
  assert.equal(result.cancelled, true);
  assert.equal(result.exitCode, 1);
});
