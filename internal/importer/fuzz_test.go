package importer

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/span"
)

// FuzzImport checks the #2 invariants for every format on any bytes:
// Parse never panics and is deterministic, reports only codes from the
// diag table, at real positions of the input; every entry is a VHDL
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
	} {
		for i := range Formats {
			f.Add(uint8(i), []byte(s))
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, src []byte) {
		ft := Formats[int(which)%len(Formats)]
		name := "dir/p" + ft.Ext
		p1 := ft.Parse(name, src)
		p2 := ft.Parse(name, src)
		if show(p1) != show(p2) {
			t.Fatalf("%s: Parse is not deterministic", ft.Ext)
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
			if d.Path != name || !isReal(d.Line, d.Col) {
				t.Fatalf("%s:%d:%d is not a position of the input: %s",
					d.Path, d.Line, d.Col, d.Message)
			}
		}
		for _, e := range p1.Entries {
			if !isVHDL(e.Path) || !config.IsIdentifier(e.Library) ||
				!isReal(e.Line, e.Col) {
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
