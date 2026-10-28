package ide

import (
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/format"
	"github.com/connerohnesorge/gohard/v2/internal/gen/vhdlgen"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// genProjects are the projects the generated code actions are checked
// on (#57): every corpus design without its testbench files and the
// vhdlgen designs of the first seeds under each standard, each of
// their files formatted first, so a generated edit must keep it
// formatted.
func genProjects(t *testing.T) map[string]project {
	t.Helper()
	out := map[string]project{}
	for name, p := range corpus(t) {
		// Without its own testbenches, whose file names the action
		// would take.
		maps.DeleteFunc(p, func(k string, _ []byte) bool {
			return strings.HasSuffix(k, "_tb.vhd")
		})
		out["corpus/"+name] = p
	}
	seeds := uint64(64)
	if testing.Short() {
		seeds = 8
	}
	for seed := range seeds {
		std := vhdlgen.Standards[int(seed)%len(vhdlgen.Standards)]
		d := vhdlgen.Generate(seed, nil, vhdlgen.Options{Standard: std})
		cfg := "standard: \"" + d.Standard + "\"\n"
		p := project{"gohard.yaml": []byte(cfg)}
		c := config.Defaults()
		c.Standard = d.Standard
		for _, f := range d.Files {
			p[f.Path] = formatted(t, c, f.Text)
		}
		out["vhdlgen/"+strconv.FormatUint(seed, 10)] = p
	}
	return out
}

// formatted returns text as `gohard fmt` writes it under c.
func formatted(t testing.TB, c config.Config, text []byte) []byte {
	t.Helper()
	res := parser.Parse(text, parser.FromConfig(c, "test"))
	if !format.Parses(res) {
		return text
	}
	return format.Format(res, format.FromConfig(c)).Text
}

// fmtClean reports whether `gohard fmt --check` accepts text.
func fmtClean(c config.Config, text []byte) bool {
	res := parser.Parse(text, parser.FromConfig(c, "test"))
	return format.Parses(res) && !format.Format(res,
		format.FromConfig(c)).Changed()
}

// diagSet returns every diagnostic of an as "path: code message".
func diagSet(an *Analysis) []string {
	var out []string
	for _, fd := range an.Diagnostics {
		for _, d := range fd.Diagnostics {
			out = append(out, d.Path+": "+string(d.Code)+" "+d.Message)
		}
	}
	slices.Sort(out)
	return out
}

func entities(an *Analysis) []*sema.Unit {
	var out []*sema.Unit
	for _, u := range an.Result.AllUnits() {
		if u.Kind == sema.UnitEntity && u.File != nil &&
			!u.File.Bundled && !u.Duplicate {
			out = append(out, u)
		}
	}
	return out
}

func actionOf(as []server.CodeAction, title string,
) (server.CodeAction, bool) {
	for _, a := range as {
		if a.Title == title {
			return a, true
		}
	}
	return server.CodeAction{}, false
}

// TestGeneratedTestbench is the property of Create testbench (#57) on
// every entity with ports of the corpus and of generated designs: the
// action is offered (the entities that cannot get one are listed and
// must stay few), the testbench is formatted, adds no diagnostic to the
// project, and simulates to std.env.finish (stopped) under VHDL-2008
// and 2019, to the end of its events (quiet) under 1993 and 2002, never
// over a budget; only an assertion of the design itself, on inputs
// the testbench leaves undriven, may halt it first. Three round trips check the other actions on it:
// Instantiate entity rewrites `<name>;` back into the testbench's
// instance, Complete port map restores the instance from its first
// association, and Declare component lets the instance name the entity
// as a component without a new diagnostic.
func TestGeneratedTestbench(t *testing.T) {
	offered, skipped, ran := 0, 0, 0
	for _, name := range slices.Sorted(maps.Keys(genProjects(t))) {
		p := genProjects(t)[name]
		an := analyze(t, snapshotOf(t, p, nil))
		cfg := Config(an.Snap)
		base := diagSet(an)
		for _, e := range entities(an) {
			if _, ps := formals(e.Region); len(ps) == 0 {
				continue
			}
			label := name + "/" + e.Decl.Text
			tbName := e.Decl.Text + "_tb"
			title := "Create testbench " + tbName + ".vhd"
			as := an.Refactors(uri(e.File.Path), Span(e.Decl.Span),
				Generate{ToolVersion: "test", Create: true})
			a, ok := actionOf(as, title)
			if !ok {
				skipped++
				t.Logf("%s: no testbench", label)
				continue
			}
			if no := an.Refactors(uri(e.File.Path), Span(e.Decl.Span),
				Generate{ToolVersion: "test"}); len(no) > 0 {
				if _, ok := actionOf(no, title); ok {
					t.Fatalf("%s: offered without create", label)
				}
			}
			offered++
			rel := path.Join(path.Dir(e.File.Path), tbName+".vhd")
			fe := a.Edits[0]
			if len(a.Edits) != 1 || !fe.Create || fe.URI != uri(rel) ||
				a.Kind != server.ActionSource {
				t.Fatalf("%s: action %+v", label, a)
			}
			tb := []byte(fe.Edits[0].NewText)
			if !fmtClean(cfg, tb) {
				t.Fatalf("%s: testbench not formatted:\n%s", label, tb)
			}
			q := maps.Clone(p)
			q[rel] = tb
			an2 := analyze(t, snapshotOf(t, q, nil))
			if got := diagSet(an2); !slices.Equal(got, base) {
				t.Fatalf("%s: diagnostics\n%s\nwant\n%s\ntestbench:\n%s",
					label, strings.Join(got, "\n"),
					strings.Join(base, "\n"), tb)
			}
			if !hasErrors(an2) && simulates(t, label, an2, tbName) {
				ran++
			}
			roundTrips(t, label, q, rel, e.Decl.Text, base)
		}
	}
	t.Logf("%d testbenches (%d simulated), %d entities without one",
		offered, ran, skipped)
	if ran == 0 && simrun.DefaultCode != nil {
		t.Fatal("no testbench simulated")
	}
	if offered == 0 || skipped*4 > offered {
		t.Fatalf("%d testbenches, %d entities without one", offered,
			skipped)
	}
}

// simulates runs the testbench tb of an as `gohard check --sim` does
// (simrun.Run) and checks how it ends; false when there is no
// interpreter to run it.
func simulates(t *testing.T, label string, an *Analysis, tb string,
) bool {
	t.Helper()
	if simrun.DefaultCode == nil {
		return false
	}
	cfg := Config(an.Snap)
	out := simrun.Run(simrun.Options{Result: an.Result,
		Sim: cfg.Simulation, Testbench: tb})
	want := "stopped"
	if an.Result.Standard < token.Std2008 {
		want = "quiet"
	}
	asserted := false
	for _, d := range out.Diagnostics {
		switch {
		case d.Code == diag.SimBudgetExceeded:
			t.Fatalf("%s: %s", label, d.Message)
		case d.Code == diag.SimAssertError &&
			!strings.HasSuffix(d.Path, "_tb.vhd"):
			asserted = true
		}
	}
	if asserted && out.Status.String() == "halted" {
		return true // the design's own assertion on undriven inputs
	}
	if out.Status.String() != want {
		t.Fatalf("%s: the testbench ended %s, want %s: %v", label,
			out.Status, want, out.Diagnostics)
	}
	return true
}

// roundTrips checks the rewrites on the testbench rel of q, whose
// instance u_<name> maps every formal to the object of its name.
func roundTrips(t *testing.T, label string, q project, rel, name string,
	base []string,
) {
	t.Helper()
	tb := q[rel]
	cfg := Config(snapshotOf(t, q, nil))
	res := parser.Parse(tb, parser.FromConfig(cfg, "test"))
	var inst cst.ComponentInstantiationStmt
	for n := range res.Tree().Descendants() {
		if c, ok := cst.As[cst.ComponentInstantiationStmt](n); ok {
			inst = c
			break
		}
	}
	if inst.IsZero() {
		t.Fatalf("%s: no instance", label)
	}
	sp := inst.Node.Span()
	act := func(text []byte, off int, title string) []byte {
		t.Helper()
		r := maps.Clone(q)
		r[rel] = text
		an := analyze(t, snapshotOf(t, r, nil))
		as := an.Refactors(uri(rel), server.Span{Start: off, End: off},
			Generate{ToolVersion: "test"})
		a, ok := actionOf(as, title)
		if !ok {
			t.Fatalf("%s: no %q in %+v of\n%s", label, title, as, text)
		}
		return apply(text, a.Edits[0].Edits)
	}

	// The instance is complete: no rewrite is offered on it.
	if as := analyze(t, snapshotOf(t, q, nil)).Refactors(uri(rel),
		server.Span{Start: sp.Start, End: sp.Start},
		Generate{ToolVersion: "test", Create: true}); len(as) > 0 {
		t.Fatalf("%s: %+v offered on the testbench's instance", label, as)
	}

	// Instantiate entity: `<name>;` becomes the instance again.
	bare := slices.Concat(tb[:sp.Start], []byte(name+";"), tb[sp.End:])
	if got := act(bare, sp.Start, "Instantiate entity work."+name); string(
		got,
	) != string(
		tb,
	) {
		t.Fatalf("%s: instantiate gave\n%s\nwant\n%s", label, got, tb)
	}

	// Complete port map: the first association is enough.
	var items []cst.AssociationElement
	for it := range inst.PortMap().List().Items() {
		items = append(items, it)
	}
	if len(items) > 1 {
		cut := slices.Concat(tb[:items[0].Node.Span().End],
			tb[items[len(items)-1].Node.Span().End:])
		if got := act(cut, sp.Start, "Complete port map"); string(
			got,
		) != string(
			tb,
		) {
			t.Fatalf("%s: complete gave\n%s\nwant\n%s", label, got, tb)
		}
	}

	// Declare component: the instance names the component.
	iu := inst.Unit().Node.Span()
	comp := slices.Concat(tb[:iu.Start], []byte(name), tb[iu.End:])
	got := act(comp, sp.Start, "Declare component "+name)
	if !fmtClean(cfg, got) {
		t.Fatalf("%s: declare gave unformatted\n%s", label, got)
	}
	r := maps.Clone(q)
	r[rel] = got
	if d := diagSet(analyze(t, snapshotOf(t, r, nil))); !slices.Equal(d,
		base) {
		t.Fatalf("%s: declare component gave\n%s\n%s", label,
			strings.Join(d, "\n"), got)
	}
}

// TestRefactorsOffered: where each generated code action is offered
// and where it is not (#57).
func TestRefactorsOffered(t *testing.T) {
	ent := func(name string) string {
		return "library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
			"entity " + name + " is\n  port (\n    clk : in std_logic;\n" +
			"    q : out std_logic\n  );\nend entity " + name + ";\n"
	}
	top := "library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
		"entity top is\nend entity top;\n\n" +
		"architecture rtl of top is\n  signal clk : std_logic;\n" +
		"  procedure p is\n  begin\n  end procedure p;\n" +
		"  component other is\n  end component other;\nbegin\n" +
		"  counter;\n" +
		"  u_counter : entity work.counter\n    port map (clk => clk);\n" +
		"  lbl : counter;\n" +
		"  p;\n" +
		"  u1 : other;\n" +
		"  fifo;\n" +
		"  ip.fifo;\n" +
		"  nothing;\n" +
		"  top;\n" +
		"end architecture rtl;\n"
	p := project{
		"gohard.yaml": []byte("libraries:\n  ip: [ip]\n" +
			"  solo: [solo/one.vhd]\n"),
		"counter.vhd":  []byte(ent("counter")),
		"taken.vhd":    []byte(ent("taken")),
		"taken_tb.vhd": []byte("entity taken_tb is\nend entity taken_tb;\n"),
		"ip/fifo.vhd":  []byte(ent("fifo")),
		"solo/one.vhd": []byte(ent("one")),
		"top.vhd":      []byte(top),
	}
	an := analyze(t, snapshotOf(t, p, nil))
	titles := func(rel string, off int, create bool) []string {
		var out []string
		for _, a := range an.Refactors(uri(rel),
			server.Span{Start: off, End: off},
			Generate{ToolVersion: "test", Create: create}) {
			out = append(out, a.Title)
		}
		return out
	}
	for _, c := range []struct {
		at   string
		want []string
	}{
		{"  counter;", []string{"Instantiate entity work.counter"}},
		{"  u_counter :", []string{"Complete port map"}},
		{"  lbl :", []string{"Instantiate entity work.counter",
			"Declare component counter"}},
		{"  p;", nil},   // a procedure call
		{"  u1 :", nil}, // a declared component
		{"  fifo;", []string{"Instantiate entity ip.fifo"}},
		{"  ip.fifo;", []string{"Instantiate entity ip.fifo"}},
		{"  nothing;", nil},
		{"  top;", nil}, // the architecture's own entity
	} {
		off := strings.Index(top, c.at) + 2
		if got := titles("top.vhd", off, true); !slices.Equal(got,
			c.want) {
			t.Errorf("%q: %q, want %q", c.at, got, c.want)
		}
	}
	// The label u_counter is taken, so the new instance is u_counter_2.
	off := strings.Index(top, "  counter;") + 2
	a := an.Refactors(uri("top.vhd"), server.Span{Start: off, End: off},
		Generate{ToolVersion: "test"})
	if len(a) != 1 || !strings.Contains(a[0].Edits[0].Edits[0].NewText,
		"u_counter_2 : entity work.counter") {
		t.Errorf("label: %+v", a)
	}
	// Create testbench: in an entity with ports whose <name>_tb.vhd
	// would be analyzed into its library and is not taken.
	for _, c := range []struct {
		rel, want string
	}{
		{"counter.vhd", "Create testbench counter_tb.vhd"},
		{"ip/fifo.vhd", "Create testbench fifo_tb.vhd"},
		{"taken.vhd", ""},    // taken_tb.vhd exists
		{"solo/one.vhd", ""}, // one_tb.vhd would be in work
		{"top.vhd", ""},      // no ports
		{"taken_tb.vhd", ""}, // a testbench
	} {
		off := strings.Index(string(p[c.rel]), "entity") + 7
		var want []string
		if c.want != "" {
			want = []string{c.want}
		}
		if got := titles(c.rel, off, true); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", c.rel, got, want)
		}
		if got := titles(c.rel, off, false); len(got) > 0 {
			t.Errorf("%s without create: %q", c.rel, got)
		}
	}
}

