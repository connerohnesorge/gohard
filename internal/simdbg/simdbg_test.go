package simdbg_test

import (
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/simdbg"
	"github.com/connerohnesorge/gohard/internal/simrun"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// repo is the repository root, from this package's folder.
const repo = "../.."

func read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
}

// designs returns the corpus designs, each as the options of its main
// run at seed: the run `gohard check --sim --tb` makes in the design's
// folder.
func designs(t *testing.T) []corpus.Design {
	t.Helper()
	b, err := read(corpus.ManifestPath)
	if err != nil {
		t.Fatal(corpus.Missing(corpus.ManifestPath, err))
	}
	m, err := corpus.ParseManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	return m.Designs
}

// options returns the run options of design d's main run at seed, and
// false when its analysis has errors (the run does not simulate).
func options(t *testing.T, d corpus.Design, seed uint64) (simrun.Options,
	bool,
) {
	t.Helper()
	r := d.Runs()[0]
	src, err := r.Sources(read)
	if err != nil {
		t.Fatal(err)
	}
	res, cfg, err := r.Analyze(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, dg := range res.Diagnostics {
		if dg.Severity.String() == "error" {
			return simrun.Options{}, false
		}
	}
	root := filepath.FromSlash(simenv.DefaultDir)
	files := map[string][]byte{}
	for _, p := range d.Inputs {
		b, err := read(path.Join(d.DirOf(), p))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = b
	}
	fs := simenv.New(simenv.Options{Dir: root, Files: files}).FS
	return simrun.Options{Result: res, Sim: cfg.Simulation,
		Testbench: d.Testbench, Seed: seed,
		Env: simrun.Env{Root: root, FS: fs}}, true
}

// stopText is the state of a stop as text: everything but the reason.
func stopText(s *simdbg.Stop) string {
	var b strings.Builder
	fmt.Fprintf(&b, "event %d at %d+%d process %d depth %d %s:%d:%d\n",
		s.Event, s.Time, s.Delta, s.Process, s.Depth, s.Src.Path,
		s.Src.Line, s.Src.Col)
	for _, th := range s.Threads {
		fmt.Fprintf(&b, "thread %d %s\n", th.Process, th.Path)
		for _, f := range th.Frames {
			fmt.Fprintf(&b, "  frame %s %s:%d:%d\n", f.Name, f.Src.Path,
				f.Src.Line, f.Src.Col)
			for _, v := range f.Vars {
				fmt.Fprintf(&b, "    %s : %s = %s\n", v.Name, v.Type,
					v.Value)
			}
		}
	}
	for _, v := range s.Signals {
		fmt.Fprintf(&b, "signal %s : %s = %s\n", v.Name, v.Type, v.Value)
	}
	return b.String()
}

func design(t *testing.T, name string) corpus.Design {
	t.Helper()
	for _, d := range designs(t) {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no corpus design %s", name)
	return corpus.Design{}
}

// maxStops bounds the stops of one session in the corpus tests: a
// loop with a breakpoint stops on every iteration.
const maxStops = 300

// TestReplayTrace is the replay test of #68: on every corpus design
// that simulates, at seeds 0 and 1, a session with a breakpoint on
// every line a statement starts on, continued from stop to stop to
// the end of the run, records the trace bytes, status, diagnostics
// and assert counts of the same run without the debugger. After
// maxStops stops the breakpoints are cleared and the run continues.
func TestReplayTrace(t *testing.T) {
	for _, d := range designs(t) {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			for seed := range uint64(2) {
				o, ok := options(t, d, seed)
				if !ok {
					t.Skip("does not simulate")
				}
				plain := simrun.Run(o)
				if plain.Trace == nil {
					t.Skip("does not simulate")
				}
				s := simdbg.Start(o)
				var paths []string
				for _, p := range d.Runs()[0].Paths {
					if ls := s.Lines(p); len(ls) > 0 {
						s.SetBreakpoints(p, ls)
						paths = append(paths, p)
					}
				}
				stops := 0
				for {
					r := s.Move(simdbg.Continue)
					if r.Ended {
						break
					}
					if r.Stop == nil || r.Reason !=
						simdbg.ReasonBreakpoint {
						t.Fatalf("seed %d: %+v", seed, r)
					}
					if stops++; stops == maxStops {
						for _, p := range paths {
							s.SetBreakpoints(p, nil)
						}
					}
				}
				if stops == 0 {
					t.Fatalf("seed %d: no breakpoint was hit", seed)
				}
				got, _ := s.Outcome()
				compareOutcomes(t, seed, plain, got)
				t.Logf("seed %d: %d stops", seed, stops)
			}
		})
	}
}

