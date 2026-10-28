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
  restartServerCommand,
  serverCommand,
  serverSettings,
  showOutputCommand,
} from './gohard';

let client: LanguageClient | undefined;
let output: vscode.LogOutputChannel | undefined;

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

async function stop(): Promise<void> {
  const c = client;
  client = undefined;
  if (c === undefined) {
    return;
  }
  try {
    await c.dispose();
  } catch {
    // A client that never started cannot be stopped; dropping it is
    // enough.
  }
}

/** start starts a new client with the current settings. A server that
 * fails to start is reported by the client in a notification and the
 * GoHard output; the extension stays active so the user can fix
 * gohard.path and restart. */
async function start(): Promise<void> {
  if (output === undefined) {
    return;
  }
  client = newClient(output);
  try {
    await client.start();
  } catch {
    // Reported by the client.
  }
}

async function restart(): Promise<void> {
  await stop();
  await start();
}

export async function activate(ctx: vscode.ExtensionContext): Promise<void> {
  output = vscode.window.createOutputChannel('GoHard', { log: true });
  ctx.subscriptions.push(
    output,
    vscode.commands.registerCommand(restartServerCommand, restart),
    vscode.commands.registerCommand(showOutputCommand, () =>
      output?.show(true),
    ),
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration('gohard.path')) {
        await restart();
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
  await start();
}

export function deactivate(): Promise<void> {
  return stop();
}
