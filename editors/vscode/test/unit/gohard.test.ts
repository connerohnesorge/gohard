// Unit tests of the client parts that do not need VS Code (#3).
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  copyReproCommand,
  copyText,
  forwardedKeys,
  Inspected,
  restartServerCommand,
  serverCommand,
  serverSettings,
  showOutputCommand,
} from '../../src/gohard';

// out/test/unit -> editors/vscode
const extDir = join(__dirname, '..', '..', '..');
const manifest = JSON.parse(readFileSync(join(extDir, 'package.json'), 'utf8'));

function inspector(values: Record<string, Inspected>) {
  return (key: string) => values[key];
}

test('serverSettings sends nothing the user did not set', () => {
  assert.deepEqual(
    serverSettings(() => undefined),
    { gohard: {} },
  );
  assert.deepEqual(
    serverSettings(() => ({})),
    { gohard: {} },
  );
});

test('serverSettings nests the gohard.yaml keys', () => {
  const got = serverSettings(
    inspector({
      'inlay_hints.parameter_names': { globalValue: true },
      'inlay_hints.values': { globalValue: false },
    }),
  );
  assert.deepEqual(got, {
    gohard: { inlay_hints: { parameter_names: true, values: false } },
  });
});

test('serverSettings follows VS Code precedence', () => {
  const key = 'inlay_hints.types';
  const cases: [Inspected, unknown][] = [
    [{ globalValue: true }, true],
    [{ globalValue: true, workspaceValue: false }, false],
    [{ workspaceValue: false, workspaceFolderValue: true }, true],
    [{ globalValue: false, workspaceFolderValue: true }, true],
  ];
  for (const [inspected, want] of cases) {
    const got = serverSettings(inspector({ [key]: inspected }));
    assert.deepEqual(got, { gohard: { inlay_hints: { types: want } } });
  }
});

test('serverSettings asks only for the forwarded keys', () => {
  const asked: string[] = [];
  serverSettings((key) => {
    asked.push(key);
    return undefined;
  });
  assert.deepEqual(asked, [...forwardedKeys]);
});

test('serverCommand runs gohard lsp', () => {
  assert.deepEqual(serverCommand(undefined), {
    command: 'gohard',
    args: ['lsp'],
  });
  assert.deepEqual(serverCommand('  '), { command: 'gohard', args: ['lsp'] });
  assert.deepEqual(serverCommand(' /opt/gohard/bin/gohard '), {
    command: '/opt/gohard/bin/gohard',
    args: ['lsp'],
  });
});

test('copyText copies only the repro command', () => {
  const cmd = 'gohard check --sim --repro .gohard/repro/tb-42.repro';
  assert.equal(copyText(copyReproCommand, cmd), cmd);
  assert.equal(copyText(copyReproCommand, ''), undefined);
  assert.equal(copyText(copyReproCommand, { error: 'x' }), undefined);
  assert.equal(copyText('gohard.runTestbench', cmd), undefined);
});

test('package.json forwards exactly the forwarded keys', () => {
  const props = Object.keys(manifest.contributes.configuration.properties);
  const forwarded = props
    .filter((p) => p !== 'gohard.path' && p !== 'gohard.trace.server')
    .map((p) => p.replace(/^gohard\./, ''));
  assert.deepEqual(forwarded.sort(), [...forwardedKeys].sort());
  for (const key of forwardedKeys) {
    const p = manifest.contributes.configuration.properties['gohard.' + key];
    assert.equal(p.type, 'boolean', key);
    assert.equal(p.default, false, key);
  }
});

test('package.json contributes the commands the extension registers', () => {
  const ids = manifest.contributes.commands.map(
    (c: { command: string }) => c.command,
  );
  assert.deepEqual(ids, [restartServerCommand, showOutputCommand]);
});
