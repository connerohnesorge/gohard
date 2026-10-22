package elab

import (
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// ySim simulates top of the VHDL-2019 file y.vhd with env given to
// both elaboration (Options.Env) and the interpreter, as simrun does.
func ySim(t *testing.T, src, top string, e EnvOptions) gRun {
	t.Helper()
	e.Standard = token.Std2019
	r := gAnalyze(t, []gSource{{"y.vhd", src}}, e.ToolVersion)
	b := eval.NewBudget(0)
	d, ds := Elaborate(r, Options{Top: top, Budget: b, Env: e,
		Code: Interpreter(InterpOptions{Env: e})})
	if d == nil {
		t.Fatalf("elaboration: %v", ds)
	}
	res := gRun{}
	for _, x := range ds {
		if x.Severity == diag.Error {
			res.errs = append(res.errs, string(x.Code)+": "+x.Message)
		}
	}
	if len(res.errs) > 0 {
		return res
	}
	rec := trace.NewRecorder(nil)
	k, err := sim.New(d.Model, sim.Options{Budget: b,
		TimeBudget: 1_000_000_000_000, Writer: rec})
	if err != nil {
		t.Fatal(err)
	}
	k.RunAll(64, nil)
	if err := k.Err(); err != nil {
		t.Fatal(err)
	}
	res.k = k
	tr, err := rec.Trace()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range tr.Events {
		if ev.Kind == trace.Report || ev.Kind == trace.Assert {
			res.msgs = append(res.msgs, ev.Message)
		}
	}
	for _, x := range k.Diagnostics() {
		if x.Severity == diag.Error {
			res.errs = append(res.errs, string(x.Code)+": "+x.Message)
		}
	}
	return res
}

const yElabSrc = `use std.env.all;
entity sub is
  generic (
    version : string;
    tool    : string   := TOOL_VERSION;
    line    : positive := FILE_LINE);
end entity;
architecture a of sub is
  signal len : natural := tool'length;
begin
  process begin
    report version & " " & tool & " " & integer'image(len) & " " &
      integer'image(line);
    wait;
  end process;
end architecture;
use std.env.all;
entity tb is end entity;
architecture a of tb is
  constant L : positive := FILE_LINE;
  signal fixed : string(1 to 4) := VHDL_VERSION;
  signal open_s : string := GETENV("NAME") & "!";
  signal line_s : integer := L;
  signal here : positive := FILE_LINE;
  signal path : string := FILE_PATH & "/" & FILE_NAME;
  signal unset : string(1 to 2) := GETENV("UNSET") & "[]";
  signal ep : real := EPOCH;
begin
  u1 : entity work.sub generic map (version => vhdl_version);
  u2 : entity work.sub generic map (version => GETENV("NAME"),
    tool => TOOL_NAME, line => 7);
  process begin
    report fixed & " " & open_s & " " & integer'image(line_s) & " " &
      integer'image(here) & " " & path & " " & unset & " " &
      real'image(ep);
    wait;
  end process;
end architecture;
`

// TestYElabNatives: generics (actuals and defaults) and signal initial
// values computed from std.env natives are evaluated at elaboration,
// from the run's EnvOptions, with the answers NVC 1.23 gives:
// FILE_LINE is the line of the call, the clock is the epoch.
func TestYElabNatives(t *testing.T) {
	r := ySim(t, yElabSrc, "tb", EnvOptions{ToolVersion: "9.8.7",
		Vars: map[string]string{"NAME": "env"}, Epoch: gEpoch})
	r.want(t,
		"2019 env! 20 24 ./y.vhd [] 1e+09",
		"2019 9.8.7 5 6",
		"env gohard 6 7")
}

// TestYElabNativesAgree: for random environments, what elaboration
// computes for a signal's initial value or a generic equals what the
// interpreter computes for the same call in a process: one answer per
// native, whichever evaluator asks.
func TestYElabNativesAgree(t *testing.T) {
	rng := &env.SplitMix64{State: 41}
	for iter := range 16 {
		vars := map[string]string{}
		n := 1 + env.Intn(rng, 6)
		var decls, reports strings.Builder
		var want []string
		for i := range n {
			k := fmt.Sprintf("K%d", i)
			v := strings.Repeat(string(rune('a'+env.Intn(rng, 26))),
				env.Intn(rng, 5))
			if env.Intn(rng, 3) > 0 {
				vars[k] = v
			} else {
				v = ""
			}
			fmt.Fprintf(&decls, "  signal s%d : string := GETENV(%q) & "+
				"\"|\";\n", i, k)
			fmt.Fprintf(&reports, "    report s%d & GETENV(%q);\n", i, k)
			want = append(want, v+"|"+v)
		}
		tv := fmt.Sprintf("%d.%d", env.Intn(rng, 10), env.Intn(rng, 100))
		src := `use std.env.all;
entity g is
  generic (v : string := TOOL_VERSION & VHDL_VERSION;
    l : positive := FILE_LINE);
end entity;
architecture a of g is begin
  process begin
    report v & "|" & TOOL_VERSION & VHDL_VERSION;
    report integer'image(l);
    wait;
  end process;
end architecture;
use std.env.all;
entity tb is end entity;
architecture a of tb is
` + decls.String() + `begin
  u : entity work.g;
  process begin
` + reports.String() + `    wait;
  end process;
end architecture;
`
		// The testbench's process comes first, then the instance's.
		want = append(want, tv+"2019|"+tv+"2019", "4")
		t.Run(fmt.Sprint(iter), func(t *testing.T) {
			r := ySim(t, src, "tb", EnvOptions{ToolVersion: tv,
				Vars: vars})
			r.want(t, want...)
		})
	}
}

// TestYVarPortAliasChains: an instance bound through any chain of
// aliases of a shared variable reaches that shared variable, whatever
// the chain's length (LRM 6.6.2).
func TestYVarPortAliasChains(t *testing.T) {
	rng := &env.SplitMix64{State: 77}
	for iter := range 10 {
		m := 1 + env.Intn(rng, 3)
		n := 1 + env.Intn(rng, 6)
		want := make([]int, m)
		var b strings.Builder
		b.WriteString(fCounterPkg + `
use work.counter_pkg.all;
entity tb is end entity tb;
architecture sim of tb is
`)
		for j := range m {
			fmt.Fprintf(&b, "  shared variable v%d : counter_t;\n", j)
			fmt.Fprintf(&b, "  alias a%d_1 is v%d;\n", j, j)
			for d := 2; d <= 3; d++ {
				fmt.Fprintf(&b, "  alias a%d_%d is a%d_%d;\n", j, d, j,
					d-1)
			}
		}
		b.WriteString("begin\n")
		for i := range n {
			j, k := env.Intn(rng, m), env.Intn(rng, 5)
			want[j] += k
			actual := fmt.Sprintf("v%d", j)
			if d := env.Intn(rng, 4); d > 0 {
				actual = fmt.Sprintf("a%d_%d", j, d)
			}
			fmt.Fprintf(&b, "  u%d : entity work.dut generic map "+
				"(times => %d) port map (c => %s);\n", i, k, actual)
		}
		b.WriteString("  process\n  begin\n    wait for 1 ns;\n")
		var notes []string
		for j := range m {
			fmt.Fprintf(&b, "    report \"v%d = \" & "+
				"integer'image(a%d_3.get);\n", j, j)
			notes = append(notes, fmt.Sprintf(
				"hint sim/assert-note report note: v%d = %d", j, want[j]))
		}
		b.WriteString("    wait;\n  end process;\nend architecture sim;\n")
		t.Run(fmt.Sprint(iter), func(t *testing.T) {
			r, _ := simulate19(t, b.String(), "tb")
			r.wantNotes(t, notes...)
		})
	}
}

// TestYMirrorObject: a variable the design declares of a std.reflection
// mirror type (a bundled protected type without a body) is a GoHard
// gap, unsupported/construct (please report), not a run-time error.
func TestYMirrorObject(t *testing.T) {
	r := ySim(t, `use std.reflection.all;
entity tb is end entity;
architecture a of tb is begin
  process
    variable m : integer_value_mirror_pt;
  begin
    report integer'image(m.value);
    wait;
  end process;
end architecture;
`, "tb", EnvOptions{})
	want := string(diag.UnsupportedConstruct) + ": " +
		"std.reflection.INTEGER_VALUE_MIRROR_PT object declared by the " +
		"design is not supported yet, please report"
	if len(r.errs) != 1 || r.errs[0] != want {
		t.Errorf("errors %q, want %q", r.errs, want)
	}
	if len(r.msgs) != 0 {
		t.Errorf("messages %q, want none", r.msgs)
	}
}