func compareOutcomes(t *testing.T, seed uint64, want,
	got simrun.Outcome,
) {
	t.Helper()
	if got.Trace == nil {
		t.Fatalf("seed %d: no trace: %v", seed, got.Diagnostics)
	}
	if n, a, b, ok := trace.FirstDiff(trace.Encode(want.Trace),
		trace.Encode(got.Trace)); ok {
		t.Fatalf("seed %d: trace differs at line %d:\n%q\n%q", seed, n,
			a, b)
	}
	if got.Status != want.Status || got.Asserts != want.Asserts ||
		!reflect.DeepEqual(got.Diagnostics, want.Diagnostics) {
		t.Fatalf("seed %d: %s %+v %v, want %s %+v %v", seed, got.Status,
			got.Asserts, got.Diagnostics, want.Status, want.Asserts,
			want.Diagnostics)
	}
}

// walkSeed, when set, runs only that walk of TestReverseStep: the
// seed a failure prints.
var walkSeed = flag.Uint64("simdbg.walk", 0,
	"run only this walk seed of TestReverseStep")

// TestReverseStep is the reverse-step property of #68 on the corpus:
// moving back lands on the state moving forward to the same point
// gives. On every corpus design that simulates, at seeds 0 and 1, a
// seeded walk of forward moves (Next, StepIn, StepOut, Continue to a
// few random breakpoint lines, and Goto up to 40 statements ahead)
// records the state of every stop; after a move that ends the run the
// walk goes on, by jumps only, from the stop before it in a new
// session.
// After each step that stays at one call depth of one process,
// StepBack must land on the stop it came from with the same state
// (the law: StepBack undoes such a step), and Goto must return to the
// state the step reached. Then StepBack and ReverseContinue moves walk
// back to the first statement; each must land earlier than it started
// and on the state the walk recorded there, or else on the state a
// new session moving forward straight to that point (Goto) reaches.
func TestReverseStep(t *testing.T) {
	for i, d := range designs(t) {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()
			for seed := range uint64(2) {
				walk := uint64(i)*2 + seed + 1
				if *walkSeed != 0 && walk != *walkSeed {
					continue
				}
				o, ok := options(t, d, seed)
				if !ok {
					t.Skip("does not simulate")
				}
				reverseWalk(t, d, o, walk)
			}
		})
	}
}

