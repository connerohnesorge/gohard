// The parts of the client that do not need VS Code (#3): the server
// command line, the settings sent to the server, the command results
// the client copies and the order of the client's starts and stops.
// The unit tests run them under plain Node.

/** The commands the extension contributes. */
export const restartServerCommand = 'gohard.restartServer';
export const showOutputCommand = 'gohard.showOutput';

/** The server command whose string result is copied to the clipboard,
 * so the "Copy repro" lens copies (internal/server/sim.go CmdRepro). */
export const copyReproCommand = 'gohard.copyRepro';

/** The gohard.yaml keys the extension forwards (#56). Each is the VS
 * Code setting "gohard." + key. */
export const forwardedKeys = [
  'inlay_hints.parameter_names',
  'inlay_hints.types',
  'inlay_hints.values',
] as const;

/** The values WorkspaceConfiguration.inspect reports that count here:
 * the ones a user wrote in a settings file. */
export interface Inspected {
  globalValue?: unknown;
  workspaceValue?: unknown;
  workspaceFolderValue?: unknown;
}

/** The settings object the server reads from initializationOptions and
 * workspace/didChangeConfiguration: the gohard.yaml keys under
 * "gohard". */
export interface ServerSettings {
  gohard: Record<string, unknown>;
}

/**
 * serverSettings returns the settings to send: every forwarded key the
 * user set in a settings file, nested as in gohard.yaml, with VS Code's
 * precedence (folder over workspace over user). A key left at its
 * default is not sent, so gohard.yaml decides it and the server's
 * "gohard.yaml sets KEY" log names only keys the user set in both.
 */
export function serverSettings(
  inspect: (key: string) => Inspected | undefined,
): ServerSettings {
  const gohard: Record<string, unknown> = {};
  for (const key of forwardedKeys) {
    const i = inspect(key);
    const v = i?.workspaceFolderValue ?? i?.workspaceValue ?? i?.globalValue;
    if (v === undefined) {
      continue;
    }
    const parts = key.split('.');
    let obj = gohard;
    for (const p of parts.slice(0, -1)) {
      if (typeof obj[p] !== 'object' || obj[p] === null) {
        obj[p] = {};
      }
      obj = obj[p] as Record<string, unknown>;
    }
    obj[parts[parts.length - 1]] = v;
  }
  return { gohard };
}

/** serverCommand returns the command line of the server for the
 * gohard.path setting: `gohard lsp`, with "gohard" looked up on PATH
 * when the setting is empty. */
export function serverCommand(path: string | undefined): {
  command: string;
  args: string[];
} {
  const command = (path ?? '').trim();
  return { command: command === '' ? 'gohard' : command, args: ['lsp'] };
}

/** copyText returns the text to copy to the clipboard for the result of
 * a server command, or undefined. */
export function copyText(command: string, result: unknown): string | undefined {
  if (
    command === copyReproCommand &&
    typeof result === 'string' &&
    result !== ''
  ) {
    return result;
  }
  return undefined;
}

/** The part of LanguageClient that Lifecycle uses. */
export interface Client {
  start(): Promise<void>;
  dispose(): Promise<void>;
}

/**
 * Lifecycle starts and stops the language client, one request at a
 * time. LanguageClient cannot stop a client that is still starting:
 * dispose throws, and the client goes on to start and register the
 * server's commands, so the next client's start fails on a command that
 * already exists while the first server runs on unreferenced. A restart
 * or stop therefore waits until the one before it has ended, a restart
 * ends when its client's start has ended, and so no client is disposed
 * while it starts. A server that never answers initialize keeps later
 * restarts waiting until the window reloads.
 */
export class Lifecycle<C extends Client> {
  private current: C | undefined;
  private last: Promise<void> = Promise.resolve();

  /** make returns a new client with the current settings. */
  constructor(private readonly make: () => C) {}

  /** client returns the client of the last restart, or undefined after
   * stop. */
  client(): C | undefined {
    return this.current;
  }

  /** restart stops the client, if any, and starts a new one. A start
   * that fails is reported by the client, so restart resolves anyway. */
  restart(): Promise<void> {
    return this.queue(async () => {
      await this.stopNow();
      this.current = this.make();
      try {
        await this.current.start();
      } catch {
        // Reported by the client.
      }
    });
  }

  /** stop stops the client, if any. */
  stop(): Promise<void> {
    return this.queue(() => this.stopNow());
  }

  private queue(op: () => Promise<void>): Promise<void> {
    const done = this.last.then(op);
    this.last = done.catch(() => undefined);
    return done;
  }

  private async stopNow(): Promise<void> {
    const c = this.current;
    this.current = undefined;
    if (c === undefined) {
      return;
    }
    try {
      await c.dispose();
    } catch {
      // A client whose start failed cannot be stopped; dropping it is
      // enough.
    }
  }
}
