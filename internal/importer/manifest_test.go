package importer

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// memFiles is a Files over a map of slash paths relative to the
// project file's folder.
type memFiles struct {
	files map[string]string
	cores []string
}

func (m memFiles) Read(p string) ([]byte, error) {
	if s, ok := m.files[path.Clean(p)]; ok {
		return []byte(s), nil
	}
	return nil, fs.ErrNotExist
}

// Folder reports a folder that holds a file of m; the project file's
// folder is memProject.
func (m memFiles) Folder(p string) (string, bool) {
	p = path.Clean(p)
	name := path.Base(path.Join(memProject, p))
	return name, p == "." || slices.ContainsFunc(
		slices.Sorted(maps.Keys(m.files)),
		func(k string) bool { return strings.HasPrefix(k, p+"/") })
}

// memProject is the absolute folder of the project file of a memFiles.
const memProject = "/work/proj"

func (m memFiles) Cores() []string { return m.cores }

// showAll renders a project like show, naming the file of each entry
// and diagnostic.
func showAll(p Project) string {
	var b strings.Builder
	for _, e := range p.Entries {
		fmt.Fprintf(&b, "%s:%d:%d %s %s %q\n", e.File, e.Line, e.Col,
			e.Library, e.Path, e.Standard)
	}
	for _, d := range p.Diagnostics {
		fmt.Fprintf(&b, "%s:%d:%d %s[%s]: %s\n", d.Path, d.Line, d.Col,
			d.Severity, d.Code, d.Message)
	}
	return b.String()
}

func checkAll(t *testing.T, got Project, want string) {
	t.Helper()
	if s := showAll(got); s != want {
		t.Errorf("got:\n%s\nwant:\n%s", s, want)
	}
}

func TestParsePro(t *testing.T) {
	src := `# OSVVM-style build script
library osvvm
analyze IfElsePkg.vhd
if {$::osvvm::ToolSupportsGenericPackages}  {
  analyze Generic.vhd
} else {
  analyze deprecated/NoGeneric.vhd
}
analyze [FindOsvvmScriptSettingsPkg]    ;# the default body
analyze  [FindSettingsPkgBody OsvvmSettingsPkg]
if {$::osvvm::ToolName ne "XSIM"} {analyze A.vhd} else {analyze A_x.vhd}
analyze Cov_${::osvvm::FunctionalCoverageIntegratedInSimulator}.vhd
if {$::osvvm::VhdlVersion >= 2019} {
  analyze New.vhd
} elseif {$::osvvm::ToolName in {GHDL GoHard}} then {
  analyze Mid.vhd
} else {
  analyze Old_c.vhd
}
analyze RandomPkg.vhd [NoNullRangeWarning]
analyze $::poc::boardFile
if {[DirectoryExists Extra]} {
  include ./Extra
}
puts "done"
include ./sub
include missing.pro
include helper.tcl
include osvvm.pro
SetVHDLVersion 2019
if {$::osvvm::VhdlVersion >= 2019 && $::osvvm::Supports2019Generics} {
  analyze Gen2019.vhd
}
if {[string compare $::osvvm::ClockResetVersion "2024.05"] == 1} {
  analyze ClockResetPkg.vhd
} else {
  analyze deprecated/ClockResetPkg_2024_05.vhd
}
SetVHDLVersion 2003
analyze top.v
if {1} {analyze a.vhd} {analyze b.vhd} extra
`
	files := memFiles{files: map[string]string{
		"sub/build.pro": "analyze s.vhd\nlibrary sublib\n" +
			"include ../sub2.pro\n",
		"sub2.pro": "analyze two.vhd\n",
	}}
	want := `lib/osvvm.pro:3:9 osvvm IfElsePkg.vhd ""
lib/osvvm.pro:5:11 osvvm Generic.vhd ""
lib/osvvm.pro:9:9 osvvm OsvvmScriptSettingsPkg_default.vhd ""
lib/osvvm.pro:10:10 osvvm OsvvmSettingsPkg_default.vhd ""
lib/osvvm.pro:11:44 osvvm A.vhd ""
lib/osvvm.pro:12:9 osvvm Cov_default.vhd ""
lib/osvvm.pro:16:11 osvvm Mid.vhd ""
lib/osvvm.pro:20:9 osvvm RandomPkg.vhd ""
lib/sub/build.pro:1:9 osvvm sub/s.vhd ""
lib/sub2.pro:1:9 sublib two.vhd ""
lib/osvvm.pro:32:11 sublib Gen2019.vhd "2019"
lib/osvvm.pro:35:11 sublib ClockResetPkg.vhd "2019"
lib/osvvm.pro:21:1 hint[import/ignored]: analyze ignored: it uses $::poc::boardFile, which the import profile does not set
lib/osvvm.pro:22:4 hint[import/ignored]: if ignored: it uses [DirectoryExists ...], which gohard import does not evaluate
lib/osvvm.pro:25:1 hint[import/ignored]: puts ignored: gohard import reads only library, analyze, RunTest, include, build, SetVHDLVersion and if
lib/osvvm.pro:27:9 error[import/missing-file]: source file lib/missing.pro does not exist
lib/osvvm.pro:28:9 hint[import/ignored]: include helper.tcl ignored: gohard import reads only .pro scripts
lib/osvvm.pro:29:9 hint[import/ignored]: include osvvm.pro ignored: the script is already being read, so including it again would never end
lib/osvvm.pro:39:1 hint[import/ignored]: SetVHDLVersion 2003 ignored: not 1993, 2002, 2008 or 2019 (or 87, 93, 02, 08, 19)
lib/osvvm.pro:40:9 hint[import/ignored]: top.v ignored: not a .vhd or .vhdl file
lib/osvvm.pro:41:1 hint[import/ignored]: if ignored: not of the form if {expr} {body} ?elseif {expr} {body}? ?else {body}?
`
	checkAll(t, ParsePro("lib/osvvm.pro", []byte(src), files), want)
}

