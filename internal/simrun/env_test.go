package simrun

import (
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// fileTB copies the project file in.txt to out/copy.txt, appends to a
// log and reads the log back, so a run that saw another run's writes
// reports a different line count, and makes one assert of each of two
// severities plus a report.
const fileTB = `use std.textio.all;
entity tb is end;
architecture a of tb is
begin
  process
    file fi : text open read_mode is "in.txt";
    file fo : text open write_mode is "out/copy.txt";
    file lg : text open append_mode is "log.txt";
    variable l : line;
    variable n : natural := 0;
  begin
    readline(fi, l);
    writeline(fo, l);
    file_close(fo);
    write(l, string'("done"));
    writeline(lg, l);
    file_close(lg);
    file_open(lg, "log.txt", read_mode);
    while not endfile(lg) loop
      readline(lg, l);
      n := n + 1;
    end loop;
    file_close(lg);
    assert n = 1 report "log has " & integer'image(n) & " lines"
      severity failure;
    assert false report "w" severity warning;
    assert false report "n" severity note;
    report "r" severity note;
    wait;
  end process;
end;
`

func analyzed(t *testing.T) *sema.Result {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	r := sema.Analyze(sema.Input{Config: cfg, Files: []sema.Source{{
		Path: "tb.vhd", Text: []byte(fileTB)}}})
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("analysis: %d:%d: %s", d.Line, d.Col, d.Message)
		}
	}
	return r
}

func envOptions(t *testing.T, seed uint64, real bool) Options {
	t.Helper()
	if DefaultCode == nil {
		t.Skip("no interpreter registered")
	}
	root := filepath.FromSlash("/work")
	s := simenv.New(simenv.Options{Dir: root, Files: map[string][]byte{
		"in.txt": []byte("stimulus\n")}})
	return Options{Result: analyzed(t), Sim: config.Defaults().Simulation,
		Testbench: "tb", Seed: seed,
		Env: Env{Root: root, FS: s.Env().FS, RunDir: ".gohard/run/x",
			Real: real, Clock: s.Clock, Getenv: s.Env().Getenv}}
}

// TestRunFilesAndAsserts: the design reads the project through the
// FS door, its writes come back in Outcome.Files (sorted, nothing on
// the FS), and the assert counts count assertion violations only.
func TestRunFilesAndAsserts(t *testing.T) {
	o := envOptions(t, 0, false)
	out := Run(o)
	if out.Trace == nil {
		t.Fatalf("no trace: %v", out.Diagnostics)
	}
	want := []env.Written{{Path: "log.txt", Data: []byte("done\n")},
		{Path: "out/copy.txt", Data: []byte("stimulus\n")}}
	if len(out.Files) != len(want) {
		t.Fatalf("files %+v", out.Files)
	}
	for i, w := range want {
		if out.Files[i].Path != w.Path ||
			string(out.Files[i].Data) != string(w.Data) {
			t.Errorf("file %d = %s %q, want %s %q", i, out.Files[i].Path,
				out.Files[i].Data, w.Path, w.Data)
		}
	}
	if _, err := o.Env.FS.ReadFile(filepath.Join(o.Env.Root,
		"log.txt")); err == nil {
		t.Error("the run wrote to the file system")
	}
	if out.Asserts != (sim.Asserts{Note: 1, Warning: 1}) {
		t.Errorf("asserts %+v, want 1 note and 1 warning", out.Asserts)
	}
	if out.Trace.Header.NonReproducible {
		t.Error("a deterministic run is marked non-reproducible")
	}
}

// TestRunsIndependent: every run (a race re-run included) has its own
// overlay: repeated runs at any seed give the same files, never twice
// the writes, and Races leaves no trace of its runs.
func TestRunsIndependent(t *testing.T) {
	ref := Run(envOptions(t, 0, false))
	if ref.Asserts.Failure != 0 || len(ref.Files) != 2 ||
		string(ref.Files[0].Data) != "done\n" {
		t.Fatalf("first run: %+v %+v", ref.Asserts, ref.Files)
	}
	o := envOptions(t, 0, false)
	for seed := range uint64(4) {
		o.Seed = seed
		got := Run(o)
		if got.Asserts.Failure != 0 {
			t.Errorf("seed %d saw another run's log: %v", seed,
				got.Diagnostics)
		}
		if len(got.Files) != len(ref.Files) {
			t.Fatalf("seed %d: files %+v", seed, got.Files)
		}
		for i := range ref.Files {
			if string(got.Files[i].Data) != string(ref.Files[i].Data) {
				t.Errorf("seed %d: %s = %q", seed, got.Files[i].Path,
					got.Files[i].Data)
			}
		}
	}
	for _, seed := range []uint64{0, 3} {
		o.Seed = seed
		if out := Run(o); out.Asserts.Failure != 0 {
			t.Errorf("rerun at seed %d saw another run's log", seed)
		}
	}
	if ds := Races(o, []uint64{0, 3}); len(ds) != 0 {
		t.Errorf("races: %v", ds)
	}
	if _, err := o.Env.FS.ReadFile(filepath.Join(o.Env.Root,
		"log.txt")); err == nil {
		t.Error("Races wrote to the file system")
	}
}

// TestRealEnvMarked: a --real-env run's trace is marked
// non-reproducible and encodes the marker; its values are otherwise
// unchanged.
func TestRealEnvMarked(t *testing.T) {
	det := Run(envOptions(t, 0, false))
	out := Run(envOptions(t, 0, true))
	if out.Trace == nil || !out.Trace.Header.NonReproducible {
		t.Fatalf("trace not marked: %+v", out.Trace)
	}
	a, b := trace.Encode(det.Trace), trace.Encode(out.Trace)
	if string(b) == string(a) {
		t.Fatal("the encoded trace has no marker")
	}
	tr, err := trace.Decode(b)
	if err != nil || !tr.Header.NonReproducible {
		t.Errorf("decode: %v", err)
	}
	tr.Header.NonReproducible = false
	if string(trace.Encode(tr)) != string(a) {
		t.Error("--real-env changed the trace beyond the marker")
	}
}
