package importer

import (
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/span"
)

// fuzzFiles serves the fuzz input as every other file a project file
// names, so a .pro includes itself and a .core depends on itself, and
// records the paths read.
type fuzzFiles struct {
	src  []byte
	read []string
}

func (f *fuzzFiles) Read(p string) ([]byte, error) {
	f.read = append(f.read, p)
	return f.src, nil
}

func (f *fuzzFiles) IsDir(p string) bool { return strings.Contains(p, "d") }

func (f *fuzzFiles) Cores() []string {
	return []string{"dep/a.core", "../b.core"}
}

// FuzzImport checks the #2 and #64 invariants for every format on any
// bytes: Parse never panics, ends within its budgets and is
// deterministic, reports only codes from the diag table, at real
// positions of the input or of a file it read; every entry is a VHDL
// file in a library named by a VHDL identifier, at a real position;
// and the gohard.yaml that Plan and Render make from the entries loads
// with no diagnostic at all, so gohard import never writes a
// configuration that fails or warns. Go replays the seeds (and any
// crash saved under testdata/fuzz/FuzzImport) in plain go test.
func FuzzImport(f *testing.F) {
	for _, s := range []string{
		"", "vcom a.vhd", "vcom -work yes \"a'b.vhd\"",
		"vcom {a\nb.vhd}", "vcom \"x\x85.vhd\"", "vcom -2008 {-a.vhd}",
		"set_global_assignment -name VHDL_FILE a.vhd -library null",
		"set_global_assignment -name VHDL_INPUT_VERSION VHDL_2019\n" +
			"set_global_assignment -name VHDL_FILE a.vhdl",
		xprSrc, "<Project><FileSets><FileSet Type=\"DesignSrcs\">" +
			"<File Path=\"$PPRDIR/a.vhd\"><FileInfo SFType=\"VHDL\">" +
			"<Attr Name=\"Library\" Val=\"On\"/></FileInfo></File>",
		"vcom [a", "vcom \"a", "\\", "#\\", "vcom a.vhd\\\r\nb.vhd",
		"library l\nanalyze a.vhd\ninclude self.pro\ninclude d\n" +
			"if {$::osvvm::ToolName eq \"GoHard\"} {analyze b.vhd} " +
			"else {analyze c.vhd}",
		"include x/p.pro\ninclude x/p.pro\nanalyze [FindSettingsPkgBody P]",
		"if {[string compare a b] == -1 && (1 || $x)} {RunTest t.vhd}" +
			" elseif {$::poc::vendorName in {A GENERIC}} {SetVHDLVersion 93}",
		"CAPI=2:\nname: a:b:c:1\nfilesets:\n  r:\n" +
			"    files: [a.vhd, b.vhd: {logical_name: x}]\n" +
			"    file_type: vhdlSource-2008\n    depend: [a:b:c, ^a:b:d:1]\n",
		"CAPI=2:\nfilesets: {r: {files: [&x a.vhd, *x], " +
			"file_type: vhdlSource}}\nprovider: {}",
		// Aliases that fan out: each use counts against the budget.
		"CAPI=2:\nname: a:b:c:1\nx: &x [a.vhd, b.vhd]\n" +
			"d: &d [a:b:c, \">=a:b:e:1\"]\nfilesets: {r: {files: *x, " +
			"depend: *d, file_type: vhdlSource}, s: &s {files: *x, " +
			"depend: *d}, t: *s}\n",
	} {
		for i := range Formats {
			f.Add(uint8(i), []byte(s))
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, src []byte) {
		ft := Formats[int(which)%len(Formats)]
		name := "dir/p" + ft.Ext
		read := &fuzzFiles{src: src}
		p1 := ft.Parse(name, src, read)
		p2 := ft.Parse(name, src, &fuzzFiles{src: src})
		if showAll(p1) != showAll(p2) {
			t.Fatalf("%s: Parse is not deterministic", ft.Ext)
		}
		// Every file holds src, so every position must be real in it.
		paths := []string{name}
		for _, p := range read.read {
			paths = append(paths, join(path.Dir(name), p))
		}
		idx := span.NewIndex(src)
		isReal := func(line, col int) bool {
			off, ok := idx.OffsetOf(line, col)
			if !ok || line < 1 || line > idx.LineCount() {
				return false
			}
			l2, c2 := idx.LineCol(off)
			return l2 == line && c2 == col
		}
		for _, d := range p1.Diagnostics {
			if _, ok := d.Code.Info(); !ok ||
				d.Code == diag.InternalUnknownCode {
				t.Fatalf("unknown code %s", d.Code)
			}
			if !slices.Contains(paths, d.Path) || !isReal(d.Line, d.Col) {
				t.Fatalf("%s:%d:%d is not a position of the input: %s",
					d.Path, d.Line, d.Col, d.Message)
			}
		}
		for _, e := range p1.Entries {
			if !isVHDL(e.Path) || !config.IsIdentifier(e.Library) ||
				!slices.Contains(paths, e.File) || !isReal(e.Line, e.Col) {
				t.Fatalf("bad entry %+v", e)
			}
			if !slices.Contains([]string{"", "1993", "2002", "2008",
				"2019"}, e.Standard) {
				t.Fatalf("bad standard %+v", e)
			}
		}
		c, ds := Plan(name, p1.Entries)
		for _, d := range ds {
			if d.Class() == diag.Tool {
				t.Fatalf("Plan made a tool error: %s", d.Message)
			}
		}
		text := Render(name, c)
		got, ds := config.Parse(config.FileName, text)
		if len(ds) > 0 {
			t.Fatalf("written gohard.yaml has %s: %s\n%s", ds[0].Code,
				ds[0].Message, text)
		}
		var files []string
		for _, l := range got.Libraries {
			for _, f := range l.Folders {
				files = append(files, l.Name+":"+f.Path)
			}
		}
		var want []string
		for _, l := range c.Libraries {
			for _, f := range l.Files {
				want = append(want, l.Name+":"+f)
			}
		}
		if !slices.Equal(files, want) {
			t.Fatalf("gohard.yaml reads back %q, want %q", files, want)
		}
		if std := c.Standard; std != "" && got.Standard != std {
			t.Fatalf("standard %q, want %q", got.Standard, std)
		}
	})
}
