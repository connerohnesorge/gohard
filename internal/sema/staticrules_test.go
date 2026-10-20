package sema

import (
	"fmt"
	"strings"
	"testing"
)

// ruleCase is an architecture body (decls, then statements) that is
// wrong (bad) or legal (good): the bad one has an error on line 3 of
// the file (the line marked by the case) and nowhere else; the good
// one has no diagnostics.
type ruleCase struct {
	name, decls, stmts string
	bad                bool
}

const ruleHead = "entity e is\nend;\narchitecture a of e is\n"

func ruleFile(c ruleCase) string {
	return ruleHead + c.decls + "begin\n" + c.stmts + "end;\n"
}

// TestStaticRules: every rule this file covers rejects its bad case on
// the marked line and accepts the legal twin that differs in one
// place (LRM sections in the case names).
func TestStaticRules(t *testing.T) {
	proc := func(decls, body string) string {
		return "  process\n" + decls + "  begin\n" + body +
			"    wait;\n  end process;\n"
	}
	for _, c := range []ruleCase{
		// LRM 5.2.1: a range constraint is compatible with its subtype.
		{"5.2.1 range", "  type t is range 50 to 100;\n" +
			"  subtype s is t range 1 to 60; -- @\n", "", true},
		{"5.2.1 range ok", "  type t is range 50 to 100;\n" +
			"  subtype s is t range 60 to 70;\n", "", false},
		{"5.2.1 null range", "  type t is range 50 to 100;\n" +
			"  subtype s is t range 1 to 0;\n", "", false},
		{"5.2.1 enum", "  type t is ('a', 'b', 'c', 'd');\n" +
			"  subtype s is t range 'b' to 'c';\n" +
			"  subtype s2 is s range 'a' to 'c'; -- @\n", "", true},
		{"5.2.1 integer", "  subtype s is integer range 0 to " +
			"2147483648; -- @\n", "", true},
		// LRM 5.3.2.2: index constraint bounds belong to the index
		// subtype.
		{"5.3.2.2 index", "  constant c : bit_vector(-1 to 2) := " +
			"\"0011\"; -- @\n", "", true},
		{"5.3.2.2 index ok", "  constant c : bit_vector(0 to 3) := " +
			"\"0011\";\n", "", false},
		{"5.3.2.2 array type", "  type v is array (positive range -1 " +
			"to 8) of integer; -- @\n", "", true},
		// LRM 9.3.3.3: aggregate element values belong to the element
		// subtype.
		{"9.3.3.3 element", "  type nv is array (natural range <>) " +
			"of natural;\n  constant k : nv := (0, 1, -2); -- @\n", "",
			true},
		{"9.3.3.3 element ok", "  type nv is array (natural range <>) " +
			"of natural;\n  constant k : nv := (0, 1, 2);\n", "", false},
		{"9.3.3.3 element length", "  type cx is array (1 to 2) of " +
			"bit_vector(1 downto 0);\n" +
			"  constant x : cx := (\"00\", \"000\"); -- @\n", "", true},
		{"9.3.3.2 record field", "  type r is record f : natural; " +
			"end record;\n  constant x : r := (f => -1); -- @\n", "",
			true},
		// LRM 14.4.2.5: a process variable's initial value.
		{"14.4.2.5 variable", "", proc("    variable v : natural := "+
			"-1; -- @\n", ""), true},
		{"14.4.2.5 variable ok", "", proc("    variable v : natural "+
			":= 1;\n", ""), false},
		// LRM 8.4: a static index read by a process.
		{"8.4 read", "  type at is array (1 to 10) of boolean;\n", proc(
			"    variable k : at;\n", "    assert k(12); -- @\n"), true},
		{"8.4 read ok", "  type at is array (1 to 10) of boolean;\n",
			proc("    variable k : at;\n", "    assert k(10);\n"), false},
		{"8.4 if", "", proc("    variable s : string(1 to 2);\n",
			"    if s(3) = 'a' then -- @\n    end if;\n"), true},
		{"8.4 branch", "", proc("    variable s : string(1 to 2);\n",
			"    if false then\n      assert s(3) = 'a';\n"+
				"    end if;\n"), false},
		{"16.2 bounds attribute", "  type m is array (0 to 3) of " +
			"bit_vector(5 to 6);\n", proc("    variable v : m;\n",
			"    assert v(5)'left = 5;\n"), false},
		// LRM 5.2.3.1, 15.5.2: universal_integer is 64-bit.
		{"15.5.2 literal", "  constant c : boolean := 2E26 > 0; -- @\n",
			"", true},
		{"15.5.2 literal ok", "  constant c : boolean := " +
			"9223372036854775807 > 0;\n", "", false},
		// LRM 10.5.2.2: a null waveform element needs a guarded target.
		{"10.5.2.2 null", "  signal s : bit;\n", proc("",
			"    s <= '1', null after 1 ns; -- @\n"), true},
		{"10.5.2.2 null bus", "  function f (b : bit_vector) return " +
			"bit is begin return '0'; end;\n" +
			"  signal s : f bit bus;\n", proc("",
			"    s <= '1', null after 1 ns;\n"), false},
		// LRM 11.6: guarded targets.
		{"11.6 unguarded", "  function f (b : bit_vector) return bit " +
			"is begin return '0'; end;\n  signal s, t : f bit bus;\n",
			"  s <= transport '1'; -- @\n", true},
		{"11.6 guarded", "  function f (b : bit_vector) return bit " +
			"is begin return '0'; end;\n  signal s : f bit bus;\n",
			"  b: block (true) begin\n    s <= guarded '1';\n" +
				"  end block;\n", false},
		{"11.6 mixed", "  function f (b : bit_vector) return bit " +
			"is begin return '0'; end;\n  signal s : f bit bus;\n" +
			"  signal u : bit;\n  signal v : bit_vector(0 to 1);\n",
			"  (s, u) <= v; -- @\n", true},
		// LRM 10.5.2.1, 10.6.2.1: aggregate targets.
		{"10.5.2.1 twice", "  signal s1, s2 : bit;\n" +
			"  signal v : bit_vector(0 to 2);\n", proc("",
			"    (s1, s2, s1) <= v; -- @\n"), true},
		{"10.5.2.1 once", "  signal s1, s2, s3 : bit;\n" +
			"  signal v : bit_vector(0 to 2);\n", proc("",
			"    (s1, s2, s3) <= v;\n"), false},
		{"10.6.2.1 twice", "", proc("    variable x, y : "+
			"bit_vector(1 to 2);\n", "    (x(1), x(1)) := y; -- @\n"),
			true},
		{"10.6.2.1 once", "", proc("    variable x, y : "+
			"bit_vector(1 to 2);\n", "    (x(1), x(2)) := y;\n"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := ruleFile(c)
			want := 0
			for i, l := range strings.Split(src, "\n") {
				if strings.Contains(l, "-- @") {
					want = i + 1
				}
			}
			r := analyze(t, map[string]string{"e.vhd": src}, nil)
			got := diagText(r)
			if !c.bad {
				if got != "" {
					t.Fatalf("legal case reported:\n%s\n%s", got, src)
				}
				return
			}
			if !strings.HasPrefix(got, fmt.Sprintf("e.vhd:%d:", want)) {
				t.Fatalf("want an error on line %d:\n%s\n%s", want,
					got, src)
			}
		})
	}
}