func TestParseProDefaults(t *testing.T) {
	// Before any library command OSVVM analyzes into "default". A
	// folder include reads one script, as OSVVM's FindIncludeFile
	// picks it: NAME.pro (NAME the folder's name without extension,
	// the real name for "." and ".."), else build.pro; ip/build.pro and
	// its library tblib are not read.
	files := memFiles{files: map[string]string{
		"src/src.pro":    "analyze a.vhd\n",
		"src/build.pro":  "analyze b.vhd\n",
		"only/build.pro": "RunTest tb.vhd\n",
		"named.pro":      "build only\n",
		"ip/ip.pro":      "library iplib\nanalyze a.vhd\n",
		"ip/build.pro": "include ip.pro\nlibrary tblib\n" +
			"analyze tb.vhd\n",
		"sub/sub.pro":     "include ..\n",
		"proj.pro":        "analyze top.vhd\ninclude .\n",
		"build.pro":       "analyze wrong.vhd\n",
		"ip.v2/ip.pro":    "analyze v2.vhd\n",
		"ip.v2/build.pro": "analyze wrong.vhd\n",
	}}
	checkAll(t, ParsePro("p.pro", []byte("include src\ninclude named\n"+
		"include empty\ninclude ip\ninclude sub\ninclude ip.v2\n"),
		files), `src/src.pro:1:9 default src/a.vhd ""
only/build.pro:1:9 default only/tb.vhd ""
ip/ip.pro:2:9 iplib ip/a.vhd ""
proj.pro:1:9 iplib top.vhd ""
ip.v2/ip.pro:1:9 iplib ip.v2/v2.vhd ""
p.pro:3:9 error[import/missing-file]: source file empty.pro does not exist
proj.pro:2:9 hint[import/ignored]: include proj.pro ignored: the script is already being read, so including it again would never end
`)
}

