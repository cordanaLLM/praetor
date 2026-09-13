import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import runner from "../dist/runner.js";

const extensionRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));

async function prepare(root) {
  const userData = join(root, "user-data");
  const extensions = join(root, "extensions");
  const workspace = join(root, "workspace");
  await Promise.all([mkdir(join(userData, "User"), { recursive: true }), mkdir(extensions), mkdir(workspace)]);
  await writeFile(join(userData, "User", "settings.json"), JSON.stringify({
    "standards.lsp.enabled": false,
    "standards.cli.path": process.execPath,
    "telemetry.telemetryLevel": "off",
  }) + "\n", { mode: 0o600 });
  await writeFile(join(workspace, "audit"), "require('node:fs').writeFileSync('unexpected-cli-execution', 'executed');\n", { mode: 0o600 });
  return { userData, extensions, workspace, report: join(root, "report.json") };
}

async function main() {
  if (process.argv.length > 3) throw new Error("usage: host-smoke.mjs [code-executable]");
  const root = await mkdtemp(join(tmpdir(), "praetor-vscode-host-smoke-"));
  try {
    const paths = await prepare(root);
    const env = { ...process.env, PRAETOR_HOST_SMOKE_REPORT: paths.report,
      XDG_CONFIG_HOME: join(paths.userData, "config"), XDG_DATA_HOME: join(paths.userData, "data"),
      XDG_CACHE_HOME: join(paths.userData, "cache"), XDG_STATE_HOME: join(paths.userData, "state") };
    delete env.VSCODE_IPC_HOOK_CLI;
    delete env.ELECTRON_RUN_AS_NODE;
    delete env.VSCODE_CLI;
    const result = await runner.runCLI(process.argv[2] ?? "code", [
      "--user-data-dir", paths.userData, "--extensions-dir", paths.extensions,
      "--disable-gpu", "--wait", "--extensionDevelopmentPath=" + extensionRoot,
      "--extensionTestsPath=" + join(extensionRoot, "dist", "hostSmoke.js"), paths.workspace,
    ], paths.workspace, { timeoutMs: 120_000, env });
    if (result.exitCode !== 0) throw new Error("isolated host incomplete: " + JSON.stringify(result));
    const report = JSON.parse(await readFile(paths.report, "utf8"));
    if (report.schema_version !== 1 || report.extension_active !== true || report.registered_commands?.length !== 5) {
      throw new Error("isolated host returned no valid activation report");
    }
    console.log(JSON.stringify(report));
  } finally { await rm(root, { recursive: true, force: true }); }
}

main().catch(error => { console.error("VS Code host smoke failed: " + String(error)); process.exitCode = 1; });
