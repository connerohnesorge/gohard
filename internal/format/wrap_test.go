package format

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

func maxLen(n int) func(*Options) {
	return func(o *Options) { o.MaxLineLength = n }
}

func TestWrapMaps(t *testing.T) {
	in := lines(
		"architecture rtl of e is",
		"begin",
		"  u : entity work.e generic map (WIDTH => 8, N => 2) port map "+
			"(clk => clk, reset_n => rst, data_in => d, q => q); -- c",
		"end;")
	wrapped := lines(
		"architecture rtl of e is",
		"begin",
		"  u : entity work.e",
		"    generic map (WIDTH => 8, N => 2)",
		"    port map (",
		"      clk     => clk,",
		"      reset_n => rst,",
		"      data_in => d,",
		"      q       => q",
		"    ); -- c",
		"end;")
	runCases(t, []ruleCase{{
		// The maps move to lines of their own; the port map is still
		// too long and goes one association per line, `=>` aligned.
		// The trailing comment stays at the end of the statement.
		name: "80",
		in:   in,
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e",
			"    generic map (WIDTH => 8, N => 2)",
			"    port map (clk => clk, reset_n => rst, data_in => d, "+
				"q => q); -- c",
			"end;"),
	}, {
		name: "60",
		opts: maxLen(60),
		in:   in,
		want: wrapped,
	}, {
		// Wrapped maps align `=>` with format.align_ports off too.
		name: "60-noalign",
		opts: func(o *Options) { o.MaxLineLength = 60; o.AlignPorts = false },
		in:   in,
		want: wrapped,
	}, {
		name: "40",
		opts: maxLen(40),
		in:   in,
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e",
			"    generic map (WIDTH => 8, N => 2)",
			"    port map (",
			"      clk     => clk,",
			"      reset_n => rst,",
			"      data_in => d,",
			"      q       => q",
			"    ); -- c",
			"end;"),
	}, {
		// The comment does not count: the code fits, nothing wraps.
		name: "120",
		opts: maxLen(120),
		in:   in,
		want: in,
	}, {
		name: "generic-map-too",
		opts: maxLen(40),
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e generic map (WIDTH => 8, DEPTH => 16)",
			"    port map (a => b);",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e",
			"    generic map (",
			"      WIDTH => 8,",
			"      DEPTH => 16",
			"    )",
			"    port map (a => b);",
			"end;"),
	}})
}

func TestWrapClauses(t *testing.T) {
	runCases(t, []ruleCase{{
		name: "port clause",
		in: lines(
			"entity e is port (clk : in std_logic; reset_n : in "+
				"std_logic; d : in std_logic_vector(7 downto 0));",
			"end;"),
		want: lines(
			"entity e is",
			"  port (",
			"    clk     : in std_logic;",
			"    reset_n : in std_logic;",
			"    d       : in std_logic_vector(7 downto 0)",
			"  );",
			"end;"),
	}, {
		// The identifier list's comma is not a separator of the port
		// list.
		name: "identifier list",
		opts: maxLen(40),
		in: lines(
			"entity e is",
			"  port (a, b : in bit; long_name : out bit);",
			"end;"),
		want: lines(
			"entity e is",
			"  port (",
			"    a, b      : in bit;",
			"    long_name : out bit",
			"  );",
			"end;"),
	}})
}

