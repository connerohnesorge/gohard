// The GoHard extension (#3): a thin client that starts `gohard lsp`.
// Every language feature, the code lenses, code actions and semantic
// tokens included, comes from the server; the extension adds only the
// VHDL language, its settings and two commands.
import * as vscode from 'vscode';
import {
  DidChangeConfigurationNotification,
  LanguageClient,
} from 'vscode-languageclient/node';
import {
  copyText,
  forwardedKeys,
  Lifecycle,
  restartServerCommand,
  serverCommand,
  serverSettings,
  showOutputCommand,
} from './gohard';

let clients: Lifecycle<LanguageClient> | undefined;

function settings() {
  const c = vscode.workspace.getConfiguration('gohard');
  return serverSettings((key) => c.inspect(key));
}

function newClient(out: vscode.LogOutputChannel): LanguageClient {
  const path = vscode.workspace.getConfiguration('gohard').get<string>('path');
  // The id "gohard" makes the client read gohard.trace.server.
  return new LanguageClient('gohard', 'GoHard', serverCommand(path), {
    documentSelector: [{ scheme: 'file', language: 'vhdl' }],
    outputChannel: out,
    initializationOptions: settings,
    middleware: {
      executeCommand: async (command, args, next) => {
        const result = await next(command, args);
        const text = copyText(command, result);
        if (text !== undefined) {
          await vscode.env.clipboard.writeText(text);
        }
        return result;
      },
    },
  });
}

export async function activate(ctx: vscode.ExtensionContext): Promise<void> {
  const output = vscode.window.createOutputChannel('GoHard', { log: true });
  // A server that fails to start is reported by the client in a
  // notification and the GoHard output; the extension stays active so
  // the user can fix gohard.path and restart.
  const lc = new Lifecycle(() => newClient(output));
  clients = lc;
  ctx.subscriptions.push(
    output,
    vscode.commands.registerCommand(restartServerCommand, () => lc.restart()),
    vscode.commands.registerCommand(showOutputCommand, () => output.show(true)),
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      const client = lc.client();
      if (e.affectsConfiguration('gohard.path')) {
        await lc.restart();
      } else if (
        forwardedKeys.some((k) => e.affectsConfiguration('gohard.' + k)) &&
        client?.isRunning()
      ) {
        await client.sendNotification(DidChangeConfigurationNotification.type, {
          settings: settings(),
        });
      }
    }),
  );
  await lc.restart();
}

export function deactivate(): Promise<void> | undefined {
  return clients?.stop();
}
