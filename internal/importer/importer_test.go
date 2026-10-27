package importer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// show renders a project as one line per entry and diagnostic.
func show(p Project) string {
	var b strings.Builder
	for _, e := range p.Entries {
		fmt.Fprintf(&b, "%d:%d %s %s %q\n", e.Line, e.Col, e.Library,
			e.Path, e.Standard)
	}
	for _, d := range p.Diagnostics {
		fmt.Fprintf(&b, "%d:%d %s[%s]: %s\n", d.Line, d.Col, d.Severity,
			d.Code, d.Message)
	}
	return b.String()
}

func check(t *testing.T, got Project, want string) {
	t.Helper()
	if s := show(got); s != want {
		t.Errorf("got:\n%s\nwant:\n%s", s, want)
	}
}

func TestParseDo(t *testing.T) {
	src := `# compile script
vlib work
vlib libs/mylib
vmap mylib libs/mylib
vcom -2008 -work mylib ../src/pkg.vhd   ;# the package
vcom -work libs/mylib -quiet -suppress 1130 src/util.vhd
vcom -93 top.vhd \
     "tb top.vhdl" {braced.vhd}
vcom -work other -f args.txt -2019 o.vhd
vlog top.v
vcom $SRC/a.vhd
vcom src/x.v
vmap mylib
vsim -c work.tb; run -all
if {$gui} {
  vcom hidden.vhd
}
vcom -reportprogress 300 -just e -skip b -work work last.vhd
`
	want := `5:24 mylib ../src/pkg.vhd "2008"
6:45 mylib src/util.vhd ""
7:10 work top.vhd "1993"
8:6 work tb top.vhdl "1993"
8:20 work braced.vhd "1993"
9:36 other o.vhd "2019"
18:53 work last.vhd ""
9:18 hint[import/ignored]: vcom -f args.txt ignored: argument files are not read
10:1 hint[import/ignored]: vlog ignored: gohard import reads only vlib, vmap and vcom
11:1 hint[import/ignored]: vcom ignored: it uses Tcl substitution ($ or [...]), which gohard import does not evaluate
12:6 hint[import/ignored]: src/x.v ignored: not a .vhd or .vhdl file
13:1 hint[import/ignored]: vmap ignored: not of the form vmap NAME PATH
14:1 hint[import/ignored]: vsim ignored: gohard import reads only vlib, vmap and vcom
14:18 hint[import/ignored]: run ignored: gohard import reads only vlib, vmap and vcom
15:1 hint[import/ignored]: if ignored: gohard import reads only vlib, vmap and vcom
`
	check(t, ParseDo("sim/compile.do", []byte(src)), want)
}

func TestParseDoSyntax(t *testing.T) {
	check(t, ParseDo("a.do", []byte("vcom a.vhd\nvcom {b.vhd\n")),
		`1:6 work a.vhd ""
2:6 error[import/syntax]: not a valid ModelSim .do script: unterminated {
`)
}

func TestParseQSF(t *testing.T) {
	src := `# Quartus settings
set_global_assignment -name FAMILY "Cyclone V"
set_global_assignment -name VHDL_FILE rtl/pkg.vhd -library mylib -hdl_version VHDL_2008
set_global_assignment -name VHDL_FILE rtl/top.vhd
set_global_assignment -name VHDL_INPUT_VERSION VHDL_1993
set_global_assignment -library lib2 -name VHDL_FILE "rtl/b c.vhdl"
set_global_assignment -name VERILOG_FILE rtl/x.v
set_global_assignment -name VHDL_FILE rtl/old.vhd -disable
set_global_assignment -name VHDL_FILE rtl/new.vhd -hdl_version VHDL_2099
set_location_assignment PIN_A1 -to clk
set_instance_assignment -name IO_STANDARD "3.3-V LVTTL" -to clk
source extra.tcl
set_global_assignment -name VHDL_FILE $dir/z.vhd
`
	want := `3:39 mylib rtl/pkg.vhd "2008"
4:39 work rtl/top.vhd "1993"
6:53 lib2 rtl/b c.vhdl "1993"
9:39 work rtl/new.vhd ""
7:1 hint[import/ignored]: VERILOG_FILE rtl/x.v ignored: not a VHDL_FILE assignment
8:1 hint[import/ignored]: rtl/old.vhd ignored: the assignment is disabled or removed
9:1 hint[import/ignored]: -hdl_version VHDL_2099 ignored: not VHDL_1987, VHDL_1993, VHDL_2008 or VHDL_2019
12:1 hint[import/ignored]: source ignored: not a Quartus assignment
13:1 hint[import/ignored]: set_global_assignment ignored: it uses Tcl substitution ($ or [...]), which gohard import does not evaluate
`
	check(t, ParseQSF("q/top.qsf", []byte(src)), want)
}