func TestWrapLists(t *testing.T) {
	runCases(t, []ruleCase{{
		// The outermost list wraps; the inner call fits and stays.
		name: "nested call",
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  z <= some_function(argument_one, argument_two, "+
				"other_function(a, b, c), last_one_x);",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  z <= some_function(",
			"    argument_one,",
			"    argument_two,",
			"    other_function(a, b, c),",
			"    last_one_x",
			"  );",
			"end;"),
	}, {
		// A wrapped element that is still too long wraps in turn.
		name: "recursive",
		opts: maxLen(40),
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  z <= f(g(argument_one, argument_two, argument_3), x);",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  z <= f(",
			"    g(",
			"      argument_one,",
			"      argument_two,",
			"      argument_3",
			"    ),",
			"    x",
			"  );",
			"end;"),
	}, {
		// A list wrapped by hand with a too-long line goes one element
		// per line.
		name: "hand wrapped",
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  w <= g(first_argument, second_argument,",
			"    third_argument, fourth_argument, fifth_argument, "+
				"sixth_argument_xxxxxxxxxxxxx);",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  w <= g(",
			"    first_argument,",
			"    second_argument,",
			"    third_argument,",
			"    fourth_argument,",
			"    fifth_argument,",
			"    sixth_argument_xxxxxxxxxxxxx",
			"  );",
			"end;"),
	}, {
		name: "sensitivity list and aggregate",
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  p : process (clk, reset_n, data_in, data_valid, "+
				"some_other_signal, and_another) is",
			"  begin",
			"    x := (1 => a_long_value_name, 22 => another_long_value, "+
				"3 => yet_another_one);",
			"  end process;",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  p : process (",
			"    clk,",
			"    reset_n,",
			"    data_in,",
			"    data_valid,",
			"    some_other_signal,",
			"    and_another",
			"  ) is",
			"  begin",
			"    x := (",
			"      1  => a_long_value_name,",
			"      22 => another_long_value,",
			"      3  => yet_another_one",
			"    );",
			"  end process;",
			"end;"),
	}, {
		// Of two groups without separators, the one whose line up to
		// the `(` then fits wraps.
		name: "head fits",
		opts: maxLen(40),
		in: lines(
			"architecture rtl of e is",
			"  signal s : std_logic_vector(7 downto 0) := (others => '0');",
			"begin",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"  signal s : std_logic_vector(",
			"    7 downto 0",
			"  ) := (others => '0');",
			"begin",
			"end;"),
	}})
}

func TestWrapUnbreakable(t *testing.T) {
	long := `"a very long string literal that cannot be broken anywhere ` +
		`at all ok"`
	in := lines(
		"architecture rtl of e is",
		"begin",
		"  assert false report "+long+" severity note;",
		// The string alone does not fit on a line of its own either.
		"  y <= f("+long+");",
		"  -- a comment line that is longer than the limit and is never"+
			" wrapped by the formatter",
		"  q <= a_very_long_identifier_name_that_goes_on_and_on_and_on_xx;",
		"end;")
	runCases(t, []ruleCase{{
		name: "80",
		in:   in,
		want: in,
	}, {
		name: "40",
		opts: maxLen(40),
		in:   in,
		want: in,
	}})
}

func TestWrapKeepsVerbatim(t *testing.T) {
	// Lines of verbatim chunks and `-- psl` comments never wrap; a
	// group holding a chunk is not exploded.
	runCases(t, []ruleCase{{
		name: "inactive branch",
		opts: maxLen(40),
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e port map (a => b, cc => d,",
			"`if FOO = \"1\" then",
			"     e   =>   f,  g => h, i => j, k => l, m => n, o => p,",
			"`end if",
			"     q => r);",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  u : entity work.e",
			"    port map (a => b, cc => d,",
			"`if FOO = \"1\" then",
			"     e   =>   f,  g => h, i => j, k => l, m => n, o => p,",
			"`end if",
			"      q => r);",
			"end;"),
	}, {
		name: "psl",
		opts: maxLen(40),
		in: lines(
			"architecture rtl of e is",
			"begin",
			"  -- psl assert always (req -> next (ack or (busy and ok)));",
			"end;"),
		want: lines(
			"architecture rtl of e is",
			"begin",
			"  -- psl assert always (req -> next (ack or (busy and ok)));",
			"end;"),
	}})
}

// TestWrapProperties checks on the test files, at the narrowest limit,
// that wrapping only adds line breaks before tokens (every line start
// of the base layout stays one, no new line starts with a comment),
// and that the result is a fixed point: no line of it can be wrapped
// further, which is what makes the pass idempotent.
func TestWrapProperties(t *testing.T) {
	for _, p := range vhdlFiles(t) {
		src := mustRead(t, p)
		for _, std := range []token.Standard{token.Std2008,
			token.Std2019} {
			res := parser.Parse(src, parser.Options{Standard: std})
			if !Parses(res) {
				continue
			}
			opts := Default()
			opts.MaxLineLength = MinMaxLineLength
			opts = opts.normalized()
			base := build(res, opts)
			d := build(res, opts)
			wrapLines(d)
			var starts []int
			for _, ln := range d.lines {
				starts = append(starts, ln.first)
			}
			for _, ln := range base.lines {
				if _, ok := slices.BinarySearch(starts,
					ln.first); !ok {
					t.Fatalf("%s: line break before %q lost", p,
						d.atoms[ln.first].text)
				}
			}
			for li, ln := range d.lines {
				a := d.atoms[ln.first]
				if li < len(base.lines) && base.lines[li].first ==
					ln.first {
					continue
				}
				if _, ok := slices.BinarySearchFunc(base.lines,
					ln.first, func(l line, f int) int {
						return l.first - f
					}); !ok && !a.isToken() {
					t.Fatalf("%s: new line starts with %q", p,
						a.text)
				}
			}
			w := newWrapper(d)
			for li, ln := range d.lines {
				a := &d.atoms[ln.first]
				o := w.match[ln.first]
				if !a.isToken() || a.kind != token.RParen || o < 0 ||
					w.groupHasSep(o) || d.lineOf[o] == li {
					continue
				}
				if _, ok := slices.BinarySearchFunc(base.lines,
					ln.first, func(l line, f int) int {
						return l.first - f
					}); ok {
					continue // the input already broke here
				}
				// A split group without separators: every one of its
				// lines fits, or the split would not have been made.
				// The `)` line is checked only when the `)` was on the
				// line of the `(` when the split was decided
				// (singleFits). Then the content fit on one line and
				// stayed there; a `)` that an earlier break (of a map
				// or clause, or of an inner group) or the input had
				// already moved leaves a content of several lines.
				check := []int{d.lineOf[o]}
				if li == d.lineOf[o]+2 {
					check = append(check, li)
				}
				for _, k := range check {
					if d.codeWidth(k) > opts.MaxLineLength {
						t.Fatalf("%s (%v): line %d of a split group is"+
							" %d wide: %q", p, std, k, d.codeWidth(k),
							lineText(d, k))
					}
				}
			}
			for li := range d.lines {
				if w.wrapLine(li) {
					t.Fatalf("%s: line %d wraps again:\n%s", p, li,
						strings.TrimSpace(string(Format(res,
							opts).Text)))
				}
			}
		}
	}
}

// TestWrapSingleGroup: a group without separators is exploded only
// when its three lines (up to the `(`, the content, the `)` and the
// rest) all fit; splitting `rising_edge(clk)` or `s(i)` off a line whose
// rest stays too long gains nothing, so the line is left as it is.
func TestWrapSingleGroup(t *testing.T) {
	cond := lines(
		"architecture rtl of e is",
		"begin",
		"  process (clk)",
		"  begin",
		"    if rising_edge(clk) and enable_signal_long = '1' and "+
			"another_condition_signal = '0' then",
		"    end if;",
		"  end process;",
		"end;")
	slice := lines(
		"architecture rtl of e is",
		"begin",
		"  data_out_register(7 downto 0) <= memory_array_long_name_x"+
			" and other_operand_long;",
		"  g : for i in 0 to 7 generate s(i) <= a_long_operand_name("+
			"i) and b; end generate;",
		"end;")
	runCases(t, []ruleCase{
		// At 40 the rest after the `)` stays too long: no split.
		{name: "cond-40", opts: maxLen(40), in: cond, want: cond},
		{name: "slice-40", opts: maxLen(40), in: slice, want: slice},
		{
			// At 80 every piece of the split fits, so it is made.
			name: "cond-80",
			in:   cond,
			want: lines(
				"architecture rtl of e is",
				"begin",
				"  process (clk)",
				"  begin",
				"    if rising_edge(",
				"      clk",
				"    ) and enable_signal_long = '1' and "+
					"another_condition_signal = '0' then",
				"    end if;",
				"  end process;",
				"end;"),
		},
		{
			name: "slice-80",
			in:   slice,
			want: lines(
				"architecture rtl of e is",
				"begin",
				"  data_out_register(",
				"    7 downto 0",
				"  ) <= memory_array_long_name_x and other_operand_long;",
				"  g : for i in 0 to 7 generate s(",
				"    i",
				"  ) <= a_long_operand_name(i) and b; end generate;",
				"end;"),
		},
		{
			// Here all three lines fit, so the split helps.
			name: "fits",
			opts: maxLen(40),
			in: lines(
				"architecture rtl of e is",
				"begin",
				"  y <= f(a_long_operand_name and b_long);",
				"end;"),
			want: lines(
				"architecture rtl of e is",
				"begin",
				"  y <= f(",
				"    a_long_operand_name and b_long",
				"  );",
				"end;"),
		},
	})
}

// lineText returns the atoms of line li with their gaps.
func lineText(d *doc, li int) string {
	var b strings.Builder
	ln := d.lines[li]
	for i := ln.first; i < ln.last; i++ {
		if i > ln.first {
			b.WriteString(d.gap[i])
		}
		b.WriteString(d.atoms[i].text)
	}
	return b.String()
}