func TestParseProSyntax(t *testing.T) {
	checkAll(t, ParsePro("a.pro", []byte("analyze a.vhd\nif {1} {\n"+
		"  analyze \"b.vhd\n}\n"), memFiles{}),
		`a.pro:1:9 default a.vhd ""
a.pro:3:11 error[import/syntax]: not a valid OSVVM .pro script: unterminated "
`)
	checkAll(
		t,
		ParsePro("a.pro", []byte("if {1} {analyze \"b.vhd}\n"),
			memFiles{}),
		`a.pro:1:17 error[import/syntax]: not a valid OSVVM .pro script: unterminated "
`,
	)
}

// TestParseProBudget checks that scripts that include each other
// without end stop at the budget (P7) instead of running forever.
func TestParseProBudget(t *testing.T) {
	// a.pro includes b/a.pro twice, which includes b/b/a.pro twice,
	// and so on: no cycle, but 2^depth commands.
	files := memFiles{files: map[string]string{}}
	dir := "."
	for i := range 30 {
		dir = path.Join(dir, "b")
		files.files[path.Join(dir, "a.pro")] = "include b/a.pro\n" +
			"include b/a.pro\nanalyze x.vhd\n"
		if i == 29 {
			files.files[path.Join(dir, "a.pro")] = "analyze x.vhd\n"
		}
	}
	p := ParsePro("a.pro", []byte("include b/a.pro\n"), files)
	last := p.Diagnostics[len(p.Diagnostics)-1]
	if !strings.Contains(last.Message, "the scripts run more than "+
		strconv.Itoa(proBudget)+" commands") {
		t.Errorf("last diagnostic = %s", last.Message)
	}
}

// TestProConditions checks the if condition evaluator against Tcl's
// expr results for the operators .pro scripts use.
func TestProConditions(t *testing.T) {
	for _, tc := range []struct {
		cond string
		want string // "1", "0" or the reason it is unknown
	}{
		{`1`, "1"}, {`0`, "0"}, {`!0`, "1"}, {`yes`, "1"},
		{`off`, "0"}, {`-1 < 0`, "1"}, {`10 > 9`, "1"},
		{`"10" > "9"`, "1"}, {`"abc" < "abd"`, "1"},
		{`"b" > "a10"`, "1"}, {`2024.05 == 2024.050`, "1"},
		{`$::osvvm::ToolName eq "GoHard"`, "1"},
		{`$::osvvm::ToolName ne {GoHard}`, "0"},
		{`$::osvvm::ToolName in {GHDL GoHard}`, "1"},
		{`$::osvvm::ToolName ni {GHDL GoHard}`, "0"},
		{`$::osvvm::VhdlVersion >= 2019`, "0"},
		{`$::osvvm::VhdlVersion == 2008`, "1"},
		{`$::osvvm::ToolSupportsGenericPackages`, "1"},
		{`!$::osvvm::UseMemoryPkg01`, "1"},
		{`($::poc::vendorName eq "GENERIC") && (1 || 0)`, "1"},
		{`1 || 0 && 0`, "1"}, {`(1 || 0) && 0`, "0"},
		{`$::x::y || 1`, "1"}, {`$::x::y && 0`, "0"},
		{`0 && [Unknown]`, "0"},
		{`$::x::y && 1`, "it uses $::x::y, which the import " +
			"profile does not set"},
		{`[DirectoryExists a]`, "it uses [DirectoryExists ...], " +
			"which gohard import does not evaluate"},
		{`[string compare $::osvvm::ClockResetVersion "2024.05"] == 1`,
			"1"},
		{`abs(1)`, notExpr}, {`1 +`, notExpr}, {`"x"`, notExpr},
		{`1 2`, notExpr}, {`$a(1)`, "it uses the array element " +
			"$a(...), which the import profile does not set"},
	} {
		cmds, _ := splitTcl([]byte("if {" + tc.cond + "} {}"))
		r := &proRun{}
		v, why := r.condition([]byte("if {"+tc.cond+"} {}"),
			cmds[0].Words[1], 0)
		got := why
		if why == "" {
			got = "0"
			if v {
				got = "1"
			}
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.cond, got, tc.want)
		}
	}
}

