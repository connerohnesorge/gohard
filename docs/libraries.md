# Library setup guide

This guide shows how to point GoHard at the VHDL libraries a design
uses (#1): your own code, the free verification libraries (OSVVM, UVVM,
VUnit) and the vendor libraries of Xilinx, Intel and Lattice. Every
example shows the `gohard.yaml` lines for Linux, macOS and Windows.

The rule is always the same. `libraries:` in `gohard.yaml` maps a
library name to a list of folders ([config.md](config.md#libraries)).
Every VHDL file under a listed folder, at any depth, belongs to that
library. Every other project file belongs to `work`. A folder may be
relative to the project root or absolute, and may lie outside the
project: then its units resolve names, but GoHard does not report the
library's own problems.

`std` and `ieee` are bundled with GoHard. Never map them.

## Zero-install: a project that only uses `work`

A project whose files all belong to `work` needs nothing installed and
no `gohard.yaml` at all. Put the files anywhere under the project root
and run `gohard check`, or open them in your editor
([editors.md](editors.md)).

<!-- example: work src/counter.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity counter is
  port (
    clk   : in  std_logic;
    count : out unsigned(7 downto 0)
  );
end entity counter;

architecture rtl of counter is
  signal n : unsigned(7 downto 0) := (others => '0');
begin
  n     <= n + 1 when rising_edge(clk);
  count <= n;
end architecture rtl;
```

<!-- example: work src/top.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;

entity top is
  port (
    clk  : in  std_logic;
    leds : out unsigned(7 downto 0)
  );
end entity top;

architecture rtl of top is
begin
  u_counter : entity work.counter
    port map (clk => clk, count => leds);
end architecture rtl;
```

```sh
gohard check
```

`work` needs no `library` clause, and a design entity in another file
is found by name. The project root is the folder that holds
`gohard.yaml`, else the nearest folder with `.git`, else the current
folder ([config.md](config.md)). Add a `gohard.yaml` only when you
need a setting.

## Writing paths in gohard.yaml

* Linux and macOS paths start with `/`. `~` is **not** expanded; write
  the full home folder (`/home/you`, `/Users/you`).
* Windows paths start with a drive letter. Write them in single quotes:
  `'C:\Program Files\x'`. Inside single quotes a backslash is a plain
  character and spaces are safe. In double quotes a backslash starts an
  escape, so `"C:\Xilinx"` would need every backslash doubled. Forward
  slashes (`C:/Xilinx`) work too.
* Quote any path that holds `: `, ` #`, or starts with a special YAML
  character. Single quotes are always safe; write a `'` inside them as
  `''`.
* A relative folder is relative to the project root, on every OS, and
  is best written with `/`.

## Finding vendor libraries: `gohard libs detect`

Vendor library sources come with the vendor tools. GoHard never ships
them and never copies or changes them: map the folders of **your own**
install. `gohard libs detect` finds them for you:

```sh
gohard libs detect                    # print gohard.yaml lines
gohard libs detect --write            # add them to gohard.yaml
gohard libs detect --write --version vivado=2024.1
```

It is read-only. It looks in the usual Vivado, Quartus and Diamond
install folders of your OS, in the folders the vendor variables name
(`XILINX_VIVADO`, `QUARTUS_ROOTDIR`, `FOUNDRY`, `GHDL_PREFIX`), next to
the vendor programs on `PATH` (`vivado`, `quartus_sh`, `diamondc`,
`pnmainc`, `ghdl`), and at GHDL library indexes (`*-obj*.cf`) in GHDL's
`vendors` folder and in the project.

| Tool | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Vivado | `/opt/Xilinx`, `/tools/Xilinx`, `~/Xilinx` | `/Applications/Xilinx`, `/opt/Xilinx`, `~/Xilinx` | `C:\Xilinx`, `C:\AMD` |
| Quartus | `/opt/intelFPGA*`, `/opt/altera`, `~/intelFPGA*`, `~/altera` | `/Applications/intelFPGA*`, `~/intelFPGA*` | `C:\intelFPGA*`, `C:\altera*` |
| Diamond | `/usr/local/diamond`, `/opt/lscc/diamond`, `~/lscc/diamond` | `/Applications/lscc/diamond`, `/opt/lscc/diamond`, `~/lscc/diamond` | `C:\lscc\diamond` |
| GHDL | `/usr/lib/ghdl`, `/usr/local/lib/ghdl`, `/opt/ghdl/lib/ghdl`, `~/.local/lib/ghdl` | `/opt/homebrew/lib/ghdl`, `/usr/local/lib/ghdl` | `C:\msys64\mingw64\lib\ghdl`, `C:\msys64\ucrt64\lib\ghdl`, `C:\Program Files\GHDL\lib\ghdl` |

Vivado, Quartus and Diamond do not run on macOS. The macOS paths in
this guide are for a copy of an install's library folders, or for a
Linux install on a mounted disk; `gohard libs detect` also finds those
through the variables and `PATH`.

When a tool is installed in several versions, `gohard libs detect`
lists them all as comments, and `--write` writes nothing and exits 2
until `--version TOOL=VERSION` (or just `--version VERSION`) picks
one. A library already mapped in `gohard.yaml` is left as it is.

Libraries GoHard cannot read get no line. They are logged as the
warning `libs/unreadable` instead of a line that would quietly fail:

* **IEEE 1735 encrypted IP**, such as Xilinx `secureip`. The
  components of encrypted blocks are still declared in plain text in
  the `vcomponents` packages, so a design that instantiates them still
  resolves. An encrypted unit inside a library that is mapped (its
  interface left readable) is added to `allow_opaque` by `--write`.
  To simulate a design that uses an encrypted block, give the block a
  behavioral stand-in under `simulation.models`
  ([config.md](config.md)); otherwise the simulation reports a
  diagnostic that names the block.
* **Tool-specific precompiled libraries**: ModelSim/Questa
  (`_info`, `.qdb`), Active-HDL/Riviera-PRO (`.lib`) and GHDL indexes
  whose VHDL sources are gone. Map the VHDL sources instead.

The language server's quick fix for "library ... is not mapped" uses
the same search: when no project folder declares the library, it
offers one fix per install found.

## Xilinx `unisim` and `unimacro`

Vivado keeps the VHDL sources under `data/vhdl/src` of the version
folder: `unisims` for library `unisim` (the component declarations are
in `unisim_VCOMP.vhd`) and `unimacro` for `unimacro`. From Vivado
2025.1 the version folder comes first (`/tools/Xilinx/2025.1/Vivado`).

Linux:

<!-- example: xilinx linux -->
```yaml
libraries:
  unisim: ['/opt/Xilinx/Vivado/2023.2/data/vhdl/src/unisims']
  unimacro: ['/opt/Xilinx/Vivado/2023.2/data/vhdl/src/unimacro']
```

macOS:

<!-- example: xilinx macos -->
```yaml
libraries:
  unisim: ['/Users/you/Xilinx/Vivado/2023.2/data/vhdl/src/unisims']
  unimacro: ['/Users/you/Xilinx/Vivado/2023.2/data/vhdl/src/unimacro']
```

Windows:

<!-- example: xilinx windows -->
```yaml
libraries:
  unisim: ['C:\Xilinx\Vivado\2023.2\data\vhdl\src\unisims']
  unimacro: ['C:\Xilinx\Vivado\2023.2\data\vhdl\src\unimacro']
```

The design:

<!-- example: xilinx src/top.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;

library unisim;
use unisim.vcomponents.all;

library unimacro;
use unimacro.vcomponents.all;

entity top is
  port (
    clk_p, clk_n : in  std_logic;
    d            : in  std_logic;
    q            : out std_logic;
    tc           : out std_logic
  );
end entity top;

architecture rtl of top is
  signal clk_in, clk : std_logic;
  signal count       : std_logic_vector(15 downto 0);
begin
  u_ibuf : IBUFDS
    generic map (DIFF_TERM => true)
    port map (O => clk_in, I => clk_p, IB => clk_n);

  u_bufg : BUFG
    port map (O => clk, I => clk_in);

  u_ff : FDRE
    port map (Q => q, C => clk, CE => '1', D => d, R => '0');

  u_count : COUNTER_TC_MACRO
    generic map (WIDTH_DATA => 16, TC_VALUE => x"00000000FFFF")
    port map (Q => count, TC => tc, CE => '1', CLK => clk, RST => '0');
end architecture rtl;
```

Do not map `data/secureip`: it is encrypted (see above).

## Intel/Altera `altera_mf`

Quartus keeps the simulation sources in `quartus/eda/sim_lib` of the
version folder; `altera_mf_components.vhd` declares the components.
The `modelsim_ase` or `questa_fse` folders hold precompiled
ModelSim/Questa libraries, which GoHard cannot read.

Linux:

<!-- example: intel linux -->
```yaml
libraries:
  altera_mf: ['/home/you/intelFPGA_lite/23.1std/quartus/eda/sim_lib']
```

macOS:

<!-- example: intel macos -->
```yaml
libraries:
  altera_mf: ['/Users/you/intelFPGA_lite/23.1std/quartus/eda/sim_lib']
```

Windows:

<!-- example: intel windows -->
```yaml
libraries:
  altera_mf: ['C:\intelFPGA_lite\23.1std\quartus\eda\sim_lib']
```

The design:

<!-- example: intel src/ram.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;

library altera_mf;
use altera_mf.altera_mf_components.all;

entity ram is
  port (
    clk  : in  std_logic;
    addr : in  std_logic_vector(7 downto 0);
    din  : in  std_logic_vector(15 downto 0);
    we   : in  std_logic;
    dout : out std_logic_vector(15 downto 0)
  );
end entity ram;

architecture rtl of ram is
begin
  u_ram : altsyncram
    generic map (
      operation_mode => "SINGLE_PORT",
      width_a        => 16,
      widthad_a      => 8
    )
    port map (
      clock0    => clk,
      address_a => addr,
      data_a    => din,
      wren_a    => we,
      q_a       => dout
    );
end architecture rtl;
```

## Lattice (Diamond)

Diamond keeps one library per device family under
`cae_library/simulation/vhdl/<family>/src`, named like the family
(`ecp5u`, `machxo2`, `machxo3l`, ...). Map the families you use.

Linux:

<!-- example: lattice linux -->
```yaml
libraries:
  ecp5u: ['/usr/local/diamond/3.13/cae_library/simulation/vhdl/ecp5u/src']
```

macOS:

<!-- example: lattice macos -->
```yaml
libraries:
  ecp5u: ['/Users/you/lscc/diamond/3.13/cae_library/simulation/vhdl/ecp5u/src']
```

Windows:

<!-- example: lattice windows -->
```yaml
libraries:
  ecp5u: ['C:\lscc\diamond\3.13\cae_library\simulation\vhdl\ecp5u\src']
```

The design:

<!-- example: lattice src/blink.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;

library ecp5u;
use ecp5u.components.all;

entity blink is
  port (
    d : in  std_logic;
    q : out std_logic
  );
end entity blink;

architecture rtl of blink is
  signal osc : std_logic;
begin
  u_osc : OSCG
    generic map (DIV => 64)
    port map (OSC => osc);

  u_ff : FD1S3AX
    port map (D => d, CK => osc, Q => q);
end architecture rtl;
```

## OSVVM

Clone OSVVM (`git clone https://github.com/OSVVM/OSVVM`), or the
`OsvvmLibraries` collection, and map its `osvvm` folder. A copy inside
the project (a git submodule, say) can be mapped with a relative path
such as `osvvm: [third_party/osvvm]`.

Linux:

<!-- example: osvvm linux -->
```yaml
libraries:
  osvvm: ['/home/you/vhdl/OsvvmLibraries/osvvm']
```

macOS:

<!-- example: osvvm macos -->
```yaml
libraries:
  osvvm: ['/Users/you/vhdl/OsvvmLibraries/osvvm']
```

Windows:

<!-- example: osvvm windows -->
```yaml
libraries:
  osvvm: ['C:\Users\you\Documents\VHDL Libraries\OsvvmLibraries\osvvm']
```

The testbench:

<!-- example: osvvm src/tb.vhd -->
```vhdl
library osvvm;
context osvvm.OsvvmContext;

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
    variable rv : RandomPType;
  begin
    SetAlertLogName("tb");
    rv.InitSeed(1);
    AffirmIf(rv.RandInt(0, 3) <= 3, "range");
    EndOfTestReports;
    wait;
  end process;
end architecture sim;
```

## UVVM

Clone UVVM (`git clone https://github.com/UVVM/UVVM`). Each UVVM
library has its sources in its own `src` folder: map `uvvm_util`, and
add `uvvm_vvc_framework`, `bitvis_vip_*` and the others the same way
when you use them. UVVM declares VHDL-2008 shared variables that are
not of a protected type, so set `lenient: true`
([relaxed rules](#relaxed-rules-lenient)).

Linux:

<!-- example: uvvm linux -->
```yaml
lenient: true
libraries:
  uvvm_util: ['/home/you/vhdl/UVVM/uvvm_util/src']
```

macOS:

<!-- example: uvvm macos -->
```yaml
lenient: true
libraries:
  uvvm_util: ['/Users/you/vhdl/UVVM/uvvm_util/src']
```

Windows:

<!-- example: uvvm windows -->
```yaml
lenient: true
libraries:
  uvvm_util: ['C:\Users\you\Documents\VHDL Libraries\UVVM\uvvm_util\src']
```

The testbench:

<!-- example: uvvm src/tb.vhd -->
```vhdl
library uvvm_util;
context uvvm_util.uvvm_util_context;

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
  begin
    log(ID_LOG_HDR, "start");
    check_value(1 = 1, ERROR, "one");
    report_alert_counters(FINAL);
    std.env.stop;
    wait;
  end process;
end architecture sim;
```

## Relaxed rules: `lenient`

GHDL and NVC refuse some code that UVVM and open-logic rely on unless
they run with GHDL's `-frelaxed` or NVC's `--relaxed`. GoHard's
equivalent is `lenient: true` in `gohard.yaml`
([config.md](config.md#lenient)). UVVM's packages and open-logic's
testbenches declare VHDL-2008 shared variables that are not of a
protected type. For open-logic, map its `src` folder as `olo`
(`olo: ['/home/you/vhdl/open-logic/src']`), and its `test` folder
too when you check its testbenches, which also use OSVVM and VUnit.
`lenient: true` relaxes these rules:

| Rule (strict: an error) | Under `lenient: true` | GHDL, NVC |
| --- | --- | --- |
| From VHDL-2002 on a shared variable is of a protected type (LRM 6.4.2.4; legal under VHDL-1993) | allowed, with a `sema/relaxed` warning | `-frelaxed`, `--relaxed` (a warning) |
| A subprogram body conforms to its declaration (LRM 4.10) | a parameter that names no mode may add or omit `constant` | `-frelaxed`, `--relaxed` (a warning) |
| No subelement of a formal associated individually is associated with `open` (LRM 6.5.7.1) | allowed | `-frelaxed`, `--relaxed` (a warning) |
| A pure function declares no file (LRM 4.1) | allowed | `-frelaxed`, `--relaxed` (a warning) |
| A pure function names no external signal or variable (LRM 4.1) | allowed | `-frelaxed` (a warning); NVC always reports it |
| `others` in an aggregate needs an index range from the context (LRM 9.3.3.3) | an aggregate assigned to an unconstrained port, parameter or alias takes the target's range | `-frelaxed`, `--relaxed` |
| A declaration is not visible within itself (LRM 12.3) | the outer homograph is seen there | `--relaxed`; GHDL accepts it in every mode, with a `-Whide` warning |
| An alias of a subprogram or enumeration literal has a signature (LRM 6.6.3) | it may omit it and denotes the first visible one | NVC accepts it in every mode; GHDL always reports it |
| A component instantiation names a component unless `entity` precedes the name (LRM 11.7.1) | it may name an entity | neither accepts it |

Only the shared variable rule is reported under `lenient: true`; the
others are accepted silently. Strict analysis is the default.

## VUnit

VUnit's VHDL sources ship inside its Python package, in the
`vunit/vhdl` folder. Find it with
`python -c "import vunit, os; print(os.path.dirname(vunit.__file__))"`
and map its `vhdl` folder as `vunit_lib` (or map the `vunit/vhdl`
folder of a git clone).

Linux:

<!-- example: vunit linux -->
```yaml
libraries:
  vunit_lib: ['/home/you/.local/lib/python3.12/site-packages/vunit/vhdl']
```

macOS:

<!-- example: vunit macos -->
```yaml
libraries:
  vunit_lib: ['/Users/you/Library/Python/3.12/lib/python/site-packages/vunit/vhdl']
```

Windows:

<!-- example: vunit windows -->
```yaml
libraries:
  vunit_lib: ['C:\Users\you\AppData\Local\Programs\Python\Python312\Lib\site-packages\vunit\vhdl']
```

The testbench:

<!-- example: vunit src/tb.vhd -->
```vhdl
library vunit_lib;
context vunit_lib.vunit_context;

entity tb is
  generic (runner_cfg : string);
end entity tb;

architecture sim of tb is
begin
  main : process
  begin
    test_runner_setup(runner, runner_cfg);
    check_equal(1, 1, "one");
    info("done");
    test_runner_cleanup(runner);
  end process main;
end architecture sim;
```

## Vendor libraries compiled with GHDL

GHDL's vendor scripts (`compile-xilinx-vivado.sh` and the like)
compile vendor libraries into GHDL's own format: an index file such as
`unisim-obj08.cf` plus object files. GoHard cannot read the compiled
form; map the VHDL sources the index was compiled from. The index
names every source file, and `gohard libs detect` follows it back to
them. Here GHDL compiled Vivado's `unisim`, so the mapping is the
Vivado folder:

Linux:

<!-- example: ghdl linux -->
```yaml
# /usr/local/lib/ghdl/vendors/xilinx-vivado/unisim/v08/unisim-obj08.cf
libraries:
  unisim: ['/opt/Xilinx/Vivado/2023.2/data/vhdl/src/unisims']
```

macOS:

<!-- example: ghdl macos -->
```yaml
# /opt/homebrew/lib/ghdl/vendors/xilinx-vivado/unisim/v08/unisim-obj08.cf
libraries:
  unisim: ['/Users/you/Xilinx/Vivado/2023.2/data/vhdl/src/unisims']
```

Windows:

<!-- example: ghdl windows -->
```yaml
# C:\Program Files\GHDL\lib\ghdl\vendors\xilinx-vivado\unisim\v08\unisim-obj08.cf
libraries:
  unisim: ['C:\Xilinx\Vivado\2023.2\data\vhdl\src\unisims']
```

The design:

<!-- example: ghdl src/top.vhd -->
```vhdl
library ieee;
use ieee.std_logic_1164.all;

library unisim;
use unisim.vcomponents.all;

entity top is
  port (
    clk_in : in  std_logic;
    clk    : out std_logic
  );
end entity top;

architecture rtl of top is
begin
  u_bufg : BUFG
    port map (O => clk, I => clk_in);
end architecture rtl;
```

A library whose index lists no sources, or whose sources are gone, is
reported as `libs/unreadable`.

## How CI checks this guide

Every example above is also a project under `testdata/libsguide/`
that CI loads with `gohard check`; CI fails if any `use` in it does
not resolve (`TestLibrariesGuide` in `internal/cli`). The test also
checks that this guide shows exactly those VHDL files, that every
example maps the same libraries on Linux, macOS and Windows, and that
the Windows paths read back as drive-letter paths.

* OSVVM, UVVM and VUnit are the real libraries, pinned as git
  submodules under `testdata/libs/`.
* Vendor sources cannot be committed. For the Xilinx, Intel, Lattice
  and GHDL examples CI maps small stub packages GoHard wrote itself,
  under `testdata/stubs/`, which declare only the components the
  examples use. They are for CI only, never a substitute for the
  vendor files, and `gohard libs detect --write` never writes them
  into a `gohard.yaml`.