const xprSrc = `<?xml version="1.0" encoding="UTF-8"?>
<!-- Product Version: Vivado v2024.1 -->
<Project Version="7" Minor="67" Path="/home/u/proj/top.xpr">
  <DefaultLaunch Dir="$PRUNDIR"/>
  <FileSets Version="1" Minor="32">
    <FileSet Name="sources_1" Type="DesignSrcs" RelSrcDir="$PSRCDIR/sources_1">
      <Filter Type="Srcs"/>
      <File Path="$PPRDIR/../src/pkg.vhd">
        <FileInfo SFType="VHDL2008">
          <Attr Name="Library" Val="mylib"/>
          <Attr Name="UsedIn" Val="synthesis"/>
        </FileInfo>
      </File>
      <File Path="$PSRCDIR/sources_1/new/top.vhd">
        <FileInfo>
          <Attr Name="UsedIn" Val="synthesis"/>
        </FileInfo>
      </File>
      <File Path="$PPRDIR/../src/core.v">
        <FileInfo/>
      </File>
      <File Path="$PPRDIR/../src/old.vhd">
        <FileInfo>
          <Attr Name="AutoDisabled" Val="1"/>
        </FileInfo>
      </File>
      <File Path="$PWRAPPERDIR/w.vhd"/>
      <Config>
        <Option Name="TopModule" Val="top"/>
      </Config>
    </FileSet>
    <FileSet Name="constrs_1" Type="Constrs" RelSrcDir="$PSRCDIR/constrs_1">
      <File Path="$PPRDIR/../top.xdc"/>
    </FileSet>
    <FileSet Name="sim_1" Type="SimulationSrcs" RelSrcDir="$PSRCDIR/sim_1">
      <File Path="$PPRDIR/../tb/tb.vhd">
        <FileInfo SFType="VHDL 2019"/>
      </File>
      <File Path="$PPRDIR/../src/x.sv">
        <FileInfo SFType="SVerilog"/>
      </File>
    </FileSet>
  </FileSets>
</Project>
`

func TestParseXPR(t *testing.T) {
	want := `8:7 mylib ../src/pkg.vhd "2008"
14:7 xil_defaultlib top.srcs/sources_1/new/top.vhd "1993"
36:7 xil_defaultlib ../tb/tb.vhd "2019"
19:7 hint[import/ignored]: $PPRDIR/../src/core.v ignored: not a VHDL file
22:7 hint[import/ignored]: $PPRDIR/../src/old.vhd ignored: the file is disabled in the project
27:7 hint[import/ignored]: $PWRAPPERDIR/w.vhd ignored: the path variable is not $PPRDIR, $PSRCDIR, $PGENDIR or $PIPUSERFILESDIR
39:7 hint[import/ignored]: $PPRDIR/../src/x.sv ignored: file type SVerilog is not VHDL
`
	check(t, ParseXPR("proj/top.xpr", []byte(xprSrc)), want)
}