func TestParseCore(t *testing.T) {
	src := `CAPI=2:
name: "acme:ip:top:1.0.0"
description: the top
filesets:
  rtl:
    files:
      - "rtl/top.vhd"
      - "rtl/pkg.vhd" : {logical_name: acme}
      - rtl/old.vhd : {file_type: vhdlSource-93}
      - constraints.xdc : {file_type: xdc}
      - "tool_vivado? (rtl/viv.vhd)"
    file_type: vhdlSource-2008
    logical_name: toplib
    depend:
      - "^acme:ip:base:1.2.0"
      - ">=acme:ip:util:2"
      - "acme:ip:missing:1.0"
      - "tool_vivado? (acme:ip:viv)"
      - "not a vlnv"
  tb:
    files: [tb/tb.vhd, tb/notype.vhd: {logical_name: tb}]
    file_type: vhdlSource
  docs:
    files:
      - readme.md
targets:
  default:
    filesets: [rtl, tb]
    toplevel: top
provider:
  name: github
`
	core := func(name, deps string) string {
		return "CAPI=2:\nname: " + name + "\nfilesets:\n  rtl:\n" +
			"    files: [b.vhd]\n    file_type: vhdlSource-2008\n" +
			"    logical_name: base\n" + deps
	}
	files := memFiles{
		files: map[string]string{
			"../base/base.core":     core("acme:ip:base:1.3.0", ""),
			"../base_old/base.core": core("acme:ip:base:1.1.0", ""),
			"../base2/base.core":    core("acme:ip:base:2.0.0", ""),
			"../util/util.core": core("acme:ip:util:2.5",
				"    depend: [acme:ip:base, acme:ip:top]\n"),
			"../bad/bad.core": "name: acme:ip:util:9.0\n",
		},
		cores: []string{"../bad/bad.core", "../base/base.core",
			"../base2/base.core", "../base_old/base.core",
			"../util/util.core"},
	}
	want := `base/base.core:5:13 base ../base/b.vhd "2008"
util/util.core:5:13 base ../util/b.vhd "2008"
ip/top.core:7:9 toplib rtl/top.vhd "2008"
ip/top.core:8:9 acme rtl/pkg.vhd "2008"
ip/top.core:9:9 toplib rtl/old.vhd "1993"
ip/top.core:21:13 work tb/tb.vhd ""
ip/top.core:21:24 tb tb/notype.vhd ""
ip/top.core:10:9 hint[import/ignored]: constraints.xdc ignored: file type xdc is not VHDL
ip/top.core:11:9 hint[import/ignored]: tool_vivado? (rtl/viv.vhd) ignored: it has a FuseSoC use-flag condition
ip/top.core:18:9 hint[import/ignored]: depend tool_vivado? (acme:ip:viv) ignored: it has a FuseSoC use-flag condition
ip/top.core:19:9 hint[import/ignored]: depend not a vlnv ignored: not of the form [op]vendor:library:name[:version]
ip/top.core:25:9 hint[import/ignored]: readme.md ignored: it has no file_type
ip/top.core:30:1 hint[import/ignored]: provider ignored: gohard import fetches nothing, so it reads the files from the .core file's folder
ip/top.core:17:9 warning[import/unresolved-dependency]: dependency acme:ip:missing:1.0 matches no .core file under the project root or on the command line; gohard import fetches nothing, so its files are not imported
`
	checkAll(t, ParseCore("ip/top.core", []byte(src), files), want)
}