func reverseWalk(t *testing.T, d corpus.Design, o simrun.Options,
	walk uint64,
) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("walk %d (-simdbg.walk=%d): %s", walk, walk,
			fmt.Sprintf(format, args...))
	}
	rng := &env.SplitMix64{State: walk}
	s := simdbg.Start(o)
	r := s.Entry()
	if r.Stop == nil {
		t.Skip("runs no statement")
	}
	seen := map[int64]string{}
	record := func(st *simdbg.Stop) {
		t.Helper()
		txt := stopText(st)
		if old, ok := seen[st.Event]; ok && old != txt {
			fail("event %d reached twice with two states:\n%s\n%s",
				st.Event, old, txt)
		}
		seen[st.Event] = txt
	}
	record(r.Stop)
	var lines []srcLine
	for _, p := range d.Runs()[0].Paths {
		for _, l := range s.Lines(p) {
			lines = append(lines, srcLine{p, l})
		}
	}
	for range env.Intn(rng, 3) {
		bp := lines[env.Intn(rng, len(lines))]
		s.SetBreakpoints(bp.path, []int{bp.line})
	}
	// jump is a forward Goto up to 40 statements ahead: it moves the
	// walk to other processes.
	const jump = simdbg.Move(0)
	moves := []simdbg.Move{simdbg.Next, simdbg.Next, simdbg.StepIn,
		simdbg.StepIn, simdbg.StepOut, simdbg.Continue, jump, jump}
	last := r.Stop
	laws, backs := 0, 0
	for range 60 {
		prev := last
		m := moves[env.Intn(rng, len(moves))]
		if m == jump {
			r = s.Goto(prev.Event + 1 + int64(env.Intn(rng, 40)))
		} else {
			r = s.Move(m)
		}
		if r.Ended {
			// Back to the last stop in a new session, which has no
			// breakpoints; from there on the walk only jumps, so it
			// replays the whole run seldom.
			moves = []simdbg.Move{jump}
			s = simdbg.Start(o)
			if g := s.Goto(prev.Event); g.Stop == nil ||
				stopText(g.Stop) != seen[prev.Event] {
				fail("goto %d in a new session: %+v", prev.Event, g)
			}
			continue
		}
		if r.Stop.Event <= prev.Event {
			fail("move %d went from %d to %d", m, prev.Event,
				r.Stop.Event)
		}
		record(r.Stop)
		last = r.Stop
		if m != simdbg.Next && m != simdbg.StepIn ||
			r.Reason != simdbg.ReasonStep ||
			r.Stop.Process != prev.Process ||
			r.Stop.Depth != prev.Depth {
			continue
		}
		b := s.Move(simdbg.StepBack)
		if b.Stop == nil || b.Stop.Event != prev.Event {
			fail("step back from %d after move %d from %d: %+v",
				last.Event, m, prev.Event, b)
		}
		if got := stopText(b.Stop); got != seen[prev.Event] {
			fail("step back to %d: state\n%s\nforward\n%s",
				prev.Event, got, seen[prev.Event])
		}
		g := s.Goto(last.Event)
		if g.Stop == nil || stopText(g.Stop) != seen[last.Event] {
			fail("goto %d after a step back: %+v", last.Event, g)
		}
		laws++
	}
	fresh := 0
	for at := s.Stop(); at.Event > 1; {
		m := simdbg.StepBack
		if env.Intn(rng, 5) == 0 {
			m = simdbg.ReverseContinue
		}
		b := s.Move(m)
		if b.Stop == nil || b.Stop.Event >= at.Event {
			fail("move %d back from %d: %+v", m, at.Event, b)
		}
		got := stopText(b.Stop)
		want, ok := seen[b.Stop.Event]
		if !ok && fresh < 4 {
			fresh++
			f := simdbg.Start(o).Goto(b.Stop.Event)
			if f.Stop == nil {
				fail("goto %d in a new session: %+v", b.Stop.Event, f)
			}
			want, ok = stopText(f.Stop), true
		}
		if ok && got != want {
			fail("move %d back to %d: state\n%s\nforward\n%s", m,
				b.Stop.Event, got, want)
		}
		at = b.Stop
		backs++
	}
	t.Logf("walk %d: %d stops forward, %d step-back laws, %d moves "+
		"back, %d checked in a new session", walk, len(seen), laws,
		backs, fresh)
}

type srcLine struct {
	path string
	line int
}

// TestCancel: a cancelled move leaves the session at its stop, and the
// next move goes on from there.
func TestCancel(t *testing.T) {
	o, ok := options(t, design(t, "wait_forms"), 0)
	if !ok {
		t.Fatal("wait_forms does not simulate")
	}
	cancel := false
	o.Cancelled = func() bool { return cancel }
	s := simdbg.Start(o)
	at := s.Entry().Stop
	cancel = true
	if r := s.Move(simdbg.StepIn); !r.Cancelled || r.Stop != at ||
		s.Stop() != at {
		t.Fatalf("cancelled move: %+v", r)
	}
	cancel = false
	if r := s.Move(simdbg.StepIn); r.Stop == nil ||
		r.Stop.Event <= at.Event {
		t.Fatalf("move after the cancel: %+v", r)
	}
}
