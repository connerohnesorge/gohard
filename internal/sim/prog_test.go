package sim

import (
	"fmt"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// This file is the tests' tiny process language: a Prog is a list of
// operations over scalar signals, run by the kernel through Code and
// by the naive reference scheduler (ref_test.go) directly.

type opKind uint8

const (
	opAssign opKind = iota + 1
	opWait
	opReport
	opStop
	opForce
	opRelease
)

type op struct {
	kind opKind
	sig  SigID
	// opAssign: values and delays of the waveform; opForce: vals[0].
	vals      []int64
	afters    []int64
	transport bool
	reject    int64
	hasReject bool
	// opWait
	on        []SigID
	hasFor    bool
	dur       int64
	until     bool
	untilSig  SigID
	untilVal  int64
	msg       string
	severity  trace.Severity
	finish    bool
	stopValue int64
}

// prog is a Code: a program counter over ops; after the last op it
// starts again (a VHDL process loops).
type prog struct {
	ops  []op
	pc   int
	cur  int // the op of the current wait
	sigT []*eval.Type
}

func (p *prog) Clone() Code {
	c := *p
	return &c
}

func (p *prog) val(s SigID, v int64) eval.Value {
	return eval.Value{T: p.sigT[s], I: v}
}

func (p *prog) Resume(c *Ctx) (Wait, error) {
	for range 1000 {
		if err := c.Budget().Step(1); err != nil {
			return Wait{}, err
		}
		if len(p.ops) == 0 {
			return Wait{}, nil
		}
		o := p.ops[p.pc]
		p.cur = p.pc
		p.pc = (p.pc + 1) % len(p.ops)
		switch o.kind {
		case opAssign:
			w := make([]WaveElem, len(o.vals))
			for i := range w {
				w[i] = WaveElem{Value: p.val(o.sig, o.vals[i]),
					After: o.afters[i]}
			}
			err := c.Assign(Whole(o.sig), w, Mechanism{
				Transport: o.transport, Reject: o.reject,
				HasReject: o.hasReject})
			if err != nil {
				return Wait{}, err
			}
		case opWait:
			return Wait{On: o.on, HasFor: o.hasFor, For: o.dur,
				Until: o.until}, nil
		case opReport:
			if err := c.Report(Report{Message: o.msg,
				Severity: o.severity}); err != nil {
				return Wait{}, err
			}
		case opStop:
			c.Stop(o.stopValue, o.finish, SrcRef{})
		case opForce:
			if err := c.Force(Whole(o.sig),
				p.val(o.sig, o.vals[0])); err != nil {
				return Wait{}, err
			}
		case opRelease:
			if err := c.Release(Whole(o.sig)); err != nil {
				return Wait{}, err
			}
		}
	}
	return Wait{}, fmt.Errorf("process loops without waiting")
}

func (p *prog) Until(c *Ctx) (bool, error) {
	o := p.ops[p.cur]
	return c.Value(o.untilSig).I == o.untilVal, nil
}

// --- test recording writer -------------------------------------------

type rec struct {
	sigs    []trace.Signal
	procs   []trace.Process
	changes []trace.Change
	events  []trace.Event
	end     trace.Time
	ended   bool
}

func (r *rec) Begin(_ trace.Header, d trace.Design) error {
	r.sigs, r.procs = d.Signals, d.Processes
	return nil
}
func (r *rec) Change(c trace.Change) error {
	r.changes = append(r.changes, c)
	return nil
}
func (r *rec) Event(e trace.Event) error {
	r.events = append(r.events, e)
	return nil
}
func (r *rec) End(at trace.Time) error {
	r.end, r.ended = at, true
	return nil
}

// lines renders the changes as "time delta path value" with each delta
// sorted by path, the canonical order the Recorder writes.
func (r *rec) lines() []string {
	type key struct {
		t trace.Time
		d uint32
	}
	var out []string
	var batch []string
	var cur key
	flush := func() {
		slices.Sort(batch)
		out = append(out, batch...)
		batch = nil
	}
	for i, c := range r.changes {
		k := key{c.Time, c.Delta}
		if i > 0 && k != cur {
			flush()
		}
		cur = k
		batch = append(batch, fmt.Sprintf("%d %d %s %s", c.Time, c.Delta,
			r.sigs[c.Signal].Path, valText(c.Value)))
	}
	flush()
	return out
}

func valText(v trace.Value) string {
	if v.Elems == nil {
		return fmt.Sprint(v.Int)
	}
	var b strings.Builder
	for _, e := range v.Elems {
		b.WriteByte(e.Logic())
	}
	return b.String()
}

// --- types ---------------------------------------------------------------

var (
	tInt   = eval.Integer
	tLogic = &eval.Type{Kind: eval.KindEnum, Name: "STD_ULOGIC",
		Literals: []string{"'U'", "'X'", "'0'", "'1'", "'Z'", "'W'",
			"'L'", "'H'", "'-'"}, Range: eval.Range{Left: 0, Right: 8}}
)

// logic positions
const (
	lU = iota
	lX
	l0
	l1
	lZ
	lW
	lL
	lH
	lD
)

// --- helpers -------------------------------------------------------------

type rng struct{ s env.SplitMix64 }

func newRNG(seed uint64) *rng { return &rng{env.SplitMix64{State: seed}} }

func (r *rng) IntN(n int) int { return env.Intn(&r.s, n) }

func (r *rng) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, r.IntN(i+1))
	}
}

func seedOrder(seed uint64) env.Order {
	if seed == 0 {
		return nil
	}
	return env.SeedOrder{Seed: seed}
}

type testingT interface {
	Helper()
	Fatal(args ...any)
}

func mustKernel(t testingT, m *Model, o Options) *Kernel {
	t.Helper()
	k, err := New(m, o)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