func TestParseCoreSyntax(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", `c.core:1:1 error[import/syntax]: not a valid FuseSoC .core file: the file is empty
`},
		{"name: a:b:c\n", `c.core:1:1 error[import/syntax]: not a valid FuseSoC .core file: it is not a CAPI2 core: it has no CAPI=2: line
`},
		{"CAPI=2:\nfilesets: [\n", `c.core:2:1 error[import/syntax]: not a valid FuseSoC .core file: did not find expected node content
`},
		{"- a\n", `c.core:1:1 error[import/syntax]: not a valid FuseSoC .core file: the file is not a YAML mapping
`},
	} {
		checkAll(t, ParseCore("c.core", []byte(tc.src), memFiles{}),
			tc.want)
	}
}

// TestParseCoreBudget checks that a small .core file cannot make the
// import do unbounded work through YAML aliases (P7): every files: or
// depend: item and every candidate core compared counts each time an
// alias reaches it, and the import stops with import/syntax at
// coreBudget steps, or at coreByteBudget bytes of .core files, with no
// entry. Before the budget, the files case gave 120,000 entries, and a
// 5,000 by 5,000 variant of it (273 KB) ran gohard import out of 5 GB.
func TestParseCoreBudget(t *testing.T) {
	list := func(n int, item string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(item, i)
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	// fanout is a core with the anchor &x on line 3 and sets filesets
	// that each hold set.
	fanout := func(anchor string, sets int, set string) string {
		var b strings.Builder
		b.WriteString("CAPI=2:\nname: a:b:top:1\nx: &x " + anchor +
			"\nfilesets:\n")
		for i := range sets {
			fmt.Fprintf(&b, "  s%d: {%s}\n", i, set)
		}
		return b.String()
	}
	// cores is n candidate cores named a:b:c:version, each followed by
	// pad.
	cores := func(n int, version, pad string) memFiles {
		m := memFiles{files: map[string]string{}}
		for i := range n {
			p := fmt.Sprintf("c%d.core", i)
			m.files[p] = "CAPI=2:\nname: a:b:c:" + version + "\n" + pad
			m.cores = append(m.cores, p)
		}
		return m
	}
	steps := "error[import/syntax]: not a valid FuseSoC .core file: " +
		"the import takes more than " + strconv.Itoa(coreBudget) +
		" steps, counting each use of a YAML alias\n"
	// on3 is the position of item on line 3.
	on3 := func(src, item string) string {
		l := strings.Split(src, "\n")[2]
		return "big.core:3:" + strconv.Itoa(strings.Index(l, item)+1) +
			" "
	}
	files := fanout(list(400, "f%d.vhd"), 300,
		"files: *x, file_type: vhdlSource")
	deps := fanout(list(400, "a:b:c%d"), 300, "depend: *x")
	// One dependency that none of 1,000 cores satisfies, in each of
	// 150 filesets: 150,000 comparisons.
	none := fanout(`[">=a:b:c:2"]`, 150, "depend: *x")
	// A version of 64 KiB costs 1,025 steps per comparison.
	long := fanout(`[">=a:b:c:2"]`, 100, "depend: *x")
	for _, tc := range []struct {
		name, src string
		files     memFiles
		last      string // the last diagnostic
	}{
		{"files", files, memFiles{}, on3(files, "f49.vhd,") + steps},
		{"depend", deps, memFiles{}, on3(deps, "a:b:c298,") + steps},
		{"candidates", none, cores(1000, "1", ""), on3(none, `">=`) +
			steps},
		{
			"long version", long,
			cores(1, "1."+strings.Repeat("0", 1<<16), ""),
			on3(long, `">=`) + steps,
		},
		{
			"bytes", "CAPI=2:\nfilesets: {r: {depend: [a:b:z]}}\n",
			cores(5, "1", "# "+strings.Repeat("x", 1<<20)+"\n"),
			"c3.core:1:1 error[import/syntax]: not a valid FuseSoC " +
				".core file: the .core files hold more than 4 MiB\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ParseCore("big.core", []byte(tc.src), tc.files)
			if len(p.Entries) != 0 || len(p.Diagnostics) == 0 {
				t.Fatalf("%d entries, %d diagnostics", len(p.Entries),
					len(p.Diagnostics))
			}
			last := p.Diagnostics[len(p.Diagnostics)-1:]
			if got := showAll(Project{Diagnostics: last}); got != tc.last {
				t.Errorf("last diagnostic:\n%swant:\n%s", got, tc.last)
			}
		})
	}
}

