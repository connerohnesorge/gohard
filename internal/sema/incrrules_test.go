package sema

import (
	"maps"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/parser"
)

// incrRulesDesign has one unit that depends on another in each of the
// ways incr.go's dirtyUnits follows besides Deps: the architecture of
// top evaluates f and dc, which package body p defines, statically
// (static reads); it names architecture rtl of sub, which is no
// dependency and sits in a file of its own (a late reference); w uses work.all (a library rebuilt
// for a dirty unit); and vunit v is bound to sub(rtl), so its job
// writes the PSL expressions of its file (#110).
var incrRulesDesign = map[string]string{
	"a_pkg.vhd": `package p is
  function f return integer;
  constant dc : integer;
end package;
`,
	"b_body.vhd": `package body p is
  constant dc : integer := 4;
  function f return integer is
  begin
    return 1;
  end function;
end package body;
`,
	"c_top.vhd": `use work.p.all;
entity top is
end entity;
architecture a of top is
  constant c : natural := f;
  constant c2 : natural := dc;
  signal clk, x, y : bit;
begin
  u : entity work.sub(rtl) port map (clk, x, y);
end architecture;
`,
	"d_sub.vhd": `entity sub is
  port (clk, a, b : in bit);
end entity;
`,
	"d_sub_rtl.vhd": `architecture rtl of sub is
  signal q : bit;
begin
  q <= a and b;
end architecture;
`,
	"e_all.vhd": `use work.all;
entity w is
end entity;
architecture a of w is
  constant k : integer := 0;
begin
end architecture;
`,
	"f_props.vhd": `vunit v(sub(rtl)) {
  default clock is rising_edge(clk);
  assert always (a -> b);
}
`,
}

// TestIncrementalRules (#53, P4) edits incrRulesDesign so that each
// rule of dirtyUnits decides the result: without the rule the unit
// that depends on the edited one would keep the previous result's
// objects or diagnostics, and the comparison with a full analysis
// fails. A clean unit whose static reads are dirty keeps its objects
// and only redoes its static checks. remade are the units the edit
// must re-make, kept units it cannot change and that must be shared.
func TestIncrementalRules(t *testing.T) {
	type step struct {
		name, path, old, new string
		// want is a diagnostic the full analysis must report ("" for
		// none), gone one it must not.
		want, gone   string
		remade, kept []string
	}
	steps := []step{
		{name: "static read of a function body", path: "b_body.vhd",
			old: "return 1;", new: "return -1;",
			want: "c_top.vhd:5:27: sema/static-error: value -1 is out",
			kept: []string{"work.p", "work.top", "work.top(a)",
				"work.sub(rtl)"}},
		{name: "static read, undone", path: "b_body.vhd",
			old: "return -1;", new: "return 1;",
			gone: "value -1 is out",
			kept: []string{"work.p", "work.top", "work.top(a)"}},
		{name: "static read of a deferred constant", path: "b_body.vhd",
			old: "dc : integer := 4;", new: "dc : integer := -4;",
			want: "c_top.vhd:6:28: sema/static-error: value -4 is out",
			kept: []string{"work.p", "work.top", "work.top(a)"}},
		{name: "late reference to an architecture", path: "d_sub_rtl.vhd",
			old: "  signal q : bit;\n", new: "  signal r : bit;\n" +
				"  signal q : bit;\n",
			remade: []string{"work.top(a)", "work.v"},
			kept:   []string{"work.p", "work.p body", "work.sub"}},
		{name: "a body edit seen by the vunit", path: "d_sub_rtl.vhd",
			old: "q <= a and b;", new: "q <= a or b;",
			remade: []string{"work.v"},
			kept:   []string{"work.p", "work.p body", "work.sub"}},
		{name: "use work.all", path: "b_body.vhd",
			old: "dc : integer := -4;", new: "dc : integer := 4;",
			gone:   "value -4 is out",
			remade: []string{"work.w", "work.w(a)"},
			kept:   []string{"work.sub", "work.v"}},
		{name: "the vunit's own edit", path: "f_props.vhd",
			old: "(a -> b)", new: "(a -> bb)",
			want:   "f_props.vhd:3:23: sema/undefined: bb is not declared",
			remade: []string{"work.v"},
			kept:   []string{"work.sub(rtl)", "work.top(a)"}},
		{name: "a signature edit", path: "a_pkg.vhd",
			old: "  constant dc", new: "  function f2 return integer;\n" +
				"  constant dc",
			want:   "a_pkg.vhd:3:12: sema/error: missing body for function",
			remade: []string{"work.p", "work.p body", "work.top(a)"},
			kept:   []string{"work.sub", "work.v"}},
	}
	cfg := config.Defaults()
	popts := parser.FromConfig(cfg, "")
	for _, withCache := range []bool{false, true} {
		var c Cache
		if withCache {
			c = newCacheEnv(cfg).open()
		}
		texts := map[string][]byte{}
		trees := map[string]*parser.Result{}
		for k, v := range incrRulesDesign {
			texts[k] = []byte(v)
			trees[k] = parser.Parse(texts[k], popts)
		}
		treeText := maps.Clone(texts)
		input := func(prev *Result) Input {
			return Input{Files: snapshot{}.sources(texts, trees, treeText),
				Config: cfg, Cache: c, Prev: prev}
		}
		prev := Analyze(input(nil))
		if prev.Diagnostics != nil {
			t.Fatalf("the design has diagnostics:\n%s", diagText(prev))
		}
		for _, st := range steps {
			where := st.name
			if withCache {
				where += " (cache)"
			}
			text := string(texts[st.path])
			if strings.Count(text, st.old) != 1 {
				t.Fatalf("%s: %q is not once in %s", where, st.old,
					st.path)
			}
			texts[st.path] = []byte(strings.Replace(text, st.old, st.new,
				1))
			trees[st.path] = parser.Parse(texts[st.path], popts)
			treeText[st.path] = texts[st.path]
			before := graphBytes(t, prev)
			incr := Analyze(input(prev))
			full := Analyze(Input{Files: snapshot{}.sources(texts, nil,
				nil), Config: cfg})
			got := diagText(full)
			if st.want != "" && !strings.Contains(got, st.want) {
				t.Fatalf("%s: no %q in\n%s", where, st.want, got)
			}
			if st.gone != "" && strings.Contains(got, st.gone) {
				t.Fatalf("%s: %q in\n%s", where, st.gone, got)
			}
			checkIncremental(t, where, prev, before, incr, full)
			shared := map[string]bool{}
			for i, u := range incr.Units {
				shared[u.Label()] = prev.Units[i] == u
			}
			for _, l := range st.remade {
				if v, ok := shared[l]; !ok || v {
					t.Fatalf("%s: %s is shared or missing (%v)", where, l,
						shared)
				}
			}
			for _, l := range st.kept {
				if !shared[l] {
					t.Fatalf("%s: %s is not shared (%v)", where, l,
						shared)
				}
			}
			prev = incr
		}
	}
}
