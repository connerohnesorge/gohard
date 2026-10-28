// Starts VS Code with the extension under development and runs suite.ts
// in it (#3), once per case, each in a fresh user data folder whose
// settings point gohard.path at the gohard under test. Run through
// test/run.sh, which builds gohard and copies the fixtures:
//
//   node out/test/e2e/runTest.js GOHARD WORK
//
// VSCODE_VERSION picks the VS Code release it downloads (default
// "stable"); VSCODE_PATH runs an installed VS Code (or a fork such as
// VSCodium) instead, for systems where the download cannot run, like
// NixOS.
import { runTests } from '@vscode/test-electron';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// out/test/e2e -> editors/vscode
const extDir = join(__dirname, '..', '..', '..');
const repoDir = join(extDir, '..', '..');

// Each case opens one folder of WORK; want is a committed fixture the
// case compares with.
const cases = [
  { name: 'cross-file', folder: 'cross-file', want: '' },
  {
    name: 'inlay-hints',
    folder: 'inlay-hints',
    want: join(
      repoDir,
      'testdata',
      'lsp',
      'inlay-hints',
      'parameter-names',
      'want.stdout',
    ),
  },
];

async function main(): Promise<void> {
  const [gohard, work] = process.argv.slice(2);
  if (!gohard || !work) {
    throw new Error('usage: runTest.js GOHARD WORK');
  }
  const version = process.env.VSCODE_VERSION || 'stable';
  const installed = process.env.VSCODE_PATH || undefined;
  for (const c of cases) {
    console.log(`== VS Code ${installed ?? version}: ${c.name}`);
    const user = join(work, '.vscode-user', c.name);
    const done = join(user, 'done');
    mkdirSync(join(user, 'data', 'User'), { recursive: true });
    writeFileSync(
      join(user, 'data', 'User', 'settings.json'),
      JSON.stringify(
        {
          'gohard.path': gohard,
          'gohard.trace.server': 'messages',
          'workbench.startupEditor': 'none',
          'update.mode': 'none',
          'extensions.autoUpdate': false,
          'telemetry.telemetryLevel': 'off',
          'chat.disableAIFeatures': true,
        },
        null,
        2,
      ),
    );
    await runTests({
      ...(installed ? { vscodeExecutablePath: installed } : { version }),
      cachePath: join(extDir, '.vscode-test'),
      extensionDevelopmentPath: extDir,
      extensionTestsPath: join(__dirname, 'suite.js'),
      launchArgs: [
        join(work, c.folder),
        '--disable-extensions',
        '--disable-gpu',
        `--user-data-dir=${join(user, 'data')}`,
        `--extensions-dir=${join(user, 'extensions')}`,
      ],
      extensionTestsEnv: {
        GOHARD_E2E_CASE: c.name,
        GOHARD_E2E_GOHARD: gohard,
        GOHARD_E2E_WANT: c.want,
        GOHARD_E2E_DONE: done,
      },
    });
    // A launcher that exits 0 without running the suite does not pass.
    if (!existsSync(done)) {
      throw new Error(`${c.name}: VS Code exited without running the suite`);
    }
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
