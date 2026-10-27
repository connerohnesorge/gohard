# Running GoHard in CI

`gohard check` exits 0 when the design is clean, 1 when it has design
errors (or warnings under `--strict`), and 2 when GoHard could not check
the design at all: a bad `gohard.yaml`, a missing path, an unsupported
construct, a crash (#20). After exit 2 the SARIF file holds only the
setup failure (`invocations[0].executionSuccessful: false`), not the
design's findings.

GitHub code scanning closes every alert that is missing from an upload.
Uploading the exit-2 SARIF would therefore wipe the project's existing
alerts, so upload only after exit 0 or 1 and fail the job on exit 2:

```yaml
name: gohard
on: [push, pull_request]
permissions:
  contents: read
  security-events: write
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: '1.27.x'
      - run: go install github.com/connerohnesorge/gohard/cmd/gohard@latest
      - id: gohard
        # Record the exit code instead of failing here.
        run: |
          code=0
          gohard check --format sarif --output gohard.sarif || code=$?
          echo "exit=$code" >> "$GITHUB_OUTPUT"
      # Exit 2: GoHard did not check the design; keep the old alerts.
      - if: steps.gohard.outputs.exit == '0' || steps.gohard.outputs.exit == '1'
        uses: github/codeql-action/upload-sarif@v4
        with:
          sarif_file: gohard.sarif
          category: gohard
      - if: steps.gohard.outputs.exit != '0'
        env:
          CODE: ${{ steps.gohard.outputs.exit }}
        run: exit "$CODE"
```

The last step fails the job on design errors (1) and on setup failures
(2) alike, after the alerts are uploaded. Drop it, or test only for `2`,
to make design errors visible in code scanning without failing the job.

## JUnit XML

`gohard check --format junit` writes JUnit XML, which GitLab, Jenkins
and Azure Pipelines show as test results (#61). It validates against
the Jenkins `junit-10.xsd` schema. The output is one `<testsuite>`
with:

* one test case per checked file (classname `gohard.check`), and one
  per other path with a diagnostic, such as `gohard.yaml`;
  diagnostics without a path go to the case named `gohard`;
* with `--sim`, one test case per testbench run (classname
  `gohard.sim`), holding that run's diagnostics.

In each case a diagnostic that would exit 2 on its own (a tool or
setup failure) goes into `<error>`, one that would exit 1 (an error, an
unchecked unit, a warning under `--strict`) into `<failure>`, and the
rest into `<system-out>`. The `type` of an `<error>` or `<failure>` is
the code of its most severe diagnostic and its text lists all of them
in the text form. The schema allows no properties on a test case, so a
testbench's seed, status, simulated end time and repro path are suite
properties named `<testbench>.seed`, `<testbench>.status`,
`<testbench>.end` and `<testbench>.repro`, and also the first lines of
the case's `<system-out>`. Every `time` attribute is `0`: GoHard does
not report wall-clock time. Exit codes are those of the other formats.

An exit-2 run still writes the XML, with the setup failure as an
`<error>`. A test report replaces nothing, so it can be uploaded after
any exit code.

GitLab reads the file through `artifacts:reports:junit`. `when:
always` keeps the report and the repro files of a failing run:

```yaml
gohard:
  image: golang:1.27
  script:
    - go install github.com/connerohnesorge/gohard/cmd/gohard@latest
    - gohard check --sim --format junit --output gohard-junit.xml
  artifacts:
    when: always
    paths:
      - .gohard/traces/
    reports:
      junit: gohard-junit.xml
```

GitHub Actions has no built-in JUnit view; a reporter action such as
`mikepenz/action-junit-report` turns the file into a check run with
annotations (it needs `checks: write`):

```yaml
name: gohard-tests
on: [push, pull_request]
permissions:
  contents: read
  checks: write
jobs:
  sim:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: '1.27.x'
      - run: go install github.com/connerohnesorge/gohard/cmd/gohard@latest
      - run: gohard check --sim --format junit --output gohard-junit.xml
      - if: always()
        uses: mikepenz/action-junit-report@v6
        with:
          report_paths: gohard-junit.xml
      - if: always()
        uses: actions/upload-artifact@v7
        with:
          name: gohard-traces
          path: .gohard/traces/
          include-hidden-files: true
```

The `gohard check` step fails the job on exit 1 or 2; the report and
the repro files are still published.
