package sema

import (
	"strings"
	"testing"
)

const configSpecHead = "entity c1_ent is\nend;\npackage p is\nend;\n" +
	"entity e is\nend;\narchitecture a of e is\n" +
	"  component c1 is\n  end component;\n" +
	"  component c2 is\n  end component;\n"

const configSpecBody = "begin\n  i1: component c1;\n" +
	"  i2: entity work.c1_ent;\n  i3: component c2;\n" +
	"  b: block is\n  begin\n    i4: component c1;\n  end block;\n" +
	"end;\n"

// TestConfigSpecRules: each label of a configuration specification
// names a component instantiation of the same region whose component
// is the specification's (LRM 7.3.1), bound once; the binding names an
// entity (LRM 7.3.2.2) and has an entity aspect (LRM 7.3.2.1).
func TestConfigSpecRules(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"for i1 : c1 use entity work.c1_ent;", ""},
		{"for all : c2 use entity work.c1_ent;", ""},
		{"for others : c1 use open;", ""},
		{"for i1 : c2 use entity work.c1_ent;",
			"instance i1 is a component c1, not c2"},
		{"for i2 : c1 use entity work.c1_ent;",
			"does not apply to the direct instantiation i2"},
		{"for i4 : c1 use entity work.c1_ent;",
			"no component instantiation statement labelled i4"},
		{"for nope : c1 use entity work.c1_ent;",
			"no component instantiation statement labelled nope"},
		{"for i1 : e use entity work.c1_ent;",
			"e is an entity, not a component"},
		{"for i1 : c1 use entity work.p;",
			"work.p is a package, not an entity"},
		{"for i1 : c1;", "must have an entity aspect"},
		{"for i1 : c1 use entity work.c1_ent;\n" +
			"  for i1 : c1 use entity work.c1_ent;",
			"instance i1 is already bound"},
		{"for all : c2 use entity work.c1_ent;\n" +
			"  for all : c2 use entity work.c1_ent;",
			"instance i3 of c2 is already bound"},
		// others binds what no specification names: no conflict.
		{"for others : c1 use entity work.c1_ent;\n" +
			"  for i1 : c1 use entity work.c1_ent;", ""},
	}
	for _, c := range cases {
		src := configSpecHead + "  " + c.spec + "\n" + configSpecBody
		got := diagText(analyze(t, map[string]string{"t.vhd": src}, nil))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%q: unexpected\n%s", c.spec, got)
		case c.want != "" && !strings.Contains(got, c.want):
			t.Errorf("%q: want %q in\n%s", c.spec, c.want, got)
		case c.want != "" && strings.Count(got, "\n") != 1:
			t.Errorf("%q: want one diagnostic:\n%s", c.spec, got)
		}
	}
}

// TestConfigSpecBlock: a specification in a block's declarative part
// sees the block's own instances only.
func TestConfigSpecBlock(t *testing.T) {
	src := configSpecHead + "begin\n  i1: component c1;\n" +
		"  b: block is\n    for i4 : c1 use entity work.c1_ent;\n" +
		"    for i1 : c1 use entity work.c1_ent;\n" +
		"  begin\n    i4: component c1;\n  end block;\nend;\n"
	got := diagText(analyze(t, map[string]string{"t.vhd": src}, nil))
	want := "t.vhd:16:9: sema/error: no component instantiation " +
		"statement labelled i1 in this region (LRM 7.3.1)\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

const configDeclHead = "entity sub is\nend;\narchitecture x of sub is\n" +
	"begin\nend;\nentity top is\nend;\narchitecture a of top is\n" +
	"  component comp is\n  end component;\nbegin\n" +
	"  c1: component comp;\n  d1: entity work.sub;\n" +
	"  p: process is begin wait; end process;\n" +
	"  b: block is\n  begin\n    c2: component comp;\n  end block;\n" +
	"end;\n"

// TestConfigurationRules: block configurations name blocks or generate
// statements of the configured block (LRM 3.4.2), component
// configurations its component instances (LRM 3.4.3), and a nested
// block configuration needs fully bound instances.
func TestConfigurationRules(t *testing.T) {
	cases := []struct{ items, want string }{
		{"for c1 : comp use entity work.sub(x); end for;\n" +
			"    for b\n      for c2 : comp use entity work.sub; end for;\n" +
			"    end for;", ""},
		{"for nope : comp use entity work.sub; end for;",
			"no component instantiation statement labelled nope in " +
				"architecture a (LRM 3.4.3)"},
		{"for c2 : comp use entity work.sub; end for;",
			"no component instantiation statement labelled c2"},
		{"for d1 : comp use entity work.sub; end for;",
			"does not apply to the direct instantiation d1"},
		{"for p\n    end for;",
			"p is not a block or generate statement of architecture a"},
		{"for q\n    end for;",
			"no block or generate statement labelled q in architecture a"},
		{"for c1 : comp\n      for x\n      end for;\n    end for;",
			"instance c1 is not fully bound"},
		{"for c1 : comp use entity work.sub(nope); end for;",
			"entity sub has no architecture nope"},
	}
	for _, c := range cases {
		src := configDeclHead + "configuration cfg of top is\n  for a\n" +
			"    " + c.items + "\n  end for;\nend;\n"
		got := diagText(analyze(t, map[string]string{"t.vhd": src}, nil))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%q: unexpected\n%s", c.items, got)
		case c.want != "" && (!strings.Contains(got, c.want) ||
			strings.Count(got, "\n") != 1):
			t.Errorf("%q: want only %q in\n%s", c.items, c.want, got)
		}
	}
}

// TestDisconnectRules: a disconnection specification names guarded
// signals (LRM 7.4).
func TestDisconnectRules(t *testing.T) {
	head := "entity e is\nend;\narchitecture a of e is\n" +
		"  function res (x : bit_vector) return bit;\n" +
		"  function res (x : bit_vector) return bit is\n" +
		"  begin\n    return x(x'left);\n  end;\n" +
		"  subtype rbit is res bit;\n" +
		"  signal g : rbit bus;\n  signal u : bit;\n"
	cases := []struct{ spec, want string }{
		{"disconnect g : rbit after 1 ns;", ""},
		{"disconnect u : bit after 1 ns;",
			"signal u in a disconnection specification is not a " +
				"guarded signal (LRM 7.4)"},
		{"disconnect res : bit after 1 ns;", "res is not a signal"},
	}
	for _, c := range cases {
		src := head + "  " + c.spec + "\nbegin\nend;\n"
		got := diagText(analyze(t, map[string]string{"t.vhd": src}, nil))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%q: unexpected\n%s", c.spec, got)
		case c.want != "" && !strings.Contains(got, c.want):
			t.Errorf("%q: want %q in\n%s", c.spec, c.want, got)
		}
	}
}
