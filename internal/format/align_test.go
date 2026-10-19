package format

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

func noAlign(o *Options) { o.AlignPorts = false }

func TestAlignPorts(t *testing.T) {
	in := lines(
		"entity e is",
		"  generic (",
		"    WIDTH : natural := 8;",
		"    N : positive",
		"  );",
		"  port (",
		"    clk : in std_logic;",
		"    reset_n : in std_logic;",
		"",
		"    data_in : in std_logic_vector(7 downto 0);",
		"    -- outputs",
		"    q : out std_logic;",
		"    valid_out, ready : out std_logic);",
		"end;")
	runCases(t, []ruleCase{{
		// `:` aligns in each list, never across the generic and port
		// lists, a blank line or a comment-only line.
		name: "on",
		in:   in,
		want: lines(
			"entity e is",
			"  generic (",
			"    WIDTH : natural := 8;",
			"    N     : positive",
			"  );",
			"  port (",
			"    clk     : in std_logic;",
			"    reset_n : in std_logic;",
			"",
			"    data_in : in std_logic_vector(7 downto 0);",
			"    -- outputs",
			"    q                : out std_logic;",
			"    valid_out, ready : out std_logic);",
			"end;"),
	}, {
		name: "off",
		opts: noAlign,
		in:   in,
		want: in,
	}, {
		// Padding written by hand is normalized first, so alignment
		// does not depend on the input's spacing.
		name: "repadded",
		in: lines(
			"entity e is",
			"  port (a        : in bit;",
			"        bb   : in bit;",
			"        ccc:out bit);",
			"end;"),
		want: lines(
			"entity e is",
			"  port (a : in bit;",
			"    bb  : in bit;",
			"    ccc : out bit);",
			"end;"),
	}, {
		// A subprogram parameter list is not a port or generic list.
		name: "parameters",
		in: lines(
			"package p is",
			"  procedure q (",
			"    a : in bit;",
			"    bbb : out bit);",
			"end;"),
		want: lines(
			"package p is",
			"  procedure q (",
			"    a : in bit;",
			"    bbb : out bit);",
			"end;"),
	}})
}

func TestAlignArrows(t *testing.T) {
	in := lines(
		"architecture rtl of e is",
		"  constant C : rec_t := (",
		"    first => '0',",
		"    b => '1',",
		"    others => '0');",
		"  constant D : bit_vector := (",
		"    '0',",
		"    '1');",
		"begin",
		"  u : entity work.e",
		"    generic map (",
		"      WIDTH => 8,",
		"      N => 2",
		"    )",
		"    port map (",
		"      clk => clk, -- the clock",
		"      reset_n => rst,",
		"      -- data",
		"      data_in => d,",
		"      q => open",
		"    );",
		"end;")
	runCases(t, []ruleCase{{
		name: "on",
		in:   in,
		want: lines(
			"architecture rtl of e is",
			"  constant C : rec_t := (",
			"    first  => '0',",
			"    b      => '1',",
			"    others => '0');",
			"  constant D : bit_vector := (",
			"    '0',",
			"    '1');",
			"begin",
			"  u : entity work.e",
			"    generic map (",
			"      WIDTH => 8,",
			"      N     => 2",
			"    )",
			"    port map (",
			"      clk     => clk, -- the clock",
			"      reset_n => rst,",
			"      -- data",
			"      data_in => d,",
			"      q       => open",
			"    );",
			"end;"),
	}, {
		// Maps written one association per line are the wrapped form:
		// their `=>` aligns whatever format.align_ports says.
		name: "off",
		opts: noAlign,
		in:   in,
		want: lines(
			"architecture rtl of e is",
			"  constant C : rec_t := (",
			"    first => '0',",
			"    b => '1',",
			"    others => '0');",
			"  constant D : bit_vector := (",
			"    '0',",
			"    '1');",
			"begin",
			"  u : entity work.e",
			"    generic map (",
			"      WIDTH => 8,",
			"      N     => 2",
			"    )",
			"    port map (",
			"      clk     => clk, -- the clock",
			"      reset_n => rst,",
			"      -- data",
			"      data_in => d,",
			"      q       => open",
			"    );",
			"end;"),
	}, {
		// A positional element ends a run; one map line holding two
		// associations aligns on its first.
		name: "mixed",
		in: lines(
			"architecture rtl of e is",
			"  constant C : rec_t := (",
			"    a => '0',",
			"    '1',",
			"    bbb => '0', c => '1',",
			"    dd => '1');",
			"begin",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"  constant C : rec_t := (",
			"    a => '0',",
			"    '1',",
			"    bbb => '0', c => '1',",
			"    dd  => '1');",
			"begin",
			"end;"),
	}})
}

