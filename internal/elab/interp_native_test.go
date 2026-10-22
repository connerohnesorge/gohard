package elab

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
)

// TestUnsupportedStopsTheRun: a legal statement the interpreter does
// not implement yet (named associations in an aggregate target; it
// was std.env.PslIsCovered, a call without a native, until v1.1
// delivered every std.env native) is the diagnostic
// unsupported/construct (please report) at the statement, never
// sim/runtime-error; the run fails there (sim.Ctx.Abort) and exits 2.
func TestUnsupportedStopsTheRun(t *testing.T) {
	const src = `
entity tb is end;
architecture a of tb is
begin
  process
    variable x, y : bit;
    variable v : bit_vector(1 to 2) := "10";
  begin
    wait for 1 ns;
    (1 => x, 2 => y) := v;
    report "after";
    wait;
  end process;
  process
  begin
    for i in 1 to 1000 loop
      wait for 1 ns;
    end loop;
    report "ticker ran to the end";
    wait;
  end process;
end;
`
	r := simWorld(t, src, "tb", "2019", 0)
	var got []diag.Diagnostic
	for _, d := range r.diags {
		if d.Code == diag.SimRuntimeError {
			t.Errorf("runtime error: %s", d.Message)
		}
		if d.Code == diag.UnsupportedConstruct {
			got = append(got, d)
		}
	}
	if len(got) != 1 {
		t.Fatalf("unsupported diagnostics %v, want one", got)
	}
	d := got[0]
	if want := "named associations in aggregate targets is not " +
		"supported yet, please report"; d.Message != want {
		t.Errorf("message %q, want %q", d.Message, want)
	}
	if d.Path != "t.vhd" || d.Line != 10 {
		t.Errorf("at %s:%d, want t.vhd:10", d.Path, d.Line)
	}
	if code := diag.ExitCode(r.diags, false, diag.NoSignal); code !=
		diag.ExitTool {
		t.Errorf("exit %d, want %d", code, diag.ExitTool)
	}
	if st := r.k.Status(); st != sim.Failed {
		t.Errorf("status %v, want failed", st)
	}
	if now := r.k.Now(); now != 1_000_000 {
		t.Errorf("run ended at %d fs, want 1 ns: it must stop", now)
	}
	if slices.Contains(r.out, "after") ||
		slices.Contains(r.out, "ticker ran to the end") {
		t.Errorf("the run went on after the unsupported statement: %q",
			r.out)
	}
}

// TestDesignBodyMissingIsRuntimeError: a design subprogram without a
// body (the package body was never written) is the design's error, the
// v1.0 sim/runtime-error (exit 1), never the please-report variant.
func TestDesignBodyMissingIsRuntimeError(t *testing.T) {
	const src = `
package p is
  impure function f return integer;
  procedure q;
end package;
use work.p.all;
entity tb is end;
architecture a of tb is
begin
  process
    variable v : integer;
  begin
    wait for 1 ns;
    %s;
    wait;
  end process;
end;
`
	for _, std := range []string{"2008", "2019"} {
		for _, c := range []struct{ call, name string }{
			{"q", "work.p.q"}, {"v := f", "work.p.f"},
		} {
			r := simWorld(t, fmt.Sprintf(src, c.call), "tb", std, 0)
			var msgs []string
			for _, d := range r.diags {
				if d.Code == diag.UnsupportedConstruct {
					t.Errorf("%s: %s", std, d.Message)
				}
				if d.Code == diag.SimRuntimeError {
					msgs = append(msgs, d.Message)
				}
			}
			want := c.name + " is not supported in simulation in " +
				"tb._p0 at 1 ns"
			if len(msgs) != 1 || !strings.HasSuffix(msgs[0], want) {
				t.Errorf("%s %s: runtime errors %q, want %q", std,
					c.call, msgs, want)
			}
			if code := diag.ExitCode(r.diags, false,
				diag.NoSignal); code != diag.ExitProblems {
				t.Errorf("%s: exit %d, want %d", std, code,
					diag.ExitProblems)
			}
		}
	}
}

// TestNativeErrorChain: the error survives wrapping by statement
// location and errors.Join, so the process always finds it, and its
// text is the please-report variant of unsupported/construct.
func TestNativeErrorChain(t *testing.T) {
	x := &sema.Decl{Text: "EPOCH", Unit: &sema.Unit{Library: "std",
		Name: "env"}}
	err := unsupported(x)
	want := diag.NewVariant(diag.UnsupportedConstruct, diag.PleaseReport,
		"std.env.EPOCH").Message
	if err.Error() != want {
		t.Errorf("%q, want %q", err.Error(), want)
	}
	for _, w := range []error{err,
		located(&node{src: sim.SrcRef{Path: "a.vhd", Line: 3}}, err),
		errors.Join(errors.New("other"), err)} {
		var ne *nativeError
		if !errors.As(w, &ne) || ne.name != "std.env.EPOCH" {
			t.Errorf("%v: native error lost", w)
		}
	}
	m := &machine{}
	if _, err := m.reflectFunc(x, 7, nil, nil); !errors.As(err,
		new(*nativeError)) {
		t.Errorf("reflectFunc stub: %v", err)
	}
	if err := m.reflectProc(x, 7, nil); !errors.As(err,
		new(*nativeError)) {
		t.Errorf("reflectProc stub: %v", err)
	}
}

// TestSimulateStandards: the helper runs a design under every
// standard it parses in; a design without standard-dependent parts
// prints the same lines under each.
func TestSimulateStandards(t *testing.T) {
	const src = `
entity tb is end;
architecture a of tb is
  signal s : integer := 0;
begin
  s <= 1 after 1 ns, 2 after 2 ns;
  process (s)
  begin
    report integer'image(s);
  end process;
end;
`
	var first []string
	for _, std := range []string{"1993", "2002", "2008", "2019"} {
		r := simulate(t, src, "tb", runOpts{std: std})
		var msgs []string
		for _, d := range r.diags {
			msgs = append(msgs, d.Message)
		}
		if len(msgs) == 0 {
			t.Fatalf("%s: no reports", std)
		}
		if first == nil {
			first = msgs
		} else if !slices.Equal(msgs, first) {
			t.Errorf("%s: %q, want %q", std, msgs, first)
		}
	}
}