// TestAnalyzed: the folders whose new files `gohard check` analyzes.
func TestAnalyzed(t *testing.T) {
	cfg := config.Defaults()
	cfg.Libraries = []config.Library{
		{Name: "ext", Folders: []config.Folder{{Path: "../ext"}}},
		{Name: "one", Folders: []config.Folder{{Path: "../one/a.vhd"}}},
		{Name: "dot", Folders: []config.Folder{{Path: ".dot"}}},
	}
	for p, want := range map[string]bool{
		"a_tb.vhd":           true,
		"rtl/a_tb.vhd":       true,
		".hidden/a_tb.vhd":   false,
		"rtl/.x/a_tb.vhd":    false,
		"../ext/a_tb.vhd":    true,
		"../ext/s/a_tb.vhd":  true,
		"../ext/.s/a_tb.vhd": false,
		"../one/a_tb.vhd":    false,
		"../other/a_tb.vhd":  false,
		".dot/a_tb.vhd":      false, // the walk skips it, listed or not
	} {
		if got := analyzed(cfg, p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
}

// genAction returns the edited text of the action title at the first
// occurrence of at in the file rel of p, and whether it is offered.
func genAction(t *testing.T, p project, rel, at, title string,
) ([]byte, bool) {
	t.Helper()
	off := strings.Index(string(p[rel]), at)
	if off < 0 {
		t.Fatalf("%q not in %s", at, rel)
	}
	off += len(at) - len(strings.TrimLeft(at, " "))
	an := analyze(t, snapshotOf(t, p, nil))
	as := an.Refactors(uri(rel), server.Span{Start: off, End: off},
		Generate{ToolVersion: "test"})
	a, ok := actionOf(as, title)
	if !ok {
		return nil, false
	}
	if len(a.Edits) != 1 || a.Edits[0].URI != uri(rel) {
		t.Fatalf("%s: edits %+v", title, a.Edits)
	}
	return apply(p[rel], a.Edits[0].Edits), true
}

// cleanResult checks that the file rel of p is formatted and that the
// project has no error.
func cleanResult(t *testing.T, p project, rel string) {
	t.Helper()
	cfg := Config(snapshotOf(t, p, nil))
	if !fmtClean(cfg, p[rel]) {
		t.Fatalf("%s not formatted:\n%s", rel, p[rel])
	}
	if an := analyze(t, snapshotOf(t, p, nil)); hasErrors(an) {
		t.Fatalf("errors %q in\n%s", diagSet(an), p[rel])
	}
}

// genCounter is an entity for the rewrites' tests.
const genCounter = "library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
	"entity counter is\n  port (\n    clk : in std_logic;\n" +
	"    q   : out std_logic\n  );\nend entity counter;\n"

// TestInstantiateKeepsNextStatement: without a `;` after the name, the
// parser folds the next statement into the name's statement as an
// error; Instantiate entity replaces the label, the name and its `;`
// only, so it gives what it gives with the `;` and the next statement
// stays (#57). A statement with more than the name on its line is not
// rewritten.
func TestInstantiateKeepsNextStatement(t *testing.T) {
	top := func(stmts string) []byte {
		return []byte("library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
			"entity top is\n  port (\n    clk : in std_logic\n  );\n" +
			"end entity top;\n\narchitecture rtl of top is\n" +
			"  signal s : std_logic;\nbegin\n" + stmts +
			"end architecture rtl;\n")
	}
	const title = "Instantiate entity work.counter"
	for _, c := range []struct{ name, next string }{
		{"  counter", "  s <= clk;\n"},
		{"  lbl : counter", "  s <= clk;\n"},
		{"  work.counter", "  s <= clk;\n"},
		{"  counter -- here", "  s <= clk;\n"},
		{"  counter", "  tick : process (clk) is\n  begin\n" +
			"    s <= clk;\n  end process tick;\n"},
		{"  counter", "  u2 : entity work.counter\n" +
			"    port map (\n      clk => clk,\n      q   => s\n    );\n"},
	} {
		p := project{"counter.vhd": []byte(genCounter),
			"top.vhd": top(c.name + "\n" + c.next)}
		got, ok := genAction(t, p, "top.vhd", c.name, title)
		if !ok {
			t.Fatalf("%q: not offered", c.name)
		}
		semi, _, _ := strings.Cut(c.name, " --")
		p["top.vhd"] = top(semi + ";\n" + c.next)
		want, ok := genAction(t, p, "top.vhd", semi, title)
		if !ok {
			t.Fatalf("%q;: not offered", c.name)
		}
		if semi != c.name {
			i := strings.LastIndex(string(want), ");\n") + 2
			want = slices.Concat(want[:i], []byte(" -- here"), want[i:])
		}
		if string(got) != string(want) {
			t.Fatalf("%q then %q gave\n%s\nwant\n%s", c.name, c.next,
				got, want)
		}
		p["top.vhd"] = got
		cleanResult(t, p, "top.vhd")
	}
	for _, s := range []string{"  counter s <= clk;\n",
		"  counter foo;\n"} {
		p := project{"counter.vhd": []byte(genCounter), "top.vhd": top(s)}
		if got, ok := genAction(t, p, "top.vhd", "counter", title); ok {
			t.Errorf("%q: offered:\n%s", s, got)
		}
	}
}

// TestGeneratedKeepsLineComments: a declaration or context clause
// added after the last one goes after the comment on its line, so the
// comment stays with what it describes (#57).
func TestGeneratedKeepsLineComments(t *testing.T) {
	const comp = "Declare component counter"
	for _, c := range []struct{ arch, keep string }{
		{"architecture rtl of top is\n" +
			"  signal s : std_logic; -- drives q\nbegin\n",
			"  signal s : std_logic; -- drives q\n\n  component counter is\n"},
		{"architecture rtl of top is -- the top level\nbegin\n",
			"architecture rtl of top is -- the top level\n" +
				"  component counter is\n"},
		{"architecture rtl of top is /* top */\nbegin\n",
			"architecture rtl of top is /* top */\n" +
				"  component counter is\n"},
	} {
		p := project{"counter.vhd": []byte(genCounter),
			"top.vhd": []byte("library ieee;\nuse ieee.std_logic_1164.all;" +
				"\n\nentity top is\n  port (\n    clk : in std_logic\n" +
				"  );\nend entity top;\n\n" + c.arch +
				"  u0 : counter\n    port map (\n      clk => clk\n" +
				"    );\nend architecture rtl;\n")}
		got, ok := genAction(t, p, "top.vhd", "u0 :", comp)
		if !ok {
			t.Fatalf("%q: not offered", c.arch)
		}
		if !strings.Contains(string(got), c.keep) ||
			!strings.Contains(string(got), "  end component counter;\n") {
			t.Fatalf("%q gave\n%s", c.arch, got)
		}
		p["top.vhd"] = got
		cleanResult(t, p, "top.vhd")
	}
	// The library clause Instantiate entity adds follows the comment
	// of the last context clause.
	p := project{
		"gohard.yaml": []byte("libraries:\n  ip: [ip]\n"),
		"ip/fifo.vhd": []byte(strings.ReplaceAll(genCounter, "counter",
			"fifo")),
		"top.vhd": []byte("entity top is\nend entity top;\n\n" +
			"library ieee; -- the standard\n" +
			"use ieee.std_logic_1164.all; -- std_logic\n\n" +
			"architecture rtl of top is\n  signal clk : std_logic;\n" +
			"begin\n  fifo\nend architecture rtl;\n"),
	}
	got, ok := genAction(
		t,
		p,
		"top.vhd",
		"  fifo",
		"Instantiate entity ip.fifo",
	)
	if !ok {
		t.Fatal("Instantiate entity ip.fifo not offered")
	}
	if !strings.Contains(string(got), "use ieee.std_logic_1164.all; "+
		"-- std_logic\nlibrary ip;\n\narchitecture") {
		t.Fatalf("library clause:\n%s", got)
	}
	p["top.vhd"] = got
	cleanResult(t, p, "top.vhd")
}

// TestDeclareComponentContext: the generic and port declarations a
// component declaration copies keep naming what they name in the
// entity: the clauses that only the entity's context has are added,
// and the action is not offered where no clause can do it, or where
// the component would not bind to the entity (#57).
func TestDeclareComponentContext(t *testing.T) {
	types := "package types is\n" +
		"  subtype word_t is bit_vector(7 downto 0);\n" +
		"  constant ZERO : word_t := (others => '0');\n" +
		"end package types;\n"
	ent := func(ctx, ports string) []byte {
		return []byte(ctx + "entity counter is\n  port (\n" + ports +
			"\n  );\nend entity counter;\n")
	}
	top := func(ctx, decls string) []byte {
		return []byte(ctx + "entity top is\nend entity top;\n\n" +
			"architecture rtl of top is\n" + decls + "begin\n" +
			"  u0 : counter;\nend architecture rtl;\n")
	}
	const comp = "Declare component counter"
	const ieee = "library ieee;\nuse ieee.std_logic_1164.all;\n\n"
	for _, c := range []struct {
		name      string
		ctx       string // of counter.vhd
		ports     string
		topCtx    string
		decls     string
		want      string // the context the action adds, "-" for none
		notOffers bool
	}{
		{name: "use all", ctx: "use work.types.all;\n\n",
			ports: "    count : out word_t := ZERO",
			want:  "use work.types.all;\n\narchitecture"},
		{name: "use package", ctx: "use work.types;\n\n",
			ports: "    count : out types.word_t",
			want:  "use work.types;\n\narchitecture"},
		{name: "expanded", ports: "    count : out work.types.word_t",
			want: "-"},
		{name: "library", ctx: "library ieee;\n\n",
			ports: "    q : out ieee.std_logic_1164.std_logic",
			want:  "library ieee;\n\narchitecture"},
		{name: "literal", ctx: ieee,
			ports: "    clk : in ieee.std_logic_1164.std_logic := '1'",
			want: "library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
				"architecture"},
		{name: "string", ctx: ieee,
			ports: "    d : in ieee.std_logic_1164.std_logic_vector(" +
				"1 downto 0) := \"1Z\"",
			want: "library ieee;\nuse ieee.std_logic_1164.all;\n\n" +
				"architecture"},
		{name: "already used", ctx: "use work.types.all;\n\n",
			ports:  "    count : out word_t",
			topCtx: "use work.types.all;\n\n", want: "-"},
		{name: "hidden", ctx: "use work.types.all;\n\n",
			ports:     "    count : out word_t",
			decls:     "  subtype word_t is bit_vector(3 downto 0);\n",
			notOffers: true},
	} {
		p := project{"types.vhd": []byte(types),
			"counter.vhd": ent(c.ctx, c.ports),
			"top.vhd":     top(c.topCtx, c.decls)}
		got, ok := genAction(t, p, "top.vhd", "u0 :", comp)
		if ok == c.notOffers {
			t.Fatalf("%s: offered %v:\n%s", c.name, ok, got)
		}
		if !ok {
			continue
		}
		before, _, _ := strings.Cut(string(got), "entity top is")
		if c.want == "-" && before != c.topCtx ||
			c.want != "-" && !strings.Contains(string(got), c.want) {
			t.Fatalf("%s: context %q in\n%s", c.name, c.want, got)
		}
		p["top.vhd"] = got
		cleanResult(t, p, "top.vhd")
	}
	// An entity of another library: the default binding would not find
	// it, so only Instantiate entity is offered.
	p := project{"gohard.yaml": []byte("libraries:\n  ip: [ip]\n"),
		"ip/counter.vhd": ent("", "    clk : in bit"),
		"top.vhd":        top("", "")}
	if got, ok := genAction(t, p, "top.vhd", "u0 :", comp); ok {
		t.Fatalf("offered for an entity of library ip:\n%s", got)
	}
	if _, ok := genAction(t, p, "top.vhd", "u0 :",
		"Instantiate entity ip.counter"); !ok {
		t.Fatal("Instantiate entity ip.counter not offered")
	}
}
