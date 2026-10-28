package elab

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/token"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// run is one simulation of an inline design.
type run struct {
	k     *sim.Kernel
	diags []diag.Diagnostic
	out   []string
	text  []byte
}

type runOpts struct {
	seed  uint64
	steps int64
	until sim.Time // time budget (0: 1 ms)
	in    []byte
	// std is the VHDL standard of the analysis and the run ("":
	// 2008).
	std string
	// interpreted runs every ieee body from source (#59).
	interpreted bool
}

func simulate(t *testing.T, src, top string, o runOpts) run {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	if o.std != "" {
		cfg.Standard = o.std
	}
	std, ok := token.ParseStandard(cfg.Standard)
	if !ok {
		t.Fatalf("unknown standard %q", cfg.Standard)
	}
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %s:%d:%d: %s", d.Path, d.Line, d.Col,
				d.Message)
		}
	}
	var out []string
	code := Interpreter(InterpOptions{Input: o.in,
		Output: func(l string) { out = append(out, l) },
		Env:    EnvOptions{Standard: std}})
	b := eval.NewBudget(o.steps)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Code: code,
		InterpretedBodies: o.interpreted})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	for _, x := range ds {
		if x.Severity == diag.Error {
			t.Fatalf("elaboration: %s", x.Message)
		}
	}
	if len(d.Missing) > 0 {
		t.Fatalf("no code for %v", d.Missing)
	}
	until := o.until
	if until == 0 {
		until = 1_000_000_000_000
	}
	rec := trace.NewRecorder(nil)
	opts := sim.Options{Budget: b, TimeBudget: until, Writer: rec,
		Seed: o.seed}
	if o.seed != 0 {
		opts.Order = env.SeedOrder{Seed: o.seed}
	}
	k, err := sim.New(d.Model, opts)
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res := run{k: k, diags: k.Diagnostics(), out: out}
	if tr, err := rec.Trace(); err == nil {
		res.text = trace.Encode(tr)
	}
	return res
}

// clean fails on any diagnostic of severity warning or error.
func (r run) clean(t *testing.T) {
	t.Helper()
	for _, d := range r.diags {
		if d.Severity != diag.Hint {
			t.Errorf("%s %s: %s", d.Severity, d.Code, d.Message)
		}
	}
}

func (r run) wantOut(t *testing.T, want ...string) {
	t.Helper()
	if strings.Join(r.out, "\n") != strings.Join(want, "\n") {
		t.Errorf("OUTPUT:\n%s\nwant:\n%s", strings.Join(r.out, "\n"),
			strings.Join(want, "\n"))
	}
}

const prelude = `
library ieee;
use ieee.std_logic_1164.all;
use ieee.numeric_std.all;
use std.textio.all;
`

