// The end-to-end checks VS Code runs inside its extension host (#3).
// runTest.ts starts VS Code once per case (GOHARD_E2E_CASE) on a copy of
// a testdata/lsp fixture:
//
//   cross-file   the diagnostics of top.vhd equal `gohard lsp
//                diagnostics`, its semantic tokens equal `gohard lsp
//                semantic-tokens`, and the signal s is a signal
//                declaration in VS Code's decoding
//   inlay-hints  no hint without settings; gohard.inlay_hints.
//                parameter_names turned on gives the hints of the
//                inlay-hints/parameter-names fixture, through
//                didChangeConfiguration and, after "GoHard: Restart
//                Server", through initializationOptions; after three
//                restarts at once the last client takes a setting
//                change, "Copy repro" copies its command and one
//                server runs
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import * as vscode from 'vscode';

const gohard = process.env.GOHARD_E2E_GOHARD ?? '';
const timeoutMs = 60_000;

/** until polls f until it returns a value, failing after timeoutMs
 * with the last value it saw. */
async function until<T>(
  what: string,
  f: () => Promise<{ ok: boolean; value: T }>,
): Promise<T> {
  const end = Date.now() + timeoutMs;
  let last: T | undefined;
  for (;;) {
    const r = await f();
    if (r.ok) {
      return r.value;
    }
    last = r.value;
    if (Date.now() > end) {
      throw new Error(
        `timed out waiting for ${what}; last: ${JSON.stringify(last, null, 1)}`,
      );
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

interface LspRange {
  start: { line: number; character: number };
  end: { line: number; character: number };
}

function rangeText(r: LspRange | vscode.Range): string {
  const { start, end } = r;
  return `${start.line}:${start.character}-${end.line}:${end.character}`;
}

function diagText(
  range: LspRange | vscode.Range,
  severity: number,
  code: unknown,
  source: string | undefined,
  message: string,
): string {
  return `${rangeText(range)} ${severity} ${code} ${source}: ${message}`;
}

/** cli runs `gohard lsp ACTION --at FILE:1:1` in folder. */
function cli(folder: string, action: string, file: string): any {
  const out = execFileSync(gohard, ['lsp', action, '--at', `${file}:1:1`], {
    cwd: folder,
    encoding: 'utf8',
  });
  return JSON.parse(out);
}

/** servers returns the number of `gohard lsp` processes of the gohard
 * under test, from /proc. */
function servers(): number {
  let n = 0;
  for (const pid of readdirSync('/proc')) {
    if (!/^[0-9]+$/.test(pid)) {
      continue;
    }
    let argv: string[];
    try {
      argv = readFileSync(`/proc/${pid}/cmdline`, 'utf8').split('\0');
    } catch {
      continue; // exited
    }
    if (same(argv, [gohard, 'lsp', ''])) {
      n++;
    }
  }
  return n;
}

async function open(file: string): Promise<vscode.TextDocument> {
  const folder = vscode.workspace.workspaceFolders?.[0];
  assert.ok(folder, 'VS Code opened a folder');
  const doc = await vscode.workspace.openTextDocument(
    vscode.Uri.file(join(folder.uri.fsPath, file)),
  );
  await vscode.window.showTextDocument(doc);
  assert.equal(doc.languageId, 'vhdl');
  return doc;
}

/** semanticTokens waits for the server's semantic tokens of doc, which
 * also shows that the server runs and has loaded the project. */
async function semanticTokens(doc: vscode.TextDocument) {
  return until('semantic tokens', async () => {
    const t = await vscode.commands.executeCommand<
      vscode.SemanticTokens | undefined
    >('vscode.provideDocumentSemanticTokens', doc.uri);
    return { ok: t !== undefined && t.data.length > 0, value: t! };
  });
}

async function crossFile(): Promise<void> {
  const doc = await open('top.vhd');
  const folder = vscode.workspace.workspaceFolders![0].uri.fsPath;

  const want = (cli(folder, 'diagnostics', 'top.vhd').diagnostics as any[])
    .map((d) => diagText(d.range, d.severity, d.code, d.source, d.message))
    .sort();
  assert.ok(want.length > 0, 'the fixture has diagnostics');
  await until('the diagnostics of gohard lsp diagnostics', async () => {
    const got = vscode.languages
      .getDiagnostics(doc.uri)
      .map((d) => {
        const code = typeof d.code === 'object' ? d.code.value : d.code;
        // VS Code counts severities from 0, LSP from 1.
        return diagText(d.range, d.severity + 1, code, d.source, d.message);
      })
      .sort();
    return { ok: same(got, want), value: got };
  });
  console.log(
    `cross-file: ${want.length} diagnostics as gohard lsp diagnostics`,
  );

  const tokens = await semanticTokens(doc);
  assert.deepEqual(
    Array.from(tokens.data),
    cli(folder, 'semantic-tokens', 'top.vhd').data,
  );
  const n = tokens.data.length / 5;
  console.log(`cross-file: ${n} semantic tokens as gohard lsp semantic-tokens`);
  const legend =
    await vscode.commands.executeCommand<vscode.SemanticTokensLegend>(
      'vscode.provideDocumentSemanticTokensLegend',
      doc.uri,
    );
  // `  signal s : bit_vector(width - 1 downto 0);` on line 7.
  const at = new vscode.Position(6, 9);
  assert.equal(doc.getText(new vscode.Range(at, at.translate(0, 1))), 's');
  assert.deepEqual(decodeAt(tokens.data, legend, at), {
    length: 1,
    type: 'signal',
    modifiers: ['declaration'],
  });
  console.log('cross-file: s is a signal declaration');
}

/** decodeAt returns the token that starts at pos. */
function decodeAt(
  data: Uint32Array,
  legend: vscode.SemanticTokensLegend,
  pos: vscode.Position,
) {
  let line = 0;
  let char = 0;
  for (let i = 0; i + 4 < data.length; i += 5) {
    line += data[i];
    char = data[i] === 0 ? char + data[i + 1] : data[i + 1];
    if (line === pos.line && char === pos.character) {
      const mods = legend.tokenModifiers.filter(
        (_, bit) => (data[i + 4] & (1 << bit)) !== 0,
      );
      return {
        length: data[i + 2],
        type: legend.tokenTypes[data[i + 3]],
        modifiers: mods,
      };
    }
  }
  return undefined;
}

async function inlayHints(): Promise<void> {
  const doc = await open('top.vhd');
  const want = (
    JSON.parse(readFileSync(process.env.GOHARD_E2E_WANT ?? '', 'utf8')) as any[]
  )
    .map(
      (h) => `${h.position.line}:${h.position.character} ${h.kind} ${h.label}`,
    )
    .sort();
  assert.ok(want.length > 0, 'the fixture has hints');
  const range = new vscode.Range(0, 0, doc.lineCount, 0);
  const hints = async () =>
    (
      (await vscode.commands.executeCommand<vscode.InlayHint[]>(
        'vscode.executeInlayHintProvider',
        doc.uri,
        range,
      )) ?? []
    )
      .map((h) => {
        const label =
          typeof h.label === 'string'
            ? h.label
            : h.label.map((p) => p.value).join('');
        return `${h.position.line}:${h.position.character} ${h.kind} ${label}`;
      })
      .sort();

  await semanticTokens(doc);
  assert.deepEqual(await hints(), [], 'no hint with every kind off');
  console.log('inlay-hints: no hint without settings');

  await vscode.workspace
    .getConfiguration('gohard')
    .update(
      'inlay_hints.parameter_names',
      true,
      vscode.ConfigurationTarget.Global,
    );
  await until('the hints after didChangeConfiguration', async () => {
    const got = await hints();
    return { ok: same(got, want), value: got };
  });
  console.log(`inlay-hints: ${want.length} hints after didChangeConfiguration`);

  await vscode.commands.executeCommand('gohard.restartServer');
  await semanticTokens(doc);
  await until('the hints after a restart', async () => {
    const got = await hints();
    return { ok: same(got, want), value: got };
  });
  console.log(`inlay-hints: ${want.length} hints after a restart`);

  // Restarts run one at a time, so none disposes a client that is
  // still starting and no two clients register the server's commands.
  // The last client must be the one running: it takes the setting
  // change, its "Copy repro" works below, and no other server is left.
  const restart = () => vscode.commands.executeCommand('gohard.restartServer');
  await Promise.all([restart(), restart(), restart()]);
  await vscode.workspace
    .getConfiguration('gohard')
    .update(
      'inlay_hints.parameter_names',
      false,
      vscode.ConfigurationTarget.Global,
    );
  await until('no hint after three restarts and a change', async () => {
    const got = await hints();
    return { ok: got.length === 0, value: got };
  });
  if (process.platform === 'linux') {
    await until('one gohard lsp', async () => {
      const n = servers();
      return { ok: n === 1, value: n };
    });
  }
  console.log('inlay-hints: three restarts at once leave one server');

  await vscode.commands.executeCommand('gohard.showOutput');

  const file = '.gohard/repro/top-7.repro';
  await vscode.env.clipboard.writeText('');
  const copied = await vscode.commands.executeCommand<string>(
    'gohard.copyRepro',
    { repro: file },
  );
  assert.equal(copied, `gohard check --repro ${file}`);
  assert.equal(await vscode.env.clipboard.readText(), copied);
  console.log('inlay-hints: Copy repro copied its command');
}

export async function run(): Promise<void> {
  const name = process.env.GOHARD_E2E_CASE;
  switch (name) {
    case 'cross-file':
      await crossFile();
      break;
    case 'inlay-hints':
      await inlayHints();
      break;
    default:
      throw new Error(`unknown case ${name}`);
  }
  // runTest.ts fails a case that exits 0 without this file.
  writeFileSync(process.env.GOHARD_E2E_DONE ?? '', `${name} passed\n`);
}
