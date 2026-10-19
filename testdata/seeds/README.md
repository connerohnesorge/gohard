# Committed replay failures

`testdata/seeds/` holds committed simulator and language-server replay
failures (#8, #29), plus failing inputs found by the parser and design
generators. Each entry is a permanent regression: plain `go test ./...`
replays every entry on every pull request, and the nightly workflow
replays every committed LSP session as well (#30). Entries are never
edited or deleted; a fix makes the replay pass.

## What goes here

* **Language-server sessions.** Written by the test harness and by
  `FuzzLSPReplay` for every failing session, in the
  `gohard lsp --record` format: `<name>.jsonl`, a header line with the
  same fields as a simulation repro file (`"schema":1`, gohard version,
  standard, testbench, seed, defines, budgets, models, sha256 of every
  source file and of `gohard.yaml`), then one client message per line.
  `gohard lsp replay FILE` against the simulated `Env` must reproduce the
  original server output byte for byte, and the test then checks that the
  failure is fixed.
* **Simulation repros.** A directory with the `.repro.json` written by the
  failing run (`"schema":1`, fixed key order) together with the exact
  source files and `gohard.yaml` it hashes, so `gohard check --repro FILE`
  reruns the identical run (same seed, defines, budgets and models).
* **Generator failures.** Minimal designs produced by the parser and
  design generators that exposed a bug, stored with the seed that
  generated them.

## Layout

```
testdata/seeds/
  lsp/<name>.jsonl            recorded language-server session
  sim/<name>/<tb>-<seed>.repro.json
  sim/<name>/<sources and gohard.yaml>
  gen/<name>/                 generator output plus its seed
```

`<name>` describes the failure (for example the issue number and a short
slug). Everything here is deterministic input: no timestamps, host names
or absolute paths. Replays run only against `internal/env/simenv`, fully
determined by the recorded seed (#29).
