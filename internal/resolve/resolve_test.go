package resolve

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/sema"
)

var files = map[string]string{
	"pkg.vhd": `library ieee;
use ieee.numeric_std.all;
package defs is
  constant WIDTH : natural := 8;
  function inc(x : natural) return natural;
end package defs;
package body defs is
  function inc(x : natural) return natural is
  begin
    return x + 1;
  end function inc;
end package body defs;
`,
	"top.vhd": `library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use work.defs.all;
entity top is
  port (a, b : in unsigned(WIDTH - 1 downto 0));
end entity top;
architecture rtl of top is
  signal sum : unsigned(WIDTH - 1 downto 0);
  signal n : natural := inc(WIDTH);
begin
  sum <= a + b;
  p: process (a) is
    variable v : natural;
  begin
    v := inc(v);
  end process;
end architecture;
`,
}

func result(t *testing.T) *sema.Result {
	t.Helper()
	var srcs []sema.Source
	for _, p := range []string{"pkg.vhd", "top.vhd"} {
		srcs = append(srcs, sema.Source{Path: p, Text: []byte(files[p])})
	}
	r := sema.Analyze(sema.Input{Files: srcs, Config: config.Defaults()})
	if len(r.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", r.Diagnostics)
	}
	return r
}

// off returns the offset of the nth occurrence of s in path.
func off(t *testing.T, path, s string, nth int) int {
	t.Helper()
	text := files[path]
	from := 0
	for {
		i := strings.Index(text[from:], s)
		if i < 0 {
			t.Fatalf("%s: no %q #%d", path, s, nth)
		}
		if nth == 0 {
			return from + i
		}
		nth--
		from += i + 1
	}
}

func locs(ls []Location) string {
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s:%d", l.Path, l.Span.Start))
	}
	return strings.Join(out, " ")
}

func TestDefinition(t *testing.T) {
	r := result(t)
	got := locs(Definition(r, "top.vhd", off(t, "top.vhd", "WIDTH", 0)))
	if want := fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "WIDTH",
		0)); got != want {
		t.Errorf("WIDTH: %s, want %s", got, want)
	}
	// The operator lands in the bundled numeric_std.
	d := Definition(r, "top.vhd", off(t, "top.vhd", "+", 0))
	if len(d) != 1 || !d[0].Bundled ||
		d[0].Path != "ieee/numeric_std.vhdl" {
		t.Errorf("a + b: %+v", d)
	}
	// Definition on a declaration is the declaration itself.
	got = locs(Definition(r, "pkg.vhd", off(t, "pkg.vhd", "WIDTH", 0)))
	if want := fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "WIDTH",
		0)); got != want {
		t.Errorf("on the declaration: %s", got)
	}
}

func TestReferences(t *testing.T) {
	r := result(t)
	got := locs(References(r, "pkg.vhd", off(t, "pkg.vhd", "inc", 0),
		true))
	want := []string{
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 0)),
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 1)),
		fmt.Sprintf("pkg.vhd:%d", off(t, "pkg.vhd", "inc", 2)),
		fmt.Sprintf("top.vhd:%d", off(t, "top.vhd", "inc", 0)),
		fmt.Sprintf("top.vhd:%d", off(t, "top.vhd", "inc", 1)),
	}
	if got != strings.Join(want, " ") {
		t.Errorf("references of inc:\n%s\nwant\n%s", got,
			strings.Join(want, " "))
	}
	// The same set from any occurrence, without the declarations.
	from := locs(References(r, "top.vhd", off(t, "top.vhd", "inc", 1),
		false))
	if from != strings.Join(want[1:], " ") {
		t.Errorf("from a use: %s", from)
	}
}