func TestAlignAssignments(t *testing.T) {
	in := lines(
		"architecture rtl of e is",
		"begin",
		"  a <= b;",
		"  long_name <= c;",
		"  lbl : x <= y;",
		"  v_out <= w when c else z;",
		"",
		"  q <= d;",
		"  process (clk) is",
		"  begin",
		"    v := 1;",
		"    count := 2;",
		"    s <= v;",
		"    long_signal <= count;",
		"    -- a comment",
		"    t <= v;",
		"    if c then",
		"      x <= y;",
		"    end if;",
		"    xyz <= y;",
		"  end process;",
		"end;")
	runCases(t, []ruleCase{{
		// `<=` and `:=` runs are separate; a blank line, a comment
		// line or another statement ends a run.
		name: "on",
		in:   in,
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  a         <= b;",
			"  long_name <= c;",
			"  lbl : x   <= y;",
			"  v_out     <= w when c else z;",
			"",
			"  q <= d;",
			"  process (clk) is",
			"  begin",
			"    v     := 1;",
			"    count := 2;",
			"    s           <= v;",
			"    long_signal <= count;",
			"    -- a comment",
			"    t <= v;",
			"    if c then",
			"      x <= y;",
			"    end if;",
			"    xyz <= y;",
			"  end process;",
			"end;"),
	}, {
		name: "off",
		opts: noAlign,
		in:   in,
		want: in,
	}, {
		// No space after a block comment in the input: the token still
		// aligns, and formatting the output again changes nothing.
		name: "block comment",
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  a /* x */<= b;",
			"  bbbbbbbbbbbb <= c;",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  a /* x */    <= b;",
			"  bbbbbbbbbbbb <= c;",
			"end;"),
	}, {
		// A statement that continues on the next line ends the run.
		name: "continued",
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  a <= b;",
			"  bb <= c",
			"    or d;",
			"  ccc <= d;",
			"  dddd <= d;",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  a  <= b;",
			"  bb <= c",
			"    or d;",
			"  ccc  <= d;",
			"  dddd <= d;",
			"end;"),
	}})
}

// TestAlignmentColumns checks on the test files that, after
// formatting, every run found by the pass has its alignment tokens in
// one column, and that alignment only widens the gaps before them.
func TestAlignmentColumns(t *testing.T) {
	for _, p := range vhdlFiles(t) {
		src := mustRead(t, p)
		res := parser.Parse(src, parser.Options{Standard: token.Std2008})
		if !Parses(res) {
			continue
		}
		opts := Default().normalized()
		d := build(res, opts)
		wrapLines(d)
		before := append([]string(nil), d.gap...)
		alignLines(d)
		var run []anchor
		check := func() {
			for _, a := range run[1:] {
				if a.col+utf8.RuneCountInString(d.gap[a.atom]) !=
					run[0].col+utf8.RuneCountInString(
						d.gap[run[0].atom]) {
					t.Fatalf("%s: run not aligned at %q", p,
						d.atoms[a.atom].text)
				}
			}
		}
		for li := range d.lines {
			a, ok := d.anchorOf(li)
			if !ok || (len(run) > 0 &&
				(d.lines[li].blank > 0 || !run[0].sameRun(a))) {
				if len(run) > 1 {
					check()
				}
				run = run[:0]
			}
			if ok {
				run = append(run, a)
			}
		}
		if len(run) > 1 {
			check()
		}
		for i, g := range d.gap {
			if g != before[i] && (!strings.HasPrefix(g, before[i]) ||
				strings.Trim(g, " ") != "") {
				t.Fatalf("%s: gap %d changed from %q to %q", p, i,
					before[i], g)
			}
		}
	}
}
