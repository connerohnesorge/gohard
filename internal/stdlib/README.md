# internal/stdlib: bundled std and ieee (#33)

## Source

| | |
| --- | --- |
| Upstream | https://gitlab.com/IEEE-P1076/packages |
| Tag | `1076-2019` |
| Commit | `16a012320947d378611cc7457f64ed76cb52bac4` (2020-09-30, "Merge branch 'release' into 'release'") |
| License | Apache-2.0 (`LICENSE`, copied unchanged; copyright holders in `AUTHORS.md`, copied unchanged) |

`std/*.vhdl` and `ieee/*.vhdl` are the upstream `std/` and `ieee/` files
of that commit, byte for byte (some are ISO-8859-1, as upstream). Every
file carries the Apache-2.0 header; only this Apache-licensed text
ships. The upstream `code/` (VHPI C headers), `testbench/`, `tools/`
and `vhpi_uml/` folders are not vendored. `.gitattributes` turns off
line-ending conversion so the hashes hold on every OS.

There is no package body for `std.standard`, `std.textio`, `std.env` or
`std.reflection` upstream: those are built into the simulation kernel
(#35). Every other body runs interpreted from this text.

To re-vendor (only for a new pinned tag): copy the files, update this
table, regenerate `SHA256SUMS` (one `<sha256>  <path>` line per file,
sorted by path, as `sha256sum` prints) and run the tests below.

## Files here

| File | Contents |
| --- | --- |
| `SHA256SUMS` | integrity hash of every bundled file; `sha256sum -c SHA256SUMS` checks a checkout |
| `firststd.tsv` | the first standard of every bundled declaration |
| `testdata/ghdl/{1993,2008,2019}.txt` | the declaration sets of GHDL's std/ieee sources, recorded |
| `testdata/nvc/2008.txt` | the declaration set of NVC 1.23's VHDL-2008 std/ieee sources (`TestRecordNVC`) |
| `stdlib.go` | the embedded files and their analysis order |
| `integrity.go` | `Verify`, the `WithTamper` test hook, `InternalError` |
| `decls.go`, `tags.go` | declaration extraction, tags and per-standard views |
| `prebuilt.go` | the hook for the build-time analysis (#34) |

## Integrity

`Verify` hashes every embedded file at startup (`gohard check`) and
compares it with `SHA256SUMS`. A mismatch, a missing hash or a missing
file is `stdlib/integrity`, a tool failure (exit 2). An error that
analysis finds inside the bundled text is `stdlib/internal-error`
(`InternalError`), also exit 2. The fixtures
`testdata/cli/check-stdlib-tampered{,-json}` tamper `ieee/numeric_std.vhdl`
through the `tamper-stdlib` setup directive, which calls
`stdlib.WithTamper` on the run's context; nothing outside Go code can
reach the hook.

## Analysis and views

Every bundled file parses with no syntax error under VHDL-2008 and
VHDL-2019 (`TestParse`, and the PR parser-regression run in
`internal/parser`). The bundled text is analyzed as VHDL-2019 for every
selected standard; a view hides names, not text. Under 1993 a design
sees `numeric_std.UNSIGNED` but not `UNRESOLVED_UNSIGNED`, the 2008
type the 2019 text declares it from, and sees the 1993 overloads that
the 2019 text declares on the unresolved types.

`ViewOf(std)` shows exactly the declarations whose first standard is at
or before `std` (`TestViews`): 2008 hides the 2019 additions; 1993 and
2002 hide the 2008 and 2019 additions. A package that does not exist
in a standard (`fixed_pkg` before 2008, `std.reflection` before 2019)
is hidden with all its declarations (`View.HasUnit`).

A declaration is a package or an item of a package's declarative part
(type, subtype, constant, file, alias, attribute, function, procedure,
...). Enumeration literals, units, record elements and protected-type
methods belong to their type. Implicitly declared operations
(`TO_STRING` on character arrays, `MINIMUM`, the predefined operators)
are not bundled declarations; analysis declares them per standard
(#34).

## First-standard tags

`firststd.tsv` has one line per declaration: standard, source, key.
The tag of a declaration is the oldest reference set that has it:

| Source | Meaning |
| --- | --- |
| `ghdl-93` | in GHDL's VHDL-93 text: `std/v93/{standard,textio}`, `ieee/v93/{std_logic_1164,numeric_std,numeric_bit}` (IEEE 1164-1993, 1076.3-1997) and `ieee/math_{real,complex}` (1076.2-1996) |
| `ghdl-08` | in GHDL's VHDL-2008 text (`std/v08`, `ieee2008`: the IEEE 1076-2008 package texts) and not in its VHDL-93 text |
| `ghdl-19` | only in GHDL's VHDL-2019 `std/v19/{textio,env}` |
| `new-19` | in no reference set: added by 1076-2019 |
| `manual-*` | a reviewed exception; a trailing `?` marks an uncertain tag |

Matching ignores the declaration kind and the prefix of type marks, and
treats `UNRESOLVED_UNSIGNED`/`U_UNSIGNED`/`UNSIGNED` (likewise
`SIGNED`, `UFIXED`, `SFIXED`, `FLOAT*`) and
`STD_LOGIC_VECTOR`/`STD_ULOGIC_VECTOR`, `STD_LOGIC`/`STD_ULOGIC` as one
type: in 2008 the resolved types became subtypes of the unresolved
ones, so one 2008 overload replaces the two 1993 overloads, and a 1993
function that 2008 turned into an alias (`To_StdLogicVector`) or a 1993
type that became a subtype (`STD_LOGIC_VECTOR`) keeps 1993.

No std/ieee declaration is new in 2002 (VHDL-2002 changed the language,
not these packages), so the 2002 view equals the 1993 view.

Results (1962 declarations): 451 from 1993, 1366 from 2008, 145 from
2019. The 2019 additions are `std.standard` `DIRECTION`,
`FILE_OPEN_STATE` and `FILE_ORIGIN_KIND`; `std.textio` `LINE_VECTOR`;
the new `std.env` subprograms and types (time records, `GETENV`,
`VHDL_VERSION`/`TOOL_*`, directories, call paths, `FILE_NAME`/
`FILE_PATH`/`FILE_LINE`, the VHDL assert and PSL controls); and all of
`std.reflection`. No ieee declaration is new in 2019.

Reviewed exceptions:

* `std.textio procedure WRITE [LINE, REAL, STRING]` (format string):
  2008, `manual-lrm08?`. VHDL-2008 (16.4) adds it with the other textio
  additions; GHDL's VHDL-2008 and 2019 textio do not declare it. Only
  one source, so marked uncertain.
* The 12 `ieee.std_logic_textio` aliases: 2008, `manual-text08`. The
  bundled file is the 2008 package text (`$Date: 2008-06-27`), whose
  aliases point at the `READ`/`WRITE` procedures 2008 moved into
  `std_logic_1164`; GHDL ships an empty package instead. Before 2008
  the package was not an IEEE package, so the 1993 view hides it.

GHDL's generated `standard.vhdl` declares `UNIVERSAL_INTEGER` and
`UNIVERSAL_REAL`; the LRM text has them only as comments and analysis
predefines them, so they are not bundled declarations.

The 2008 view matches GHDL's VHDL-2008 declaration set exactly up to
those differences (`TestGHDL2008Set`), and every declaration of all
three reference sets is bundled (`TestRefSetsCovered`).

NVC ships its std/ieee libraries only in compiled form, and its
`--dump` cannot print element resolutions (`std_logic_1164` stops at
`STD_LOGIC_VECTOR`), so its set is read from the sources of the same
tag, r1.23.0, in the pinned NVC regression submodule (`testdata/nvc/lib`,
the files `lib/std.08/deps.mk` and `lib/ieee.08/deps.mk` compile into
the 2008 libraries) and recorded in `testdata/nvc/2008.txt`. The 2008
view matches it exactly (`TestNVC2008Set`) except for `NEVER_WAITS`, an
NVC-private attribute of its `textio` read procedures. NVC's 2008 text
has the `std_logic_textio` aliases and the textio `WRITE` with a format
string, so both GHDL differences above agree with NVC.

## Tests and updates

```sh
nix develop -c go test ./internal/stdlib
# re-record the GHDL reference sets (developer only; CI reads the
# recorded files):
nix develop -c sh -c 'go test ./internal/stdlib -run TestRecordGHDL \
  -ghdl "$(dirname "$(readlink -f "$(command -v ghdl)")")/../lib/ghdl/src"'
# re-record the NVC 2008 set from the pinned NVC sources:
nix develop -c go test ./internal/stdlib -run TestRecordNVC \
  -nvc "$PWD/testdata/nvc/lib"
# rewrite firststd.tsv from the recorded sets, keeping manual lines:
nix develop -c go test ./internal/stdlib -run TestTags -update
```

## Prebuilt

`std`/`ieee` are analyzed at build time (#33). `Prebuilt` returns nil
until analysis (#34) exists; the end of S4b adds the generator, the
embedded result and the test that startup performs no analysis of
them.