// TestCoreBudgetMargin checks that the .core budgets leave room for
// real projects: reading every .core file of the pinned open-logic
// takes under 1% of each budget.
func TestCoreBudgetMargin(t *testing.T) {
	var b coreSpent
	n := 0
	err := filepath.WalkDir("../../testdata/open-logic", func(p string,
		d fs.DirEntry, err error,
	) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".core" {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if c := readCore(p, p, src, &b); c.fatal || !c.named {
			t.Errorf("%s does not read as a named core:\n%s", p,
				showAll(Project{Diagnostics: c.diags}))
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("no .core file under testdata/open-logic (%v): run "+
			"git submodule update --init testdata/open-logic", err)
	}
	if b.over || b.steps*100 > coreBudget ||
		b.bytes*100 > coreByteBudget {
		t.Errorf("%d cores take %d steps and %d bytes", n, b.steps,
			b.bytes)
	}
}

func TestCoreVersions(t *testing.T) {
	for _, tc := range []struct {
		dep, have string
		want      bool
	}{
		{"a:b:c", "a:b:c:1.0", true},
		{"a:b:c:1.0", "a:b:c:1.0", true},
		{"a:b:c:1.0", "a:b:c:1.0.1", false},
		{"=a:b:c:1.0", "a:b:c:1.0", true},
		{">=a:b:c:1.9", "a:b:c:1.10", true},
		{">a:b:c:1.10", "a:b:c:1.10", false},
		{"<a:b:c:2", "a:b:c:1.99", true},
		{"<=a:b:c:1.2", "a:b:c:1.2", true},
		{"^a:b:c:1.2.0", "a:b:c:1.9", true},
		{"^a:b:c:1.2.0", "a:b:c:2.0.0", false},
		{"^a:b:c:1.2.0", "a:b:c:1.1.9", false},
		{"~a:b:c:1.2.0", "a:b:c:1.2.7", true},
		{"~a:b:c:1.2.0", "a:b:c:1.3.0", false},
		{"a:b:c:1.0", "a:b:d:1.0", false},
		{"a:b:c:1.0", "x:b:c:1.0", false},
		{"a:b:c:1.0", "a:x:c:1.0", false},
		{"a:b:c:4.7.0", "a:b:c:4.7.0-r1", false},
		{">=a:b:c:4.7.0", "a:b:c:4.7.0-r1", true},
	} {
		op, want, ok := parseVLNV(tc.dep)
		_, have, ok2 := parseVLNV(tc.have)
		if !ok || !ok2 {
			t.Fatalf("parseVLNV(%q, %q) failed", tc.dep, tc.have)
		}
		if got := (coreDep{op: op, want: want}).matches(
			have); got != tc.want {
			t.Errorf("%s matches %s = %v, want %v", tc.dep, tc.have, got,
				tc.want)
		}
	}
	for _, bad := range []string{"a:b", "a:b::1", "a:b:c:d:e", ""} {
		if _, _, ok := parseVLNV(bad); ok {
			t.Errorf("parseVLNV(%q) accepted", bad)
		}
	}
}

// TestProProfileDocumented keeps the profile table of docs/libraries.md
// in step with proProfile: one row per variable with its value, and
// the row of ::osvvm::VhdlVersion.
func TestProProfileDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/libraries.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for l := range strings.Lines(string(doc)) {
		if strings.HasPrefix(l, "| `::") {
			rows++
		}
	}
	for _, k := range slices.Sorted(maps.Keys(proProfile)) {
		row := "| `::" + k + "` | `" + proProfile[k] + "` |\n"
		if !strings.Contains(string(doc), row) {
			t.Errorf("docs/libraries.md lacks the row %q", row)
		}
	}
	if !strings.Contains(string(doc), "| `::osvvm::VhdlVersion` |") ||
		rows != len(proProfile)+1 {
		t.Errorf("docs/libraries.md has %d profile rows, want %d", rows,
			len(proProfile)+1)
	}
}
