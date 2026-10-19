# testdata/format goldens

Golden input/output pairs for the formatter (#32). `TestGolden` in
`internal/format` (`golden_test.go`) runs every case.

## Layout

```
testdata/format/<case>/
  in.vhd                 the input (any line endings; bytes as is)
  <variant>.yaml         optional: a gohard.yaml for one variant
  <variant>.want.vhd     the expected output of that variant
```

* Each `<variant>.yaml` is read as `gohard.yaml` (`standard`,
  `defines`, `psl.*` and the `format.*` keys apply); its expected output
  is `<variant>.want.vhd`.
* A case without any `.yaml` file has one variant, `default`, with all
  defaults, so its output is `default.want.vhd`.

## Checks on every variant

1. The input parses, and formatting it gives `<variant>.want.vhd` byte for
   byte.
2. The `FuzzFormat` invariants hold on the input (`checkInvariants`).
3. The golden is a fixed point: formatting it produces no edits.
4. `format.Source` gives the same text, and `format.Range` over the
   whole file gives the same edits, except an insertion at the very end,
   which belongs after the range.
5. Every inactive conditional-analysis branch of the input appears
   byte for byte, in order, in the output, with the line ending of its
   last line (`inactive-mixed-eol`: LF branches and directive lines in
   a CRLF file keep their LFs). A case named `inactive-*`
   must have at least one inactive branch.

`TestGoldenCoverage` checks that the variants together set every value
#32 requires: `format.indent` 1, 2 and 4; `format.keyword_case` lower,
upper and preserve; `format.align_ports` true and false; and
`format.max_line_length` 40, 80 and 120.

`.gitattributes` turns off line-ending conversion, so the CRLF cases
keep their bytes on every OS.

## Updating

```sh
nix develop -c go test ./internal/format -run TestGolden -update
```

`-update` rewrites every `<variant>.want.vhd`. Checks 2 to 5 still run.
Review the diff before you commit it.