func TestHover(t *testing.T) {
	r := result(t)
	h, ok := HoverAt(r, "top.vhd", off(t, "top.vhd", "+", 0))
	if !ok || !strings.Contains(h.Markdown, `function "+" `+
		`[UNRESOLVED_UNSIGNED, UNRESOLVED_UNSIGNED return `+
		`UNRESOLVED_UNSIGNED] from ieee.numeric_std`) {
		t.Errorf("hover +: %q", h.Markdown)
	}
	h, _ = HoverAt(r, "top.vhd", off(t, "top.vhd", "sum", 1))
	if !strings.Contains(h.Markdown, "signal sum : anonymous") &&
		!strings.Contains(h.Markdown, "signal sum :") {
		t.Errorf("hover sum: %q", h.Markdown)
	}
}

func TestRename(t *testing.T) {
	r := result(t)
	if _, ok := PrepareRename(r, "top.vhd", off(t, "top.vhd", "+",
		0)); ok {
		t.Error("a bundled operator is not renamable")
	}
	sp, ok := PrepareRename(r, "top.vhd", off(t, "top.vhd", "inc", 0))
	if !ok || sp.Len() != 3 {
		t.Fatalf("prepare inc: %v %v", sp, ok)
	}
	edits, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"step")
	if err != nil || len(edits) != 5 {
		t.Fatalf("rename: %v %v", edits, err)
	}
	// Applying the edits renames every occurrence, end name included.
	out := apply(files, edits)
	if strings.Contains(strings.ToLower(out["pkg.vhd"]+out["top.vhd"]),
		"inc") || !strings.Contains(out["pkg.vhd"], "end function step") {
		t.Errorf("renamed:\n%s%s", out["pkg.vhd"], out["top.vhd"])
	}
	if _, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"signal"); err != ErrBadName {
		t.Errorf("reserved word: %v", err)
	}
	if _, err := Rename(r, "top.vhd", off(t, "top.vhd", "inc", 0),
		"two words"); err != ErrBadName {
		t.Errorf("two words: %v", err)
	}
}

func apply(src map[string]string, edits []Edit) map[string]string {
	out := map[string]string{}
	for p, s := range src {
		out[p] = s
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		s := out[e.Path]
		out[e.Path] = s[:e.Span.Start] + e.NewText + s[e.Span.End:]
	}
	return out
}

func TestComplete(t *testing.T) {
	r := result(t)
	at := off(t, "top.vhd", "v := inc", 0)
	var labels []string
	for _, it := range Complete(r, "top.vhd", at) {
		labels = append(labels, strings.ToLower(it.Label))
	}
	for _, want := range []string{"v", "sum", "n", "a", "b", "width",
		"inc", "to_unsigned", "std_logic", "integer", "top"} {
		if !slices.Contains(labels, want) {
			t.Errorf("completion lacks %s", want)
		}
	}
	// Before the variable's declaration it is not visible.
	before := off(t, "top.vhd", "p: process", 0)
	for _, it := range Complete(r, "top.vhd", before) {
		if strings.ToLower(it.Label) == "v" {
			t.Error("v is visible before its process")
		}
	}
	if !slices.IsSortedFunc(Complete(r, "top.vhd", at), func(a,
		b Item) int {
		return strings.Compare(strings.ToLower(a.Label),
			strings.ToLower(b.Label))
	}) {
		t.Error("completion is not sorted")
	}
}

func TestSymbols(t *testing.T) {
	r := result(t)
	ds := DocumentSymbols(r, "top.vhd")
	if len(ds) != 2 || ds[0].Name != "top" || ds[1].Name != "rtl" {
		t.Fatalf("document symbols: %+v", ds)
	}
	var names []string
	for _, c := range ds[1].Children {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "p,sum,n" &&
		strings.Join(names, ",") != "sum,n,p" {
		t.Errorf("architecture children: %v", names)
	}
	ws := WorkspaceSymbols(r, "wid")
	if len(ws) != 1 || ws[0].Name != "WIDTH" {
		t.Errorf("workspace symbols: %+v", ws)
	}
	all := WorkspaceSymbols(r, "")
	if len(all) < 5 {
		t.Errorf("workspace symbols: %d", len(all))
	}
}
