import { spawn, type ChildProcess } from "node:child_process";

export const MAX_OUTPUT = 1 << 20;
export const DEFAULT_TIMEOUT_MS = 30_000;
export const MAX_TIMEOUT_MS = 600_000;
export interface RunResult { exitCode: number; stdout: string; stderr: string; timedOut: boolean; cancelled: boolean; outputExceeded?: boolean; invalidOutput?: boolean; }
export interface RunOptions { timeoutMs?: number; maxOutput?: number; signal?: AbortSignal; env?: NodeJS.ProcessEnv; }
export function validateExecutable(executable: string): string { const value = executable.trim(); if (!value || value.includes("\0")) throw new Error("CLI path is required"); return value; }
export function cliInvocation(executable: string, args: readonly string[], cwd: string) { return { executable: validateExecutable(executable), args: [...args], cwd }; }
function bounded(value: number | undefined, fallback: number, name: string, maximum: number): number { const result = value ?? fallback; if (!Number.isFinite(result) || result <= 0 || result > maximum || !Number.isInteger(result)) throw new Error(`${name} must be a finite positive bounded integer`); return result; }
function killTree(child: ChildProcess): void { if (child.pid === undefined) return; try { if (process.platform === "win32") child.kill(); else process.kill(-child.pid, "SIGTERM"); } catch { try { child.kill("SIGTERM"); } catch { /* exited */ } } }
function killHard(child: ChildProcess): void { try { if (process.platform === "win32") child.kill("SIGKILL"); else process.kill(-child.pid!, "SIGKILL"); } catch { try { child.kill("SIGKILL"); } catch { /* exited */ } } }
export function runCLI(executable: string, args: readonly string[], cwd: string, options: RunOptions = {}): Promise<RunResult> {
  const invocation = cliInvocation(executable, args, cwd), timeout = bounded(options.timeoutMs, DEFAULT_TIMEOUT_MS, "timeoutMs", MAX_TIMEOUT_MS), maxOutput = bounded(options.maxOutput, MAX_OUTPUT, "maxOutput", MAX_OUTPUT);
  if (options.signal?.aborted) return Promise.resolve({ exitCode: 1, stdout: "", stderr: "", timedOut: false, cancelled: true });
  return new Promise((resolve, reject) => {
    const child = spawn(invocation.executable, invocation.args, { cwd: invocation.cwd, env: options.env, shell: false, stdio: ["ignore", "pipe", "pipe"], detached: process.platform !== "win32", windowsHide: true });
    const out: Buffer[] = [], err: Buffer[] = []; let captured = 0, timedOut = false, cancelled = false, outputExceeded = false, settled = false, terminated = false; let timer: NodeJS.Timeout | undefined, hardTimer: NodeJS.Timeout | undefined, settleTimer: NodeJS.Timeout | undefined;
    const abort = (): void => terminate("cancel");
    let invalidOutput = false, closed = false, escalated = false, exitCode = 1;
    const decode = (data: Buffer): string => { try { return new TextDecoder("utf-8", { fatal: true }).decode(data); } catch { invalidOutput = true; return ""; } };
    const result = (): RunResult => {
      const stdout = decode(Buffer.concat(out)), stderr = decode(Buffer.concat(err));
      return { exitCode: timedOut || cancelled || outputExceeded || invalidOutput ? 1 : exitCode, stdout, stderr, timedOut, cancelled, outputExceeded, invalidOutput };
    };
    const finish = (result: RunResult): void => { if (settled) return; settled = true; if (timer) clearTimeout(timer); if (hardTimer) clearTimeout(hardTimer); if (settleTimer) clearTimeout(settleTimer); options.signal?.removeEventListener("abort", abort); resolve(result); };
    const terminate = (kind: "timeout" | "cancel" | "output"): void => {
      if (kind === "timeout") timedOut = true;
      if (kind === "cancel") cancelled = true;
      if (kind === "output") outputExceeded = true;
      if (terminated) return;
      terminated = true;
      killTree(child);
      hardTimer = setTimeout(() => { killHard(child); escalated = true; if (closed) finish(result()); }, 100);
      settleTimer = setTimeout(() => { child.stdout?.destroy(); child.stderr?.destroy(); finish(result()); }, 500);
    };
    const capture = (target: Buffer[], chunk: Buffer): void => { if (outputExceeded) return; const remaining = maxOutput - captured; if (remaining <= 0) { terminate("output"); return; } const kept = chunk.subarray(0, remaining); target.push(Buffer.from(kept)); captured += kept.length; if (kept.length < chunk.length) terminate("output"); };
    child.stdout?.on("data", (chunk: Buffer) => capture(out, chunk)); child.stderr?.on("data", (chunk: Buffer) => capture(err, chunk));
    child.once("error", (error) => { if (settled) return; settled = true; if (timer) clearTimeout(timer); if (hardTimer) clearTimeout(hardTimer); if (settleTimer) clearTimeout(settleTimer); options.signal?.removeEventListener("abort", abort); reject(error); });
    child.once("close", (code) => { closed = true; exitCode = code ?? 1; if (!terminated || escalated) finish(result()); });
    timer = setTimeout(() => terminate("timeout"), timeout); options.signal?.addEventListener("abort", abort, { once: true });
  });
}
