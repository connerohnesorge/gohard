# testdata/stubs: CI-only vendor stub packages

These are small packages GoHard wrote itself (#1). Each declares only
the components the `docs/libraries.md` examples use, with the port
lists the examples connect, so CI can check designs that use vendor
libraries without the vendor tools installed. They contain no vendor
text and no behavior.

They are **not** a substitute for the vendor libraries. Map the real
folders of your own Vivado, Quartus or Diamond install instead (see
`docs/libraries.md`, or run `gohard libs detect`). `gohard libs detect
--write` never writes a `testdata/stubs` path into a gohard.yaml, and
every stub file starts with the marker line `-- gohard-ci-stub` so a
copied stub is recognised too.

| Folder | Library | Units |
| --- | --- | --- |
| `xilinx/unisims` | `unisim` | `vcomponents`: `BUFG`, `IBUFDS`, `FDRE` |
| `xilinx/unimacro` | `unimacro` | `vcomponents`: `COUNTER_TC_MACRO` |
| `intel/altera_mf` | `altera_mf` | `altera_mf_components`: `altsyncram` |
| `lattice/ecp5u` | `ecp5u` | `components`: `OSCG`, `FD1S3AX` |