func TestParseXPRSyntax(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", "1:1 error[import/syntax]: not a valid Vivado .xpr " +
			"project: no Project element\n"},
		{"<Other/>", "1:1 error[import/syntax]: not a valid Vivado " +
			".xpr project: the root element is Other, not Project\n"},
		{"<Project>\n<FileSets>", "2:11 error[import/syntax]: not a " +
			"valid Vivado .xpr project: XML syntax error on line 2: " +
			"unexpected EOF\n"},
	} {
		check(t, ParseXPR("p.xpr", []byte(tc.src)), tc.want)
	}
}

func TestPlan(t *testing.T) {
	es := []Entry{
		{Path: "a.vhd", Library: "Lib", Standard: "1993", Line: 1, Col: 1},
		{Path: "b.vhd", Library: "lib", Standard: "2008", Line: 2, Col: 1},
		{Path: "c.vhd", Library: "alpha", Standard: "", Line: 3, Col: 1},
		{Path: "a.vhd", Library: "LIB", Standard: "1993", Line: 4, Col: 1},
		{Path: "a.vhd", Library: "other", Standard: "", Line: 5, Col: 1},
		{Path: "d.vhd", Library: "lib", Standard: "1993", Line: 6, Col: 1},
	}
	c, ds := Plan("p.do", es)
	got := string(Render("p.do", c))
	want := `# Written by gohard import from p.do.
libraries:
  alpha:
    - c.vhd
  Lib:
    - a.vhd
    - b.vhd
    - d.vhd
`
	if got != want {
		t.Errorf("render:\n%s\nwant:\n%s", got, want)
	}
	if c.Files != 4 {
		t.Errorf("files = %d, want 4", c.Files)
	}
	var lines []string
	for _, d := range ds {
		lines = append(lines, fmt.Sprintf("%d:%d %s[%s]: %s", d.Line,
			d.Col, d.Severity, d.Code, d.Message))
	}
	wantDs := []string{
		"1:1 hint[import/standard]: VHDL-1993 files: 2, the first one " +
			"here; gohard.yaml has one standard for every file, 2008",
		"5:1 warning[import/library-conflict]: a.vhd is listed in " +
			"library Lib and in library other; gohard.yaml maps it " +
			"to Lib",
	}
	if strings.Join(lines, "\n") != strings.Join(wantDs, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", strings.Join(lines, "\n"),
			strings.Join(wantDs, "\n"))
	}
	if ds[1].Related[0].Line != 1 {
		t.Errorf("conflict related = %+v", ds[1].Related)
	}
}

func TestRenderStandardAndQuoting(t *testing.T) {
	c, _ := Plan("p.xpr", []Entry{
		{Path: "../a b/x.vhd", Library: "work", Standard: "1993"},
		{Path: "-odd'.vhd", Library: "work", Standard: "1993"},
	})
	got := string(Render("p.xpr", c))
	want := `# Written by gohard import from p.xpr.
standard: "1993"
libraries:
  work:
    - '../a b/x.vhd'
    - '-odd''.vhd'
`
	if got != want {
		t.Errorf("render:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatOf(t *testing.T) {
	for _, tc := range [][2]string{
		{"a.do", ".do"}, {"B.XPR", ".xpr"}, {`dir\x.Qsf`, ".qsf"},
		{"a.tcl", ""}, {"do", ""},
	} {
		name, want := tc[0], tc[1]
		f, ok := FormatOf(name)
		if ok != (want != "") || f.Ext != want {
			t.Errorf("FormatOf(%q) = %q, %v", name, f.Ext, ok)
		}
	}
	if got := Extensions(); got != ".do, .qsf and .xpr" {
		t.Errorf("Extensions() = %q", got)
	}
}

// TestCodesInTable keeps the codes this package emits in the table.
func TestCodesInTable(t *testing.T) {
	for _, c := range []diag.Code{diag.ImportIgnored, diag.ImportSyntax,
		diag.ImportStandard, diag.ImportLibraryConflict} {
		if _, ok := c.Info(); !ok {
			t.Errorf("%s missing from the diag table", c)
		}
	}
}
