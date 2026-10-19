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
