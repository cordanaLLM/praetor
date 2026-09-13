import * as vscode from "vscode";
import { access, writeFile } from "node:fs/promises";
import { join } from "node:path";
import assert from "node:assert/strict";

const REQUIRED_COMMANDS = [
  "standards.audit",
  "standards.compileContext",
  "standards.runGC",
  "standards.checkSentinel",
  "standards.setupAgents",
] as const;

export async function run(): Promise<void> {
  const reportPath = process.env.PRAETOR_HOST_SMOKE_REPORT;
  if (!reportPath) throw new Error("The isolated smoke launcher must supply a report destination");
  const extension = vscode.extensions.getExtension("cordanaLLM.standards-vscode");
  if (!extension) throw new Error("Praetor extension did not activate in the isolated host");
  await extension.activate();
  const commands = await vscode.commands.getCommands(true);
  const missing = REQUIRED_COMMANDS.filter(command => !commands.includes(command));
  if (missing.length > 0) throw new Error(`Praetor commands were not registered: ${missing.join(",")}`);
  let restrictedCommandBlocked = false;
  if (!vscode.workspace.isTrusted) {
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) throw new Error("Isolated smoke workspace is missing");
    await vscode.commands.executeCommand("standards.audit");
    await assert.rejects(access(join(folder.uri.fsPath, "unexpected-cli-execution")), { code: "ENOENT" });
    restrictedCommandBlocked = true;
  }
  const report = {
    schema_version: 1,
    host_version: vscode.version,
    workspace_trusted: vscode.workspace.isTrusted,
    extension_active: extension.isActive,
    registered_commands: REQUIRED_COMMANDS,
    restricted_command_blocked: restrictedCommandBlocked,
  };
  await writeFile(reportPath, JSON.stringify(report) + "\n", { mode: 0o600 });
}
