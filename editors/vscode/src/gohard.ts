// The parts of the client that do not need VS Code (#3): the server
// command line, the settings sent to the server and the command results
// the client copies. The unit tests run them under plain Node.

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
