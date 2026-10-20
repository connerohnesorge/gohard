package sema

import (
	"strings"
	"testing"
)

const genPkg = "package gp is\n  generic (g : integer);\n" +
	"  constant c : integer := g;\nend;\n"

// TestPackageInstRules: an instance names an uninstantiated package
// (LRM 4.9), whose body is analyzed first when it needs one; outside
// the package its declarations are reached only through instances
// (LRM 8.3) and a use clause cannot name it (LRM 12.4).
func TestPackageInstRules(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"instance", genPkg +
			"package i is new work.gp generic map (1);\n", ""},
		{"not generic", "package np is\nend;\n" +
			"package i is new work.np generic map (1);\n",
			"work.np is not an uninstantiated package"},
		{"std", "package i is new std.standard generic map (1);\n",
			"std.standard is not an uninstantiated package"},
		{"interface", "package k is\n" +
			"  generic (package f is new std.standard generic map (<>));\n" +
			"end;\n", "std.standard is not an uninstantiated package"},
		{"expanded", genPkg + "entity e is\nend;\n" +
			"architecture a of e is\n" +
			"  constant d : integer := work.gp.c;\nbegin\nend;\n",
			"cannot reference c in the uninstantiated package gp"},
		{"via instance", genPkg + "entity e is\nend;\n" +
			"architecture a of e is\n" +
			"  package i is new work.gp generic map (2);\n" +
			"  constant d : integer := i.c;\nbegin\nend;\n", ""},
		{"use all", genPkg + "use work.gp.all;\nentity e is\nend;\n",
			"a use clause cannot name the uninstantiated package work.gp"},
		{"use unit", genPkg + "use work.gp;\nentity e is\nend;\n", ""},
		{"body later", "package bp is\n  generic (g : integer);\n" +
			"  function f return integer;\nend;\n" +
			"package i is new work.bp generic map (1);\n" +
			"package body bp is\n" +
			"  function f return integer is begin return g; end;\nend;\n",
			"package bp cannot be instantiated before its body"},
		{"body first", "package bp is\n  generic (g : integer);\n" +
			"  function f return integer;\nend;\n" +
			"package body bp is\n" +
			"  function f return integer is begin return g; end;\nend;\n" +
			"package i is new work.bp generic map (1);\n", ""},
	}
	for _, c := range cases {
		got := diagText(analyze(t, map[string]string{"t.vhd": c.src},
			nil))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%s: unexpected\n%s", c.name, got)
		case c.want != "" && (!strings.Contains(got, c.want) ||
			strings.Count(got, "\n") != 1):
			t.Errorf("%s: want only %q in\n%s", c.name, c.want, got)
		}
	}
}

// TestSubprogramInstRules: a subprogram instantiation names a generic
// subprogram (LRM 4.4) whose body comes before it in its unit.
func TestSubprogramInstRules(t *testing.T) {
	head := "entity e is\nend;\narchitecture a of e is\n"
	tail := "begin\nend;\n"
	cases := []struct{ name, decls, want string }{
		{"ok", "  function g generic (n : integer) (x : integer) " +
			"return integer is\n  begin\n    return x + n;\n  end;\n" +
			"  function i is new g generic map (1);\n", ""},
		{"spec then body", "  function g generic (n : integer) " +
			"return integer;\n  function g generic (n : integer) " +
			"return integer is\n  begin\n    return n;\n  end;\n" +
			"  function i is new g generic map (1);\n", ""},
		{"not generic", "  function g return integer is\n  begin\n" +
			"    return 1;\n  end;\n" +
			"  function i is new g generic map (1);\n",
			"function g is not an uninstantiated subprogram"},
		{"no body yet", "  function g generic (n : integer) " +
			"return integer;\n  function i is new g generic map (1);\n",
			"function g cannot be instantiated before its body"},
	}
	for _, c := range cases {
		src := head + c.decls + tail
		got := diagText(analyze(t, map[string]string{"t.vhd": src}, nil))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%s: unexpected\n%s", c.name, got)
		case c.want != "" && !strings.Contains(got, c.want):
			t.Errorf("%s: want %q in\n%s", c.name, c.want, got)
		}
	}
}