// TestSequentialStatements covers the statement forms: variable
// assignments (simple, conditional, selected, aggregate targets),
// if, case, case?, loops with next and exit, while loops.
func TestSequentialStatements(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
begin
  p : process
    variable i, j, n : integer := 0;
    variable v : std_logic_vector(3 downto 0);
    variable x, y : integer;
    variable s : string(1 to 3) := "abc";
    type pair is array (1 to 2) of integer;
    procedure say(msg : string) is
      variable l : line;
    begin
      write(l, msg);
      writeline(output, l);
    end procedure;
  begin
    x := 5 when i = 0 else 6;
    say("cond " & integer'image(x));
    with x select y := 1 when 5, 2 when others;
    say("sel " & integer'image(y));
    (x, y) := pair'(y, x) when true else pair'(0, 0);
    say("swap " & integer'image(x) & " " & integer'image(y));
    v := "1010";
    case? v is
      when "1-1-" => say("match 1-1-");
      when others => say("no match");
    end case?;
    case v(1 downto 0) is
      when "10" => say("case 10");
      when others => say("case other");
    end case;
    outer : for a in 1 to 3 loop
      for b in 1 to 3 loop
        next outer when b > a;
        n := n + 1;
        exit outer when a = 3 and b = 2;
      end loop;
    end loop outer;
    say("loops " & integer'image(n));
    while j < 4 loop
      j := j + 1;
    end loop;
    say("while " & integer'image(j));
    s(2) := 'X';
    s(1 to 1) := "Y";
    say(s);
    if v(3) = '1' then
      say("if then");
    elsif v(2) = '1' then
      say("if elsif");
    else
      say("if else");
    end if;
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "cond 5", "sel 1", "swap 1 5", "match 1-1-", "case 10",
		"loops 5", "while 4", "YXc", "if then")
}

// TestSubprograms covers functions and procedures: parameter modes,
// out parameters with slices, signal parameters, a procedure that
// waits, an impure function reading a signal, recursion and return.
func TestSubprograms(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal clk : std_logic := '0';
  signal q : unsigned(3 downto 0) := (others => '0');
  function fact(n : natural) return natural is
  begin
    if n <= 1 then return 1; end if;
    return n * fact(n - 1);
  end function;
  procedure bump(variable c : inout integer; variable o : out integer;
                 d : in integer := 10) is
  begin
    c := c + d;
    o := c * 2;
  end procedure;
  procedure pulse(signal s : out std_logic; t : time) is
  begin
    s <= '1';
    wait for t;
    s <= '0';
    wait for t;
  end procedure;
  procedure fill(variable r : out std_logic_vector) is
  begin
    r := (r'range => '1');
  end procedure;
  impure function q_now return integer is
  begin
    return to_integer(q);
  end function;
  procedure say(msg : string) is
    variable l : line;
  begin
    write(l, msg);
    writeline(output, l);
  end procedure;
begin
  q <= q + 1 when rising_edge(clk);
  p : process
    variable c, o : integer := 1;
    variable v : std_logic_vector(7 downto 0) := (others => '0');
  begin
    say("fact " & integer'image(fact(5)));
    bump(c, o);
    say("bump " & integer'image(c) & " " & integer'image(o));
    bump(c, o, d => 1);
    say("bump " & integer'image(c) & " " & integer'image(o));
    fill(v(5 downto 2));
    say("fill " & to_string(v));
    for k in 1 to 3 loop
      pulse(clk, 5 ns);
    end loop;
    say("count " & integer'image(q_now) & " at " & time'image(now));
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "fact 120", "bump 11 22", "bump 12 24",
		"fill 00111100", "count 3 at 30000000 fs")
}

// TestReportSeverities: severities map to hint, warning and error;
// std.env.stop with a non-zero status is an error; the run stops at the
// stop_level (error) or at stop.
func TestReportSeverities(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
begin
  p : process
  begin
    report "plain";
    report "noted" severity note;
    assert false report "warned" severity warning;
    wait for 1 ns;
    assert 1 = 2 report "failed";
    wait for 1 ns;
    report "never";
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	var got []string
	for _, d := range r.diags {
		got = append(got, d.Severity.String()+" "+string(d.Code)+" "+
			d.Message)
	}
	want := []string{
		"hint sim/assert-note report note: plain",
		"hint sim/assert-note report note: noted",
		"warning sim/assert-warning assertion warning: warned",
		"error sim/assert-error assertion error: failed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
	if r.k.Now() != 1_000_000 || r.k.Status() != sim.Halted {
		t.Errorf("ended %v at %d, want halted at 1 ns", r.k.Status(),
			r.k.Now())
	}

	stop := prelude + `
entity tb is end entity;
architecture a of tb is
begin
  p : process
  begin
    wait for 3 ns;
    std.env.stop(3);
    report "after stop";
    wait;
  end process;
end architecture;
`
	r = simulate(t, stop, "tb", runOpts{})
	if len(r.diags) != 1 || r.diags[0].Code != diag.SimStopStatus ||
		r.diags[0].Severity != diag.Error {
		t.Fatalf("diagnostics %v, want one sim/stop-status error", r.diags)
	}
	if r.k.StopStatus() != 3 || r.k.Now() != 3_000_000 {
		t.Errorf("stop status %d at %d", r.k.StopStatus(), r.k.Now())
	}
}

// TestAccessAndProtected covers allocation, dereference, deallocate,
// records through access values, shared variables of a protected type
// used by two processes.
func TestAccessAndProtected(t *testing.T) {
	src := prelude + `
package pk is
  type counter is protected
    procedure add(n : integer);
    impure function get return integer;
  end protected;
end package;
package body pk is
  type counter is protected body
    variable total : integer := 0;
    procedure add(n : integer) is
    begin
      total := total + n;
    end procedure;
    impure function get return integer is
    begin
      return total;
    end function;
  end protected body;
end package body;

library ieee;
use std.textio.all;
use work.pk.all;
entity tb is end entity;
architecture a of tb is
  shared variable cnt : counter;
  type node;
  type node_ptr is access node;
  type node is record
    val  : integer;
    next_node : node_ptr;
  end record;
  procedure say(msg : string) is
    variable l : line;
  begin
    write(l, msg);
    writeline(output, l);
  end procedure;
begin
  adder : process
  begin
    cnt.add(5);
    wait for 1 ns;
    cnt.add(7);
    wait;
  end process;
  p : process
    variable head, n : node_ptr;
    variable sum : integer := 0;
    variable s : line;
  begin
    for i in 1 to 4 loop
      n := new node'(val => i, next_node => head);
      head := n;
    end loop;
    n := head;
    while n /= null loop
      sum := sum + n.val;
      n := n.next_node;
    end loop;
    say("sum " & integer'image(sum));
    head.all.val := 40;
    say("head " & integer'image(head.val));
    s := new string'("abc");
    s(2) := 'Z';
    say(s.all & integer'image(s'length));
    deallocate(s);
    assert s = null report "not null after deallocate";
    wait for 2 ns;
    say("counter " & integer'image(cnt.get));
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	r.wantOut(t, "sum 10", "head 40", "aZc3", "counter 12")
}

// TestTextio writes to a file, reads it back and reads INPUT.
func TestTextio(t *testing.T) {
	src := prelude + `
use ieee.std_logic_textio.all;
entity tb is end entity;
architecture a of tb is
begin
  p : process
    file f : text;
    variable l, o : line;
    variable i : integer;
    variable b : boolean;
    variable ok : boolean;
    variable t : time;
    variable sv : std_logic_vector(7 downto 0);
    variable bv : bit_vector(3 downto 0);
    variable c : character;
    variable st : file_open_status;
  begin
    file_open(st, f, "x.txt", read_mode);
    write(o, string'("missing ")); write(o, file_open_status'image(st));
    writeline(output, o);
    file_open(f, "x.txt", write_mode);
    write(l, 42, right, 5); write(l, ' '); write(l, true);
    write(l, ' '); write(l, 15 ns); write(l, ' ');
    hwrite(l, std_logic_vector'(x"3c")); write(l, ' ');
    write(l, bit_vector'("1001"));
    writeline(f, l);
    file_close(f);
    file_open(f, "x.txt", read_mode);
    readline(f, l);
    write(o, l.all); writeline(output, o);
    read(l, i); read(l, b); read(l, t, ok);
    hread(l, sv); read(l, bv);
    write(o, i); write(o, ' '); write(o, b); write(o, ' ');
    write(o, t); write(o, ' '); write(o, sv); write(o, ' ');
    write(o, bv); write(o, ' '); write(o, endfile(f));
    writeline(output, o);
    file_close(f);
    while not endfile(input) loop
      readline(input, l);
      read(l, c);
      write(o, c, left, 3); write(o, '|');
      writeline(output, o);
    end loop;
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{in: []byte("ab\ncd\n")})
	r.clean(t)
	r.wantOut(t, "missing name_error",
		"   42 TRUE 15 ns 3C 1001",
		"42 TRUE 15 ns 00111100 1001 TRUE",
		"a  |", "c  |")
}

// TestProcessAll: process(all) and concurrent statements wake on the
// signals they read, not on the ones they write.
func TestProcessAll(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal a, b, y, z : std_logic := '0';
  signal sel : integer := 0;
  signal v : std_logic_vector(1 downto 0);
  signal runs : natural := 0;
begin
  comb : process (all)
  begin
    if sel = 0 then y <= a; else y <= b; end if;
  end process;
  with sel select z <= a when 0, b when others;
  v <= (a, b);
  stim : process
  begin
    a <= '1'; wait for 1 ns;
    assert y = '1' and z = '1' and v = "10" report "a";
    sel <= 1; wait for 1 ns;
    assert y = '0' and z = '0' report "sel";
    b <= '1'; wait for 1 ns;
    assert y = '1' and z = '1' and v = "11" report "b";
    std.env.finish;
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
	if r.k.Now() != 3_000_000 {
		t.Errorf("finished at %d", r.k.Now())
	}
}

// TestBudget: a process looping without a wait ends in
// sim/budget-exceeded, never a hang.
func TestBudget(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
begin
  p : process
    variable n : integer := 0;
  begin
    loop
      n := (n + 1) mod 7;
    end loop;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{steps: 10000})
	if len(r.diags) != 1 || r.diags[0].Code != diag.SimBudgetExceeded {
		t.Fatalf("diagnostics %v", r.diags)
	}
}

// TestCloneContinues: a kernel cloned at any slice boundary continues
// exactly like the original (process stacks, variables, the heap and
// files are copied), and every seed gives the same trace.
func TestCloneContinues(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal clk : std_logic := '0';
  signal cnt : unsigned(7 downto 0) := (others => '0');
  procedure tick(signal c : inout std_logic) is
  begin
    c <= not c;
    wait for 3 ns;
  end procedure;
begin
  clk_gen : process
  begin
    for i in 1 to 40 loop
      tick(clk);
    end loop;
    wait;
  end process;
  done : process
  begin
    wait for 119 ns;
    report "cnt " & to_hstring(cnt);
    wait;
  end process;
  count : process (clk)
    variable l : line;
  begin
    if rising_edge(clk) then
      cnt <= cnt + 3;
      l := new string'(integer'image(to_integer(cnt)));
      deallocate(l);
    end if;
  end process;
end architecture;
`
	base := simulate(t, src, "tb", runOpts{})
	base.clean(t)
	for seed := uint64(1); seed < 4; seed++ {
		if r := simulate(t, src, "tb", runOpts{seed: seed}); !bytes.Equal(
			r.text, base.text) {
			t.Errorf("seed %d: trace differs", seed)
		}
	}
	// Fork at several points: both copies end alike.
	cfg := config.Defaults()
	cfg.Standard = "2008"
	res := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	d, _ := Elaborate(res, Options{Top: "tb", Code: Interpret})
	for cut := 1; cut < 200; cut += 23 {
		k, err := sim.New(d.Model, sim.Options{})
		if err != nil {
			t.Fatal(err)
		}
		k.Run(cut)
		c := k.Clone()
		k.RunAll(64, nil)
		c.RunAll(64, nil)
		// The copies share the evaluator's caches, so the one run
		// second may use fewer steps; values and times agree.
		if k.Now() != c.Now() || k.Status() != c.Status() ||
			!reflect.DeepEqual(k.Diagnostics(), c.Diagnostics()) {
			t.Errorf("cut %d: original %v at %d %v, clone %v at %d %v",
				cut, k.Status(), k.Now(), k.Diagnostics(), c.Status(),
				c.Now(), c.Diagnostics())
		}
		if ds := k.Diagnostics(); len(ds) != 1 ||
			ds[0].Message != "report note: cnt 3C" {
			t.Errorf("cut %d: %v", cut, ds)
		}
	}
}

// TestReleaseResolved: a force or release defines no driver (LRM
// 14.7.2), so releasing a resolved signal gives back the resolution of
// its real drivers ('0' and '1': 'X'), not of an extra 'U' driver.
func TestReleaseResolved(t *testing.T) {
	src := prelude + `
entity tb is end entity;
architecture a of tb is
  signal s : std_logic;
begin
  s <= '0';
  s <= '1';
  stim : process
  begin
    wait for 1 ns;
    assert s = 'X' report "before force";
    s <= force 'H';
    wait for 1 ns;
    assert s = 'H' report "forced";
    s <= release;
    wait for 1 ns;
    assert s = 'X' report "released: " & std_logic'image(s);
    std.env.finish;
    wait;
  end process;
end architecture;
`
	r := simulate(t, src, "tb", runOpts{})
	r.clean(t)
}

// TestInterpretSharesWorld: under the default hook every process sees
// the same shared variable; r reads what w stored 1 ns earlier.
func TestInterpretSharesWorld(t *testing.T) {
	src := `entity tb is end entity;
architecture a of tb is
  type counter is protected
    procedure set(x : integer);
    impure function get return integer;
  end protected;
  type counter is protected body
    variable v : integer := 0;
    procedure set(x : integer) is begin v := x; end procedure;
    impure function get return integer is begin return v; end function;
  end protected body;
  shared variable c : counter;
begin
  w : process begin c.set(7); wait; end process;
  r : process begin
    wait for 1 ns;
    assert c.get = 7 report "got " & integer'image(c.get) severity error;
    wait;
  end process;
end architecture;
`
	cfg := config.Defaults()
	cfg.Standard = "2008"
	res := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "t.vhd", Text: []byte(src)}}})
	d, ds := Elaborate(res, Options{Top: "tb", Code: Interpret})
	if d == nil {
		t.Fatal(ds)
	}
	k, err := sim.New(d.Model, sim.Options{})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if ds := k.Diagnostics(); len(ds) != 0 {
		t.Fatalf("%v", ds)
	}
}
