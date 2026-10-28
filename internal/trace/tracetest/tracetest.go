// Package tracetest builds native traces for tests: a fixed sample
// stream that covers every type, scope kind, modeled values and events,
// and seeded random traces for property tests. Until the kernel (#35)
// lands, these hand-built event streams are what the trace writer and
// exports are tested with.
package tracetest

import (
	"fmt"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Time units in femtoseconds.
const (
	PS = trace.Time(1000)
	NS = 1000 * PS
)

// Types are the VHDL types of the sample design.
func Types() []trace.Type {
	return []trace.Type{
		{Name: "std_logic_vector", Kind: trace.Array, Elem: "std_logic"},
		{Name: "std_logic", Kind: trace.Logic},
		{Name: "state_t", Kind: trace.Enum,
			Literals: []string{"idle", "run", "done"}},
		{Name: "boolean", Kind: trace.Enum,
			Literals: []string{"false", "true"}},
		{Name: "bit", Kind: trace.Enum, Literals: []string{"'0'", "'1'"}},
		{Name: "integer", Kind: trace.Integer, Low: -2147483648,
			High: 2147483647},
		{Name: "natural", Kind: trace.Integer, High: 2147483647},
		{Name: "real", Kind: trace.Real},
		{Name: "time", Kind: trace.Physical, Low: -1 << 63,
			High: 1<<63 - 1, Units: []trace.Unit{
				{Name: "fs", Mult: 1}, {Name: "ps", Mult: 1000},
				{Name: "ns", Mult: 1000000},
				{Name: "us", Mult: 1000000000},
			}},
		{Name: "count_array", Kind: trace.Array, Elem: "natural"},
		{Name: "state_array", Kind: trace.Array, Elem: "state_t"},
		{Name: "unused_t", Kind: trace.Integer, Low: 0, High: 3},
	}
}

// Signal indices of the sample design, in declaration (not path) order.
const (
	SigCountQ = iota
	SigClk
	SigRst
	SigDutClk
	SigState
	SigAddr
	SigValid
	SigN
	SigRatio
	SigDelay
	SigFlag
	SigHist
	SigMemDout
	SigModes
	SigOther
)

// Design is the sample hierarchy, deliberately not in path order.
func Design() trace.Design {
	return trace.Design{
		Types: Types(),
		Scopes: []trace.Scope{
			{Path: "tb.dut", Kind: trace.Instance, Unit: "counter(rtl)"},
			{Path: "tb", Kind: trace.Instance, Unit: "tb_counter(sim)"},
			{Path: "tb.dut.bus", Kind: trace.Record, Unit: "bus_t"},
			{Path: "tb.mem", Kind: trace.Instance,
				Unit: "mem_model(model)"},
			{Path: "tb.gen(0)", Kind: trace.ForGenerate},
			{Path: "top2", Kind: trace.Instance, Unit: "other(rtl)"},
		},
		Signals: []trace.Signal{
			SigCountQ: {Path: "tb.dut.count_q", Type: "std_logic_vector",
				Dir: trace.Out, Left: 7, Right: 0, Downto: true},
			SigClk: {Path: "tb.clk", Type: "std_logic",
				Dir: trace.Internal},
			SigRst: {Path: "tb.rst", Type: "std_logic",
				Dir: trace.Internal},
			SigDutClk: {Path: "tb.dut.clk", Type: "std_logic",
				Dir: trace.In},
			SigState: {Path: "tb.dut.state", Type: "state_t",
				Dir: trace.Internal},
			SigAddr: {Path: "tb.dut.bus.addr", Type: "std_logic_vector",
				Dir: trace.Internal, Left: 3, Right: 0, Downto: true},
			SigValid: {Path: "tb.dut.bus.valid", Type: "boolean",
				Dir: trace.Internal},
			SigN: {Path: "tb.dut.n", Type: "natural",
				Dir: trace.Internal},
			SigRatio: {Path: "tb.dut.ratio", Type: "real",
				Dir: trace.Internal},
			SigDelay: {Path: "tb.dut.delay", Type: "time",
				Dir: trace.Internal},
			SigFlag: {Path: "tb.dut.flag", Type: "bit",
				Dir: trace.Internal},
			SigHist: {Path: "tb.dut.hist", Type: "count_array",
				Dir: trace.Internal, Left: 0, Right: 2},
			SigMemDout: {Path: "tb.mem.dout", Type: "std_logic_vector",
				Dir: trace.Out, Left: 3, Right: 0, Downto: true},
			SigModes: {Path: "tb.gen(0).mode", Type: "state_array",
				Dir: trace.Internal, Left: 1, Right: 2},
			SigOther: {Path: "top2.x", Type: "integer",
				Dir: trace.Internal},
		},
		Processes: []trace.Process{
			{Path: "tb.stim"},                    // 1
			{Path: "tb.dut.p_count"},             // 2
			{Path: "tb.clkgen"},                  // 3
			{Path: "tb.mem.p_rd", Modeled: true}, // 4
		},
	}
}

// Stream process numbers (one-based, declaration order).
const (
	ProcStim  = 1
	ProcCount = 2
	ProcClk   = 3
	ProcMem   = 4
)

// Feed writes the sample run to w: initial values, a reset, three
// clock edges with counting, a zero-width glitch, model-driven values,
// and report, assert and cover events. Entries inside each delta come
// in an order that is not path order.
func Feed(w trace.Writer) error {
	err := w.Begin(trace.Header{Testbench: "tb", Seed: 7}, Design())
	ch := func(at trace.Time, d uint32, sig int, v trace.Value, p int) {
		if err == nil {
			err = w.Change(trace.Change{Time: at, Delta: d, Signal: sig,
				Value: v, Process: p})
		}
	}
	ev := func(e trace.Event) {
		if err == nil {
			err = w.Event(e)
		}
	}
	L, V, I := trace.LogicValue, trace.Vector, trace.Int
	// Initial values, in reverse path order.
	ch(0, 0, SigOther, I(-5), 0)
	ch(0, 0, SigRst, L('U'), 0)
	ch(0, 0, SigModes, trace.Elems(I(0), I(2)), 0)
	ch(0, 0, SigMemDout, V("UUUU"), 0)
	ch(0, 0, SigState, I(0), 0)
	ch(0, 0, SigRatio, trace.Float(0.5), 0)
	ch(0, 0, SigN, I(0), 0)
	ch(0, 0, SigHist, trace.Elems(I(0), I(0), I(0)), 0)
	ch(0, 0, SigFlag, I(0), 0)
	ch(0, 0, SigDelay, I(0), 0)
	ch(0, 0, SigCountQ, V("UUUUUUUU"), 0)
	ch(0, 0, SigDutClk, L('U'), 0)
	ch(0, 0, SigAddr, V("XXXX"), 0)
	ch(0, 0, SigValid, I(0), 0)
	ch(0, 0, SigClk, L('U'), 0)
	ev(trace.Event{Time: 0, Delta: 0, Kind: trace.Report,
		Severity: trace.Note, File: "tb.vhd", Line: 30,
		Process: ProcStim, Message: "start"})
	// Delta 1: the processes' first assignments.
	ch(0, 1, SigRst, L('1'), ProcStim)
	ch(0, 1, SigClk, L('0'), ProcClk)
	ch(0, 1, SigMemDout, V("ZZZZ"), ProcMem)
	ch(0, 1, SigDelay, I(int64(10*NS)), ProcStim)
	ch(0, 2, SigDutClk, L('0'), 0)
	ch(0, 2, SigCountQ, V("00000000"), ProcCount)
	// A zero-width glitch on valid at 0: true then false again.
	ch(0, 2, SigValid, I(1), ProcStim)
	ch(0, 3, SigValid, I(0), ProcStim)
	for k := 1; k <= 3; k++ {
		at := trace.Time(k) * 10 * NS
		ch(at, 0, SigClk, L('1'), ProcClk)
		ch(at, 1, SigDutClk, L('1'), 0)
		switch k {
		case 1:
			ch(at, 1, SigRst, L('0'), ProcStim)
			ch(at, 1, SigAddr, V("01LH"), ProcStim)
			ch(at, 1, SigFlag, I(1), ProcStim)
			ch(at, 1, SigModes, trace.Elems(I(1), I(2)), ProcStim)
		case 2:
			ch(at, 1, SigAddr, V("WZ-X"), ProcStim)
		}
		ch(at, 2, SigN, I(int64(k)), ProcCount)
		ch(at, 2, SigCountQ, V(fmt.Sprintf("%08b", k)), ProcCount)
		ch(at, 2, SigState, I(1), ProcCount)
		ch(at, 2, SigHist, trace.Elems(I(int64(k)), I(0), I(int64(k*k))),
			ProcCount)
		ch(at, 2, SigRatio, trace.Float(float64(k)/3), ProcCount)
		ch(at, 2, SigMemDout, V(fmt.Sprintf("%04b", k)), ProcMem)
		if k == 2 {
			ev(trace.Event{Time: at, Delta: 2, Kind: trace.CoverHit,
				File: "tb.vhd", Line: 51, Process: ProcStim,
				Message: "count reached 2"})
			ev(trace.Event{Time: at, Delta: 2, Kind: trace.Assert,
				Severity: trace.Error, File: "rtl/counter.vhd",
				Line: 18, Process: ProcCount,
				Message: "count_q /= \"00000010\" after reset"})
			ev(trace.Event{Time: at, Delta: 2, Kind: trace.Report,
				Severity: trace.Warning, File: "tb.vhd", Line: 52,
				Process: ProcStim, Message: "second edge"})
		}
		if k == 3 {
			ch(at, 3, SigState, I(2), ProcCount)
		}
		ch(at+5*NS, 0, SigClk, L('0'), ProcClk)
		ch(at+5*NS, 1, SigDutClk, L('0'), 0)
	}
	ev(trace.Event{Time: 40 * NS, Delta: 0, Kind: trace.CoverMiss,
		File: "tb.vhd", Line: 60, Message: "overflow never seen"})
	if err == nil {
		err = w.End(40 * NS)
	}
	return err
}

// Sample returns the recorded sample run with no dump_scope.
func Sample() *trace.Trace {
	return MustRecord(nil, Feed)
}

// MustRecord records feed through a Recorder for dumpScope and panics
// on an error.
func MustRecord(dumpScope []string, feed func(trace.Writer) error,
) *trace.Trace {
	r := trace.NewRecorder(dumpScope)
	if err := feed(r); err != nil {
		panic(err)
	}
	t, err := r.Trace()
	if err != nil {
		panic(err)
	}
	return t
}

// Stream is a generated run: its design and its entries in stream
// order.
type Stream struct {
	Header  trace.Header
	Design  trace.Design
	Changes []trace.Change
	Events  []trace.Event
	// Order interleaves them: false takes the next change, true the
	// next event.
	Order []bool
	End   trace.Time
}

// Feed writes s to w.
func (s *Stream) Feed(w trace.Writer) error {
	if err := w.Begin(s.Header, s.Design); err != nil {
		return err
	}
	ci, ei := 0, 0
	for _, ev := range s.Order {
		var err error
		if ev {
			err = w.Event(s.Events[ei])
			ei++
		} else {
			err = w.Change(s.Changes[ci])
			ci++
		}
		if err != nil {
			return err
		}
	}
	return w.End(s.End)
}

func pick(r env.Rand, n int) int { return env.Intn(r, n) }

// RandomValue returns a random valid value of signal s of design d.
func RandomValue(r env.Rand, d *trace.Design, s *trace.Signal) trace.Value {
	ty := findType(d, s.Type)
	if ty.Kind != trace.Array {
		return randomScalar(r, ty)
	}
	et := findType(d, ty.Elem)
	v := trace.Value{Elems: make([]trace.Value, s.Len())}
	for i := range v.Elems {
		v.Elems[i] = randomScalar(r, et)
	}
	return v
}

func findType(d *trace.Design, name string) *trace.Type {
	for i := range d.Types {
		if d.Types[i].Name == name {
			return &d.Types[i]
		}
	}
	panic("tracetest: no type " + name)
}

func randomScalar(r env.Rand, ty *trace.Type) trace.Value {
	switch ty.Kind {
	case trace.Logic:
		return trace.Value{Int: int64(pick(r, len(trace.LogicChars)))}
	case trace.Enum:
		return trace.Value{Int: int64(pick(r, len(ty.Literals)))}
	case trace.Real:
		return trace.Float(float64(int64(r.Uint64()>>11)-1<<52) / 1024)
	}
	span := uint64(ty.High - ty.Low)
	if span > 1000 {
		span = 1000
	}
	return trace.Int(ty.Low + int64(r.Uint64()%(span+1)))
}

// Random returns a seeded random stream over the sample types: up to
// nsig signals with random paths, and up to nsteps time steps of
// random changes and events, entries within each delta in random
// order. Every stream is valid input for a Recorder.
func Random(seed uint64, nsig, nsteps int) *Stream {
	r := &env.SplitMix64{State: seed}
	d := trace.Design{Types: Types()}
	names := []string{"a", "b", "c", "q", "x"}
	n := 1 + pick(r, nsig)
	for tries := 0; len(d.Signals) < n && tries < 100*n; tries++ {
		path := "tb"
		for range 1 + pick(r, 3) {
			path += "." + names[pick(r, len(names))]
		}
		bad := false
		for _, s := range d.Signals {
			// No path may repeat or be a scope of another.
			p := s.Path
			if p == path ||
				len(p) > len(path) && p[:len(path)+1] == path+"." ||
				len(path) > len(p) && path[:len(p)+1] == p+"." {
				bad = true
			}
		}
		if bad {
			continue
		}
		ty := d.Types[pick(r, len(d.Types))]
		s := trace.Signal{Path: path, Type: ty.Name, Dir: trace.Internal}
		if ty.Kind == trace.Array {
			s.Left = int64(pick(r, 4))
			s.Right = int64(pick(r, 6))
			s.Downto = pick(r, 2) == 0
		}
		d.Signals = append(d.Signals, s)
	}
	for i := range 1 + pick(r, 3) {
		d.Processes = append(d.Processes, trace.Process{
			Path:    fmt.Sprintf("tb.p%d", 3-i),
			Modeled: i == 1,
		})
	}
	st := &Stream{Header: trace.Header{Testbench: "tb", Seed: seed},
		Design: d}
	var at trace.Time
	for step := range nsteps {
		if step > 0 {
			at += trace.Time(1 + pick(r, 3))
		}
		for delta := range uint32(1 + pick(r, 3)) {
			// Each signal changes at most once per delta.
			perm := env.Shuffle(r.Uint64()|1, 0, len(d.Signals))
			for _, si := range perm[:pick(r, len(perm)+1)] {
				st.Changes = append(st.Changes, trace.Change{
					Time: at, Delta: delta, Signal: si,
					Value:   RandomValue(r, &d, &d.Signals[si]),
					Process: pick(r, len(d.Processes)+1),
				})
				st.Order = append(st.Order, false)
			}
			for range pick(r, 2) {
				st.Events = append(st.Events, trace.Event{
					Time: at, Delta: delta, Kind: trace.Report,
					Severity: trace.Note, File: "tb.vhd",
					Line:    1 + pick(r, 99),
					Process: pick(r, len(d.Processes)+1),
					Message: fmt.Sprintf("m%d", len(st.Events)),
				})
				st.Order = append(st.Order, true)
			}
		}
	}
	st.End = at + trace.Time(pick(r, 2))
	return st
}
