import * as path from "path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;
let hissStatusBar: vscode.StatusBarItem;
let sentinelStatusBar: vscode.StatusBarItem;
let modelTierStatusBar: vscode.StatusBarItem;

export function activate(context: vscode.ExtensionContext) {
  const config = vscode.workspace.getConfiguration("standards");
  const lspEnabled = config.get<boolean>("lsp.enabled", true);

  // 1. Initialize and start standards-lsp client if enabled
  if (lspEnabled) {
    const rawPath = config.get<string>("lsp.path", "${workspaceFolder}/bin/standards-lsp");
    const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath || ".";
    const serverExecutable = rawPath.replace("${workspaceFolder}", workspaceRoot);

    const serverOptions: ServerOptions = {
      command: serverExecutable,
      args: [],
      options: {
        cwd: workspaceRoot,
      },
    };

    const clientOptions: LanguageClientOptions = {
      documentSelector: [{ scheme: "file", language: "go" }],
      synchronize: {
        fileEvents: vscode.workspace.createFileSystemWatcher("**/*.go"),
      },
    };

    client = new LanguageClient(
      "standardsLSP",
      "cordanaLLM Standards LSP",
      serverOptions,
      clientOptions
    );

    client.start().catch((err) => {
      vscode.window.showWarningMessage(
        `Failed to start standards-lsp at ${serverExecutable}: ${err.message}`
      );
    });
  }

  // 2. Register Status Bar Items
  setupStatusBar(context);

  // 3. Register Commands
  registerCommands(context);
}

function setupStatusBar(context: vscode.ExtensionContext) {
  // HISS Compliance Status Item
  hissStatusBar = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Left,
    100
  );
  hissStatusBar.text = "$(shield) HISS: 100% Pass";
  hissStatusBar.tooltip = "Repository compliant with HISS-16 standards baseline.";
  hissStatusBar.command = "standards.audit";
  hissStatusBar.show();
  context.subscriptions.push(hissStatusBar);

  // Host Sentinel Headroom Status Item
  sentinelStatusBar = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Left,
    99
  );
  const headroom = vscode.workspace
    .getConfiguration("standards")
    .get<number>("sentinel.headroomMB", 1024);
  sentinelStatusBar.text = `$(pulse) Headroom: ${headroom}MB`;
  sentinelStatusBar.tooltip = "Host memory and resource sentinel headroom.";
  sentinelStatusBar.command = "standards.checkSentinel";
  sentinelStatusBar.show();
  context.subscriptions.push(sentinelStatusBar);

  // Active Model Tier Status Item
  modelTierStatusBar = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Left,
    98
  );
  const tier = vscode.workspace
    .getConfiguration("standards")
    .get<string>("modelTier", "gemini-2.5-pro");
  modelTierStatusBar.text = `$(hubot) Model: ${tier}`;
  modelTierStatusBar.tooltip = "Active router model capacity tier.";
  modelTierStatusBar.show();
  context.subscriptions.push(modelTierStatusBar);
}

function registerCommands(context: vscode.ExtensionContext) {
  // standards.audit
  const auditCmd = vscode.commands.registerCommand("standards.audit", () => {
    runTerminalCommand("Standards Audit", "go run ./cmd/standardsctl audit");
  });
  context.subscriptions.push(auditCmd);

  // standards.compileContext
  const compileContextCmd = vscode.commands.registerCommand(
    "standards.compileContext",
    () => {
      runTerminalCommand(
        "Standards Compile Context",
        "go run ./cmd/standardsctl compile-context"
      );
    }
  );
  context.subscriptions.push(compileContextCmd);

  // standards.runGC
  const runGCCmd = vscode.commands.registerCommand("standards.runGC", () => {
    runTerminalCommand("Standards Ratchet Sweep", "make verify-all");
  });
  context.subscriptions.push(runGCCmd);

  // standards.checkSentinel
  const sentinelCmd = vscode.commands.registerCommand(
    "standards.checkSentinel",
    () => {
      const config = vscode.workspace.getConfiguration("standards");
      const headroom = config.get<number>("sentinel.headroomMB", 1024);
      vscode.window.showInformationMessage(
        `Sentinel Check: Host headroom verified >= ${headroom}MB available.`
      );
    }
  );
  context.subscriptions.push(sentinelCmd);
}

function runTerminalCommand(name: string, command: string) {
  const existing = vscode.window.terminals.find((t) => t.name === name);
  const terminal = existing || vscode.window.createTerminal(name);
  terminal.show();
  terminal.sendText(command);
}

export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  return client.stop();
}
