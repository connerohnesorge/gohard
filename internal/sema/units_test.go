package sema

import (
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
)

// TestLaterUnitOfFile: a unit may not name itself or a unit declared
// after it in its design file (LRM 13.1, 13.5: the units of a file are
// analyzed in textual order and enter the library after analysis);
// the same units in separate files, which have no order, are fine.
func TestLaterUnitOfFile(t *testing.T) {
	pkgA := "package a is\n  constant c : integer := 1;\nend;\n"
	user := "use work.a.all;\nentity e is\nend;\n"
	r := analyze(t, map[string]string{"t.vhd": user + pkgA}, nil)
	got := diagText(r)
	if !strings.Contains(got, "t.vhd:1:10: library/missing-unit: "+
		"library work has no unit a") ||
		!strings.Contains(got, "work.a is declared later in this file") {
		t.Fatalf("forward reference:\n%s", got)
	}
	r = analyze(t, map[string]string{"t.vhd": pkgA + user}, nil)
	if got := diagText(r); got != "" {
		t.Fatalf("backward reference:\n%s", got)
	}
	// Files have no analysis order: either path order is fine.
	for _, ps := range [][2]string{{"a.vhd", "b.vhd"}, {"b.vhd", "a.vhd"}} {
		r = analyze(t, map[string]string{ps[0]: user, ps[1]: pkgA}, nil)
		if got := diagText(r); got != "" {
			t.Fatalf("%v:\n%s", ps, got)
		}
	}
	self := "use work.p.all;\npackage p is\nend;\n"
	r = analyze(t, map[string]string{"s.vhd": self}, nil)
	if got := diagText(r); !strings.Contains(got,
		"s.vhd:1:10: library/missing-unit: library work has no unit p") ||
		!strings.Contains(got, "only after its analysis") {
		t.Fatalf("self reference:\n%s", got)
	}
	// An architecture before its entity in one file.
	arch := "architecture x of e is\nbegin\nend;\nentity e is\nend;\n"
	r = analyze(t, map[string]string{"x.vhd": arch}, nil)
	if got := diagText(r); !strings.Contains(got,
		"x.vhd:1:19: library/missing-unit") {
		t.Fatalf("architecture first:\n%s", got)
	}
}

// TestLaterUnitPermutation: the result of a file with a forward
// reference does not depend on the other files' order (P1).
func TestLaterUnitPermutation(t *testing.T) {
	files := map[string]string{
		"a.vhd": "use work.q.all;\npackage p is\nend;\n" +
			"package q is\nend;\n",
		"b.vhd": "use work.p.all;\nentity e is\nend;\n",
		"c.vhd": "use work.q.all;\nentity f is\nend;\n",
	}
	want := diagText(analyze(t, files, nil))
	for _, order := range [][]string{{"c.vhd", "b.vhd", "a.vhd"},
		{"b.vhd", "a.vhd", "c.vhd"}} {
		var srcs []Source
		for _, p := range order {
			srcs = append(srcs, Source{Path: p, Text: []byte(files[p])})
		}
		r := Analyze(Input{Files: srcs, Config: config.Defaults()})
		if got := diagText(r); got != want {
			t.Fatalf("%v: got\n%s\nwant\n%s", order, got, want)
		}
	}
	if !strings.Contains(want, "a.vhd:1:10: library/missing-unit") {
		t.Fatalf("no forward-reference error:\n%s", want)
	}
}

// TestMissingBody: a subprogram declared in a declarative part needs
// its body there (LRM 4.3); one declared in a package needs it in the
// package body when there is one (LRM 4.8); FOREIGN subprograms,
// generic subprograms and packages without a body are not reported.
func TestMissingBody(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string // substrings of the diagnostics; nil: none
	}{
		{"architecture", "entity e is\nend;\narchitecture a of e is\n" +
			"  function f(x : integer) return integer;\n" +
			"  procedure p(x : bit);\n" +
			"  procedure q;\n  procedure q is begin end;\n" +
			"begin\nend;\n",
			[]string{"t.vhd:4:12: sema/error: missing body for " +
				"function f [INTEGER return INTEGER]",
				"t.vhd:5:13: sema/error: missing body for procedure " +
					"p [BIT]"}},
		{"process", "entity e is\nend;\narchitecture a of e is\n" +
			"begin\n  process is\n    procedure p;\n  begin\n" +
			"    wait;\n  end process;\nend;\n",
			[]string{"t.vhd:6:15: sema/error: missing body for " +
				"procedure p []"}},
		{"package body", "package k is\n  procedure p;\n" +
			"  procedure q;\nend;\npackage body k is\n" +
			"  procedure q is begin end;\nend;\n",
			[]string{"t.vhd:2:13: sema/error: missing body for " +
				"procedure p []"}},
		{"no package body", "package k is\n  procedure p;\nend;\n",
			nil},
		{"foreign", "entity e is\nend;\narchitecture a of e is\n" +
			"  procedure p;\n" +
			"  attribute foreign of p : procedure is \"VHPIDIRECT p\";\n" +
			"begin\nend;\n", nil},
		{"generic", "package k is\n" +
			"  procedure p generic (type t) parameter (x : t);\n" +
			"end;\npackage body k is\n" +
			"  procedure p generic (type t) parameter (x : t) is\n" +
			"  begin end;\nend;\n", nil},
	}
	for _, c := range cases {
		r := analyze(t, map[string]string{"t.vhd": c.src}, nil)
		got := diagText(r)
		if c.want == nil && got != "" {
			t.Errorf("%s: unexpected\n%s", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: want %q in\n%s", c.name, w, got)
			}
		}
		if n := strings.Count(got, "\n"); c.want != nil &&
			n != len(c.want) {
			t.Errorf("%s: %d diagnostics, want %d:\n%s", c.name, n,
				len(c.want), got)
		}
	}
}

