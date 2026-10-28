// Unit tests of Lifecycle (#3), the order in which the extension starts
// and stops its language client, with a fake client that follows
// LanguageClient's states.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Client, Lifecycle } from '../../src/gohard';

type State = 'initial' | 'starting' | 'running' | 'startFailed' | 'stopped';

/** A command the server registers through the client on start. */
const serverCommand = 'gohard.copyRepro';

/**
 * Fake follows vscode-languageclient 11: dispose throws for a client
 * that is starting or failed to start and leaves it alone, so a start
 * still going on ends with the client running and the server's command
 * registered. Registering a command twice throws, as in VS Code, and
 * fails that client's start.
 */
class Fake implements Client {
  state: State = 'initial';
  private answer: (() => void) | undefined;
  private fail: ((err: Error) => void) | undefined;

  constructor(private readonly world: World) {}

  start(): Promise<void> {
    this.state = 'starting';
    this.world.check();
    const answered = new Promise<void>((resolve, reject) => {
      this.answer = resolve;
      this.fail = reject;
    });
    if (this.world.auto) {
      setImmediate(() => this.answer?.());
    }
    return answered.then(
      () => {
        if (this.world.commands.has(serverCommand)) {
          this.state = 'startFailed';
          throw new Error(`command '${serverCommand}' already exists`);
        }
        this.world.commands.add(serverCommand);
        this.state = 'running';
        this.world.check();
      },
      (err) => {
        this.state = 'startFailed';
        throw err;
      },
    );
  }

  async dispose(): Promise<void> {
    if (this.state === 'running') {
      this.world.commands.delete(serverCommand);
      this.state = 'stopped';
      return;
    }
    if (this.state === 'initial' || this.state === 'stopped') {
      return;
    }
    throw new Error(
      "Client is not running and can't be stopped. " +
        `It's current state is: ${this.state}`,
    );
  }

  /** answered makes the server answer initialize. */
  answered(): void {
    this.answer?.();
  }

  /** spawnFails makes the start fail, as for a gohard.path that does
   * not exist. */
  spawnFails(): void {
    this.fail?.(new Error('spawn gohard ENOENT'));
  }
}

class World {
  readonly commands = new Set<string>();
  readonly made: Fake[] = [];
  /** The most clients starting or running at one time. */
  most = 0;

  constructor(readonly auto: boolean) {}

  check(): void {
    const n = this.made.filter(
      (c) => c.state === 'starting' || c.state === 'running',
    ).length;
    this.most = Math.max(this.most, n);
  }
}

function setup(auto = false) {
  const world = new World(auto);
  const lc = new Lifecycle(() => {
    const c = new Fake(world);
    world.made.push(c);
    return c;
  });
  return { world, lc };
}

/** settle lets every pending promise callback run. */
function settle(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

test('a restart while the client starts waits for the start', async () => {
  const { world, lc } = setup();
  const first = lc.restart();
  await settle();
  assert.equal(world.made.length, 1);
  const second = lc.restart();
  await settle();
  assert.equal(world.made.length, 1, 'a second client while one starts');
  assert.equal(world.made[0].state, 'starting');

  world.made[0].answered();
  await first;
  await settle();
  assert.equal(world.made[0].state, 'stopped');
  assert.equal(world.made.length, 2);
  world.made[1].answered();
  await second;

  assert.equal(world.made[1].state, 'running');
  assert.equal(lc.client(), world.made[1]);
  assert.deepEqual([...world.commands], [serverCommand]);
  assert.equal(world.most, 1);
});

test('overlapping restarts start one client at a time', async () => {
  const { world, lc } = setup(true);
  await lc.restart();
  await Promise.all([lc.restart(), lc.restart(), lc.restart()]);
  assert.deepEqual(
    world.made.map((c) => c.state),
    ['stopped', 'stopped', 'stopped', 'running'],
  );
  assert.equal(lc.client(), world.made[3]);
  assert.deepEqual([...world.commands], [serverCommand]);
  assert.equal(world.most, 1);
});

test('stop while the client starts stops it once started', async () => {
  const { world, lc } = setup();
  const started = lc.restart();
  await settle();
  const stopped = lc.stop();
  await settle();
  assert.equal(world.made[0].state, 'starting');
  world.made[0].answered();
  await Promise.all([started, stopped]);
  assert.equal(world.made[0].state, 'stopped');
  assert.equal(lc.client(), undefined);
  assert.deepEqual([...world.commands], []);
});

test('a start that fails does not stop the next restart', async () => {
  const { world, lc } = setup();
  const failed = lc.restart();
  await settle();
  world.made[0].spawnFails();
  await failed;
  assert.equal(world.made[0].state, 'startFailed');

  const next = lc.restart();
  await settle();
  world.made[1].answered();
  await next;
  assert.equal(world.made[1].state, 'running');
  assert.equal(lc.client(), world.made[1]);
});

test('a client that cannot be made does not stop the next', async () => {
  const made: string[] = [];
  let n = 0;
  const lc = new Lifecycle(() => {
    n++;
    if (n === 1) {
      throw new Error('no client');
    }
    made.push(`client ${n}`);
    return { start: async () => {}, dispose: async () => {} };
  });
  await assert.rejects(lc.restart(), /no client/);
  await lc.restart();
  assert.deepEqual(made, ['client 2']);
});
