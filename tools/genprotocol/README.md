# genprotocol

Generates `internal/protocol/protocol_gen.go`, the LSP 3.17 types of
GoHard's language server (#25), from `metaModel.json`. Run it with
`go generate ./internal/protocol`; `scripts/ci/gen-check.sh` reruns it
and fails on drift (#30), and `internal/protocol`'s tests compare the
committed file with the generator's output.

## Vendored meta model

| File | Source | SHA-256 |
| --- | --- | --- |
| `metaModel.json` | [microsoft/vscode-languageserver-node](https://github.com/microsoft/vscode-languageserver-node), tag `release/protocol/3.17.5`, `protocol/metaModel.json` (`metaData.version` 3.17.0) | `ba382feb60d4bab4249073c0508b19c67ba2b32c53d70505f452bb49a6f39997` |
| `LICENSE` | the same tag, `License.txt` (MIT, Microsoft Corporation) | `05b6af84f5acd7186f031cf389c722350d2780608d24e6c237cf2d2d85d139bd` |

To update: replace both files from a new tag, update this table, run
`go generate ./internal/protocol` and commit the result. Proposed
(post-3.17) entries are skipped by the generator.

## What is generated

Only the type closure of the methods in `gen.Methods` plus `gen.Extra`,
minus the capability properties in `gen.Skip` (features v1 does not
have). The mapping rules are in the `gen` package documentation.

The output is gofmt-clean and the generator keeps the lines it controls
within 80 columns. Struct fields with JSON tags cannot be broken, so a
few of them are longer; the file carries the `// Code generated ... DO
NOT EDIT.` header, which `scripts/ci/format.sh` (via `go_sources` in
`scripts/ci/lib.sh`) and golines already exclude.