// TestMissingBodySplitFiles: the package body may be in another file;
// a missing body is reported however the files are ordered.
func TestMissingBodySplitFiles(t *testing.T) {
	pkg := "package k is\n  procedure p;\n  procedure q;\nend;\n"
	body := "package body k is\n  procedure q is begin end;\nend;\n"
	for _, ps := range [][2]string{{"a.vhd", "b.vhd"}, {"b.vhd", "a.vhd"}} {
		r := analyze(t, map[string]string{ps[0]: pkg, ps[1]: body}, nil)
		want := ps[0] + ":2:13: sema/error: missing body for procedure p []\n"
		if got := diagText(r); got != want {
			t.Errorf("%v: got\n%s\nwant\n%s", ps, got, want)
		}
	}
}

// TestInheritedLibraryDeps: a secondary unit depends on the units it
// names through a library its primary unit's context clause declares
// (LRM 13.1), so a use clause naming one in a process sees it.
func TestInheritedLibraryDeps(t *testing.T) {
	files := map[string]string{
		"lib.q/p.vhd": "package p is\n  type ft is access integer;\nend;\n",
		"lib.q/e.vhd": "library q;\nentity e is\nend;\n",
		"lib.q/a.vhd": "architecture a of e is\nbegin\n" +
			"  process is\n    use q.p.all;\n    variable v : ft;\n" +
			"  begin\n    wait;\n  end process;\nend;\n",
	}
	r := analyze(t, files, func(c *config.Config) {
		c.Libraries = append(c.Libraries, config.Library{Name: "q",
			Folders: []config.Folder{{Path: "lib.q"}}})
	})
	if got := diagText(r); got != "" {
		t.Fatalf("got\n%s", got)
	}
}

// TestCalledBeforeBody: a function called while a declarative item is
// elaborated needs its body elaborated first (LRM 14.4.1); calls in
// subprograms, processes and statements run later.
func TestCalledBeforeBody(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"package body", "package k is\n  function f return natural;\n" +
			"end;\npackage body k is\n  constant c : natural := f;\n" +
			"  function f return natural is begin return 5; end;\nend;\n",
			"t.vhd:5:27: sema/error: function f is called before its " +
				"body is elaborated (LRM 14.4.1)\n"},
		{"architecture", "entity e is\nend;\narchitecture a of e is\n" +
			"  function f return natural;\n" +
			"  signal s : natural := f;\n" +
			"  function f return natural is begin return 5; end;\n" +
			"begin\nend;\n",
			"t.vhd:5:25: sema/error: function f is called before its " +
				"body is elaborated (LRM 14.4.1)\n"},
		// Left to elaboration: static evaluation reads the body.
		{"package decl", "package k is\n  function f return natural;\n" +
			"  constant c : natural := f;\nend;\npackage body k is\n" +
			"  function f return natural is begin return 5; end;\nend;\n",
			""},
		{"body first", "package k is\n  function f return natural;\n" +
			"end;\npackage body k is\n" +
			"  function f return natural is begin return 5; end;\n" +
			"  constant c : natural := f;\nend;\n", ""},
		{"in a subprogram", "package k is\n" +
			"  function f return natural;\n" +
			"  function g return natural;\nend;\npackage body k is\n" +
			"  function g return natural is\n" +
			"    constant c : natural := f;\n  begin return c; end;\n" +
			"  function f return natural is begin return 5; end;\nend;\n",
			""},
		{"in a process", "entity e is\nend;\narchitecture a of e is\n" +
			"  function f return natural;\n  function f return natural " +
			"is begin return 1; end;\nbegin\n  process is\n" +
			"    variable v : natural := f;\n  begin\n    wait;\n" +
			"  end process;\nend;\n", ""},
	}
	for _, c := range cases {
		got := diagText(analyze(t, map[string]string{"t.vhd": c.src}, nil))
		if got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// TestReanalyzedUnitSeen: while a unit that replaces an earlier one of
// its file is analyzed, the library still holds the earlier one (LRM
// 13.5), so naming it is no error.
func TestReanalyzedUnitSeen(t *testing.T) {
	src := "package p1 is\nend p1;\nuse work.p1.all;\n" +
		"package p1 is\n  constant c : natural := 5;\nend p1;\n" +
		"use work.p1.all;\nentity e is\n  constant d : natural := c;\n" +
		"end entity;\n"
	if got := diagText(analyze(t, map[string]string{"t.vhd": src},
		nil)); got != "" {
		t.Fatalf("got\n%s", got)
	}
}
