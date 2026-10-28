package format

import (
	"errors"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// fmtText formats src under std with opts and checks the invariants.
func fmtText(t *testing.T, std token.Standard, opts Options,
	src string) string {
	t.Helper()
	popts := parser.Options{Standard: std, PSLComments: true}
	res := parser.Parse([]byte(src), popts)
	if !Parses(res) {
		t.Fatalf("input does not parse: %v\n%s", res.Problems, src)
	}
	if err := checkInvariants([]byte(src), popts, opts); err != nil {
		t.Fatal(err)
	}
	return string(Format(res, opts).Text)
}

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

type ruleCase struct {
	name string
	std  token.Standard
	opts func(*Options)
	in   string
	want string
}

func runCases(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := Default()
			if c.opts != nil {
				c.opts(&o)
			}
			std := c.std
			if std == 0 {
				std = token.Std2008
			}
			got := fmtText(t, std, o, c.in)
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestIndentation(t *testing.T) {
	in := lines(
		"entity e is",
		"port (",
		"\ta : in bit;",
		"\t\tb : out bit",
		");",
		"end;",
		"architecture rtl of e is",
		"signal s : bit;",
		"begin",
		"p : process (a) is",
		"begin",
		"if a = '1' then",
		"case a is",
		"when '0' =>",
		"null;",
		"when others =>",
		"b <= a;",
		"end case;",
		"elsif a = '0' then",
		"null;",
		"else",
		"for i in 0 to 1 loop",
		"null;",
		"end loop;",
		"end if;",
		"end process;",
		"u0 : entity work.e",
		"port map (",
		"a => s,",
		"b => open",
		");",
		"g : for i in 0 to 1 generate",
		"s <= a",
		"or a;",
		"end generate;",
		"end;")
	runCases(t, []ruleCase{{
		name: "indent2",
		in:   in,
		want: lines(
			"entity e is",
			"  port (",
			"    a : in bit;",
			"    b : out bit",
			"  );",
			"end;",
			"architecture rtl of e is",
			"  signal s : bit;",
			"begin",
			"  p : process (a) is",
			"  begin",
			"    if a = '1' then",
			"      case a is",
			"        when '0' =>",
			"          null;",
			"        when others =>",
			"          b <= a;",
			"      end case;",
			"    elsif a = '0' then",
			"      null;",
			"    else",
			"      for i in 0 to 1 loop",
			"        null;",
			"      end loop;",
			"    end if;",
			"  end process;",
			"  u0 : entity work.e",
			"    port map (",
			"      a => s,",
			"      b => open",
			"    );",
			"  g : for i in 0 to 1 generate",
			"    s <= a",
			"      or a;",
			"  end generate;",
			"end;"),
	}, {
		name: "indent4",
		opts: func(o *Options) { o.Indent = 4 },
		in: lines("package p is", "type r is record", "a : bit;",
			"end record;", "end package;"),
		want: lines("package p is", "    type r is record",
			"        a : bit;", "    end record;", "end package;"),
	}, {
		name: "tabs in indentation",
		in:   "entity e is\n\t\t\tport (a : in bit);\n\tend;\n",
		want: "entity e is\n  port (a : in bit);\nend;\n",
	}, {
		name: "subprogram and protected",
		in: lines("package body p is", "function f(x : integer)",
			"return integer is", "variable v : integer;", "begin",
			"return x;", "end function;",
			"type pt is protected body", "end protected body;",
			"end package body;"),
		want: lines("package body p is", "  function f(x : integer)",
			"    return integer is", "    variable v : integer;",
			"  begin", "    return x;", "  end function;",
			"  type pt is protected body", "  end protected body;",
			"end package body;"),
	}, {
		name: "if generate branches",
		in: lines("architecture a of e is", "begin",
			"g : if c generate", "s <= t;", "elsif d generate",
			"s <= u;", "else generate", "s <= v;", "end generate;",
			"end;"),
		want: lines("architecture a of e is", "begin",
			"  g : if c generate", "    s <= t;", "  elsif d generate",
			"    s <= u;", "  else generate", "    s <= v;",
			"  end generate;", "end;"),
	}, {
		name: "nested parens",
		in: lines("architecture a of e is", "begin",
			"s <= f(a,", "g(b,", "c),", "d);", "end;"),
		want: lines("architecture a of e is", "begin",
			"  s <= f(a,", "    g(b,", "      c),", "    d);", "end;"),
	}})
}

func TestKeywordCase(t *testing.T) {
	in := lines(
		`ENTITY MyEnt IS`,
		`  PORT (A : IN Bit; \ENTITY\ : Out BIT);`,
		`END ENTITY MyEnt;`,
		`Architecture RTL of MyEnt is`,
		`  Constant C : String := "BEGIN End";`,
		`  Constant X : Bit_Vector := X"aB";`,
		`  Constant Q : Character := 'E';`,
		`Begin`,
		`  -- END comment`,
		`End Architecture;`)
	runCases(t, []ruleCase{{
		name: "lower",
		in:   in,
		want: lines(
			`entity MyEnt is`,
			`  port (A : in Bit; \ENTITY\ : out BIT);`,
			`end entity MyEnt;`,
			`architecture RTL of MyEnt is`,
			`  constant C : String := "BEGIN End";`,
			`  constant X : Bit_Vector := X"aB";`,
			`  constant Q : Character := 'E';`,
			`begin`,
			`  -- END comment`,
			`end architecture;`),
	}, {
		name: "upper",
		opts: func(o *Options) { o.KeywordCase = Upper },
		in:   in,
		want: lines(
			`ENTITY MyEnt IS`,
			`  PORT (A : IN Bit; \ENTITY\ : OUT BIT);`,
			`END ENTITY MyEnt;`,
			`ARCHITECTURE RTL OF MyEnt IS`,
			`  CONSTANT C : String := "BEGIN End";`,
			`  CONSTANT X : Bit_Vector := X"aB";`,
			`  CONSTANT Q : Character := 'E';`,
			`BEGIN`,
			`  -- END comment`,
			`END ARCHITECTURE;`),
	}, {
		name: "preserve",
		opts: func(o *Options) { o.KeywordCase = Preserve },
		in:   in,
		want: lines(
			`ENTITY MyEnt IS`,
			`  PORT (A : IN Bit; \ENTITY\ : Out BIT);`,
			`END ENTITY MyEnt;`,
			`Architecture RTL of MyEnt is`,
			`  Constant C : String := "BEGIN End";`,
			`  Constant X : Bit_Vector := X"aB";`,
			`  Constant Q : Character := 'E';`,
			`Begin`,
			`  -- END comment`,
			`End Architecture;`),
	}, {
		// `Context` and `Force` are not reserved in VHDL-1993: they are
		// identifiers and keep their case.
		name: "standard keyword table",
		std:  token.Std1993,
		opts: func(o *Options) {
			o.KeywordCase = Upper
			o.Standard = token.Std1993
		},
		in:   lines("entity e is", "port (Context, Force : in bit);", "end;"),
		want: lines("ENTITY e IS", "  PORT (Context, Force : IN bit);", "END;"),
	}, {
		// Outside PSL the PSL words are identifiers.
		name: "psl words outside psl",
		opts: func(o *Options) { o.KeywordCase = Upper },
		in:   lines("entity e is", "port (Always, Never : in bit);", "end;"),
		want: lines("ENTITY e IS", "  PORT (Always, Never : IN bit);", "END;"),
	}})
}

func TestNeverChanged(t *testing.T) {
	runCases(t, []ruleCase{{
		name: "block comment contents",
		in: lines("entity e is", "      /*  keep   THIS  ", "   \t as is  */",
			"end;"),
		want: lines("entity e is", "  /*  keep   THIS  ", "   \t as is  */",
			"end;"),
	}, {
		name: "literals and extended identifiers",
		in: lines("architecture a of e is", "begin",
			`s <= "a  b"  &  'x'  &  b"1_0"  &  \x  y\;`, "end;"),
		want: lines("architecture a of e is", "begin",
			`  s <= "a  b" & 'x' & b"1_0" & \x  y\;`, "end;"),
	}, {
		name: "inactive branch byte for byte",
		std:  token.Std2019,
		opts: func(o *Options) { o.Standard = token.Std2019 },
		in: lines(
			"entity e is",
			"`if TOOL_NAME = \"nobody\" then",
			"   PORT  ( a :IN bit );   ",
			"",
			"",
			"\t\tgarbage ( ( ;",
			"`else",
			"PORT (b : IN bit);",
			"`end if",
			"END;"),
		want: lines(
			"entity e is",
			"`if TOOL_NAME = \"nobody\" then",
			"   PORT  ( a :IN bit );   ",
			"",
			"",
			"\t\tgarbage ( ( ;",
			"`else",
			"  port (b : in bit);",
			"`end if",
			"end;"),
	}, {
		name: "directive lines",
		std:  token.Std2019,
		opts: func(o *Options) { o.Standard = token.Std2019 },
		in: lines("entity e is", "   `if  TOOL_NAME   /=  \"x\"  then  ",
			"end;", "  `end   if   "),
		want: lines("entity e is", "   `if  TOOL_NAME   /=  \"x\"  then  ",
			"end;", "  `end   if   "),
	}, {
		name: "psl comment spacing",
		in: lines("architecture a of e is", "begin",
			"        -- psl  assert   always (a  ->  b);", "end;"),
		want: lines("architecture a of e is", "begin",
			"  -- psl  assert   always (a  ->  b);", "end;"),
	}, {
		name: "optional syntax kept",
		in: lines("entity e is end entity e;", "entity f is", "end;",
			"architecture a of e is begin end architecture a;",
			"architecture b of e is", "begin", "end;"),
		want: lines("entity e is end entity e;", "entity f is", "end;",
			"architecture a of e is begin end architecture a;",
			"architecture b of e is", "begin", "end;"),
	}})
}

func TestSpacing(t *testing.T) {
	runCases(t, []ruleCase{{
		name: "operators and delimiters",
		in: lines("architecture a of e is",
			"constant c:integer:=2**3+4*5-6/2 mod 3;",
			"signal s,t:bit;",
			"begin",
			"s<=t;",
			"u:entity work.e port map(a=>s ,b=>t , c => open );",
			"p:process(s)",
			"variable v:integer;",
			"begin",
			"v:=-1;v:=+v;",
			"if s='1'and t/='0' then null;end if;",
			"end process;",
			"end;"),
		want: lines("architecture a of e is",
			"  constant c : integer := 2 ** 3 + 4 * 5 - 6 / 2 mod 3;",
			"  signal s, t : bit;",
			"begin",
			"  s <= t;",
			"  u : entity work.e port map(a => s, b => t, c => open);",
			"  p : process(s)",
			"    variable v : integer;",
			"  begin",
			"    v := -1; v := +v;",
			"    if s = '1' and t /= '0' then null; end if;",
			"  end process;",
			"end;"),
	}, {
		name: "parens and names",
		in: lines("architecture a of e is", "begin",
			"s <= f ( x ( 1 ) , work . p . c ) ;",
			"t <= - a ;", "u <= s 'event;", "end;"),
		want: lines("architecture a of e is", "begin",
			"  s <= f (x (1), work.p.c);",
			"  t <= -a;", "  u <= s 'event;", "end;"),
	}, {
		name: "trailing comments keep their distance",
		in: lines("entity e is", "port (a : in bit;     -- one",
			"b : in bit\t-- two", ");", "end;"),
		want: lines("entity e is", "  port (a : in bit;     -- one",
			"    b : in bit -- two", "  );", "end;"),
	}})
}

func TestBlankLinesAndWhitespace(t *testing.T) {
	runCases(t, []ruleCase{{
		name: "collapse and region edges",
		in: lines("", "", "library ieee;", "", "", "",
			"use ieee.std_logic_1164.all;", "entity e is", "",
			"", "end;", "architecture a of e is", "", "signal s : bit;",
			"", "", "signal t : bit;", "", "begin", "", "s <= t;",
			"", "-- note", "", "end;", "", ""),
		want: lines("library ieee;", "", "use ieee.std_logic_1164.all;",
			"entity e is", "end;", "architecture a of e is",
			"  signal s : bit;", "", "  signal t : bit;", "begin",
			"  s <= t;", "", "  -- note", "end;"),
	}, {
		name: "trailing whitespace and final newline",
		in:   "entity e is   \nend;  \t ",
		want: "entity e is\nend;\n",
	}, {
		name: "many final newlines",
		in:   "entity e is\nend;\n\n\n\n",
		want: "entity e is\nend;\n",
	}, {
		name: "crlf kept",
		in:   "entity e is\r\nport (a:in bit);\r\nend;\n",
		want: "entity e is\r\n  port (a : in bit);\r\nend;\r\n",
	}, {
		name: "lf dominant",
		in:   "entity e is\r\nport (a:in bit);\nend;\n",
		want: "entity e is\n  port (a : in bit);\nend;\n",
	}, {
		name: "empty file",
		in:   " \n\n\t\n",
		want: "",
	}})
}

func TestComments(t *testing.T) {
	runCases(t, []ruleCase{{
		name: "comment lines take the next code level",
		in: lines("-- file", "        -- header", "entity e is",
			"-- ports", "port (", "-- first", "a : in bit",
			"-- last", ");", "-- body end", "end;",
			"architecture a of e is", "begin", "process", "begin",
			"if a then", "null;", "        -- else part", "else",
			"null;", "end if;", "-- wait", "wait;", "end process;",
			"end;", "    -- trailer"),
		want: lines("-- file", "-- header", "entity e is",
			"  -- ports", "  port (", "    -- first", "    a : in bit",
			"    -- last", "  );", "  -- body end", "end;",
			"architecture a of e is", "begin", "  process", "  begin",
			"    if a then", "      null;", "      -- else part",
			"    else", "      null;", "    end if;", "    -- wait",
			"    wait;", "  end process;", "end;", "-- trailer"),
	}})
}

func TestRangeFormatting(t *testing.T) {
	src := lines("entity e is", "port (a:in bit;", "b:in bit);",
		"END;")
	res := parser.Parse([]byte(src), parser.Options{})
	full := Format(res, Default())
	if len(full.Edits) == 0 {
		t.Fatal("no edits")
	}
	// Every range keeps its edits inside it, and the whole file as a
	// range gives the whole-file result.
	for s := 0; s <= len(src); s++ {
		for e := s; e <= len(src); e++ {
			es := Range(res, Default(), s, e)
			for _, ed := range es {
				if ed.Start < s || ed.End > e ||
					(ed.Start == e && ed.End == e && e > s) {
					t.Fatalf("range [%d,%d): edit %+v outside", s, e, ed)
				}
			}
		}
	}
	all := Apply([]byte(src), Range(res, Default(), 0, len(src)))
	if string(all) != string(full.Text) {
		t.Errorf("whole range:\n%s\nwant\n%s", all, full.Text)
	}
	// Formatting the second line only fixes that line.
	l2 := strings.Index(src, "port")
	l3 := strings.Index(src, "b:in")
	got := string(Apply([]byte(src), Range(res, Default(), l2, l3)))
	want := lines("entity e is", "  port (a : in bit;", "b:in bit);",
		"END;")
	if got != want {
		t.Errorf("line 2 only:\n%s\nwant\n%s", got, want)
	}
	// Reversed bounds are accepted.
	if len(Range(res, Default(), l3, l2)) == 0 {
		t.Error("reversed range lost its edits")
	}
}

func TestRangeEditsOnLargerFiles(t *testing.T) {
	// Property: for every file and a set of line ranges, the range
	// edits lie inside the range, and applying the edits of a range
	// leaves the text outside it unchanged.
	for _, p := range vhdlFiles(t)[:40] {
		src := mustRead(t, p)
		res := parser.Parse(src, parser.Options{})
		if !Parses(res) {
			continue
		}
		starts := []int{0}
		for i, c := range src {
			if c == '\n' {
				starts = append(starts, i+1)
			}
		}
		for k := 0; k+3 < len(starts); k += 3 {
			s, e := starts[k], starts[k+3]
			out := Apply(src, Range(res, Default(), s, e))
			if string(out[:s]) != string(src[:s]) ||
				string(out[len(out)-(len(src)-e):]) != string(src[e:]) {
				t.Fatalf("%s: range [%d,%d) changed text outside it",
					p, s, e)
			}
		}
	}
}

func TestEditsAreMinimal(t *testing.T) {
	src := "entity e is\n      port (a : in bit);\nend;\n"
	r := Format(parser.Parse([]byte(src), parser.Options{}), Default())
	if len(r.Edits) != 1 || r.Edits[0] != (Edit{Start: 14, End: 18}) {
		t.Errorf("edits %+v, want one deletion of 4 spaces", r.Edits)
	}
	if !r.Changed() {
		t.Error("Changed is false")
	}
	same := Format(parser.Parse(r.Text, parser.Options{}), Default())
	if same.Changed() || string(same.Text) != string(r.Text) {
		t.Errorf("formatted text changes again: %+v", same.Edits)
	}
}

func TestCanJoinLexes(t *testing.T) {
	// Property: when canJoin allows writing two tokens with no space,
	// lexing the joined text gives exactly those two tokens.
	texts := []string{"a", "b1", "_", "1", "2.5", "16#F#", "'a'", "\"s\"",
		`X"0F"`, `\e x\`, "-", "+", "*", "/", "**", "=", "/=", "<", "<=",
		">", ">=", "=>", ":", ":=", "(", ")", ",", ";", ".", "'", "&",
		"|", "<>", "??", "?=", "<<", ">>", "@", "^", "[", "]", "all"}
	opts := lexer.Options{Standard: token.Std2019}
	for _, a := range texts {
		for _, b := range texts {
			if !canJoin(a, b) {
				continue
			}
			ta, _ := lexer.All([]byte(a), opts, lexer.VHDL)
			tb, _ := lexer.All([]byte(b), opts, lexer.VHDL)
			tj, _ := lexer.All([]byte(a+b), opts, lexer.VHDL)
			if len(ta) != 2 || len(tb) != 2 {
				continue // not a single token on its own
			}
			if len(tj) != 3 || tj[0].Kind != ta[0].Kind ||
				tj[0].End != len(a) || tj[1].Kind != tb[0].Kind {
				t.Errorf("canJoin(%q, %q) but %q lexes differently",
					a, b, a+b)
			}
		}
	}
}

func TestOptions(t *testing.T) {
	c := config.Defaults()
	if got := FromConfig(c); got != (Options{Indent: 2,
		KeywordCase: Lower, AlignPorts: true, MaxLineLength: 80,
		Standard: token.Std2008}) {
		t.Errorf("defaults: %+v", got)
	}
	c.Format.KeywordCase = "upper"
	c.Format.Indent = 0
	c.Format.MaxLineLength = 10
	c.Standard = "1993"
	got := FromConfig(c)
	if got.KeywordCase != Upper || got.Indent != MinIndent ||
		got.MaxLineLength != MinMaxLineLength ||
		got.Standard != token.Std1993 {
		t.Errorf("clamped: %+v", got)
	}
	for _, k := range []Case{Lower, Upper, Preserve} {
		if p, ok := ParseCase(k.String()); !ok || p != k {
			t.Errorf("ParseCase(%q) = %v %v", k.String(), p, ok)
		}
	}
	if _, ok := ParseCase("title"); ok {
		t.Error("ParseCase accepted title")
	}
}

func TestSource(t *testing.T) {
	if _, err := Source([]byte("entity e is\nport (;\nend;\n"),
		parser.Options{}, Default()); !errors.Is(err, ErrNotParsed) {
		t.Errorf("syntax error: err %v", err)
	}
	r, err := Source([]byte("ENTITY e IS END;"), parser.Options{},
		Default())
	if err != nil || string(r.Text) != "entity e is end;\n" {
		t.Errorf("got %q, %v", r.Text, err)
	}
	if Parses(nil) {
		t.Error("Parses(nil)")
	}
	big := parser.Parse([]byte("entity e is end;"),
		parser.Options{MaxBytes: 4})
	if Parses(big) {
		t.Error("a too-large file parses")
	}
	// Format still accepts it and keeps it verbatim.
	if got := Format(big, Default()); string(got.Text) !=
		"entity e is end;\n" {
		t.Errorf("unparsed file: %q", got.Text)
	}
}

func TestErrorRegionsVerbatim(t *testing.T) {
	src := lines("entity e is", "      port (a :  in bit) $  ;",
		"signal  ; x  y z;", "end;",
		"architecture a of e is", "begin", "s<=t;", "end;")
	res := parser.Parse([]byte(src), parser.Options{})
	if Parses(res) {
		t.Fatal("want a syntax error")
	}
	got := string(Format(res, Default()).Text)
	if !strings.Contains(got, "      port (a :  in bit) $  ;\n") ||
		!strings.Contains(got, "\nsignal  ; x  y z;\n") ||
		!strings.Contains(got, "  s <= t;\n") {
		t.Errorf("got\n%s", got)
	}
}

func TestHooks(t *testing.T) {
	// The layout passes see the base layout and their changes reach the
	// output and the edits.
	defer func(w, a func(*doc)) { wrapPass, alignPass = w, a }(wrapPass,
		alignPass)
	var order []string
	wrapPass = func(d *doc) { order = append(order, "wrap") }
	alignPass = func(d *doc) {
		order = append(order, "align")
		for i, a := range d.atoms {
			if a.kind == token.Colon && d.lines[d.lineOf[i]].first < i {
				d.gap[i] = "   "
			}
		}
	}
	src := "entity e is\nport (a : in bit);\nend;\n"
	r := Format(parser.Parse([]byte(src), parser.Options{}), Default())
	if strings.Join(order, ",") != "wrap,align" {
		t.Errorf("order %v", order)
	}
	if want := "entity e is\n  port (a   : in bit);\nend;\n"; string(
		r.Text) != want {
		t.Errorf("got %q", r.Text)
	}
	if string(Apply([]byte(src), r.Edits)) != string(r.Text) {
		t.Error("edits do not match the text")
	}
}
