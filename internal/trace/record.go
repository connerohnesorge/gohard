package trace

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// Design is the elaborated hierarchy a run declares before it starts.
// Slices may be in any order: a Recorder sorts them.
type Design struct {
	Types     []Type
	Scopes    []Scope
	Signals   []Signal
	Processes []Process
}

// Writer receives one run as a stream: Begin once, then Change and
// Event in non-decreasing (time, delta) order, then End once. In the
// stream, Change.Signal indexes Design.Signals and Change.Process and
// Event.Process are one-based indices into Design.Processes (0 for
// none). Within one (time, delta) the changes may come in any order
// (process order depends on the seed); a Writer must not depend on it.
//
// The simulation kernel (#35) feeds a Writer; a Recorder is the Writer
// that builds the native Trace.
type Writer interface {
	Begin(h Header, d Design) error
	Change(c Change) error
	Event(e Event) error
	// End closes the stream at the time the run stopped.
	End(at Time) error
}

// Recorder is the Writer that builds a native Trace. It records every
// signal under the simulation.dump_scope prefixes (all signals when
// there are none), sorts the entries of every delta by signal path,
// drops changes that do not change the value, and marks every value a
// stand-in model drives as modeled. Errors are sticky: after one, every
// call returns it.
type Recorder struct {
	dumpScope []string
	t         *Trace
	sigMap    []int // stream signal -> trace signal, -1 if not dumped
	procMap   []int // stream process (1-based) -> trace process
	at        Time
	delta     uint32
	started   bool
	ended     bool
	err       error
	// frozen is what was recorded before the last Clone of this
	// recorder or of the one it was cloned from, in recording order:
	// clones share these entries and nothing writes them again (#68).
	// t.Changes and t.Events hold what was recorded since.
	frozen []segment
}

// segment is a run of entries recorded before a Clone.
type segment struct {
	changes []Change
	events  []Event
}

// NewRecorder returns a Recorder for the dump_scope prefixes.
func NewRecorder(dumpScope []string) *Recorder {
	return &Recorder{dumpScope: slices.Clone(dumpScope)}
}

// Clone returns an independent copy of r: the copy holds what r
// recorded so far, and each goes on recording without the other
// seeing it, as a forked kernel needs (sim.Kernel.Clone, #68). The
// two share what r recorded so far instead of copying it, so a chain
// of clones (the debugger's checkpoints) holds each entry once. Clone
// is a write to r: do not call it while another goroutine uses r.
func (r *Recorder) Clone() *Recorder {
	r.freeze()
	c := *r
	c.dumpScope = slices.Clone(r.dumpScope)
	c.sigMap = slices.Clone(r.sigMap)
	c.procMap = slices.Clone(r.procMap)
	// Clipped: an append by either recorder reallocates instead of
	// writing a slot the other sees.
	c.frozen = slices.Clip(r.frozen)
	if r.t != nil {
		// The declarations never change after Begin. A recorded value
		// is never changed once recorded.
		t := *r.t
		t.Types, t.Scopes = slices.Clip(t.Types), slices.Clip(t.Scopes)
		t.Signals = slices.Clip(t.Signals)
		t.Processes = slices.Clip(t.Processes)
		c.t = &t
	}
	return &c
}

// freeze moves what r recorded since its last freeze to the end of
// r.frozen.
func (r *Recorder) freeze() {
	if r.t == nil || len(r.t.Changes) == 0 && len(r.t.Events) == 0 {
		return
	}
	r.frozen = append(r.frozen, segment{changes: slices.Clip(r.t.Changes),
		events: slices.Clip(r.t.Events)})
	r.t.Changes, r.t.Events = nil, nil
}

// thaw gives r.t all of r's changes and events in one new slice each,
// which Trace may sort in place: the frozen entries stay as they are
// for the clones that share them.
func (r *Recorder) thaw() {
	if len(r.frozen) == 0 {
		return
	}
	t := r.t
	nc, ne := len(t.Changes), len(t.Events)
	for _, s := range r.frozen {
		nc, ne = nc+len(s.changes), ne+len(s.events)
	}
	cs, es := make([]Change, 0, nc), make([]Event, 0, ne)
	for _, s := range r.frozen {
		cs, es = append(cs, s.changes...), append(es, s.events...)
	}
	t.Changes, t.Events = append(cs, t.Changes...), append(es, t.Events...)
	r.frozen = nil
}

func (r *Recorder) fail(err error) error {
	if r.err == nil {
		r.err = err
	}
	return r.err
}

// Begin implements Writer.
func (r *Recorder) Begin(h Header, d Design) error {
	if r.err != nil {
		return r.err
	}
	if r.started {
		return r.fail(errors.New("trace: Begin called twice"))
	}
	r.started = true
	t := &Trace{Header: Header{Testbench: h.Testbench, Seed: h.Seed}}

	sigOrder := sortedOrder(len(d.Signals), func(i int) string {
		return d.Signals[i].Path
	})
	r.sigMap = make([]int, len(d.Signals))
	for i := range r.sigMap {
		r.sigMap[i] = -1
	}
	used := map[string]bool{}
	for _, i := range sigOrder {
		s := d.Signals[i]
		if !InScope(s.Path, r.dumpScope) {
			continue
		}
		if n := len(t.Signals); n > 0 && t.Signals[n-1].Path == s.Path {
			return r.fail(fmt.Errorf("trace: duplicate signal %s",
				s.Path))
		}
		r.sigMap[i] = len(t.Signals)
		t.Signals = append(t.Signals, s)
		used[s.Type] = true
	}
	types := slices.Clone(d.Types)
	slices.SortStableFunc(types, func(a, b Type) int {
		return cmp.Compare(a.Name, b.Name)
	})
	for _, ty := range types {
		if used[ty.Name] && ty.Kind == Array {
			used[ty.Elem] = true
		}
	}
	for _, ty := range types {
		if used[ty.Name] {
			ty.Literals = slices.Clone(ty.Literals)
			ty.Units = slices.Clone(ty.Units)
			t.Types = append(t.Types, ty)
		}
	}
	for _, s := range sortedBy(d.Scopes, func(s Scope) string {
		return s.Path
	}) {
		if r.keepScope(s.Path, t.Signals) {
			t.Scopes = append(t.Scopes, s)
		}
	}
	procOrder := sortedOrder(len(d.Processes), func(i int) string {
		return d.Processes[i].Path
	})
	r.procMap = make([]int, len(d.Processes)+1)
	for _, i := range procOrder {
		r.procMap[i+1] = len(t.Processes) + 1
		t.Processes = append(t.Processes, d.Processes[i])
	}
	if err := t.validateDecls(); err != nil {
		return r.fail(fmt.Errorf("trace: %w", err))
	}
	t.index()
	r.t = t
	return nil
}

// keepScope keeps a scope that is dumped or that holds a dumped signal.
func (r *Recorder) keepScope(path string, sigs []Signal) bool {
	if InScope(path, r.dumpScope) {
		return true
	}
	i, _ := slices.BinarySearchFunc(sigs, path+".",
		func(s Signal, p string) int { return cmp.Compare(s.Path, p) })
	return i < len(sigs) && len(sigs[i].Path) > len(path) &&
		sigs[i].Path[:len(path)+1] == path+"."
}

func sortedOrder(n int, key func(int) string) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(key(a), key(b))
	})
	return order
}

func sortedBy[T any](s []T, key func(T) string) []T {
	out := slices.Clone(s)
	slices.SortStableFunc(out, func(a, b T) int {
		return cmp.Compare(key(a), key(b))
	})
	return out
}

func (r *Recorder) ready(at Time, delta uint32) error {
	switch {
	case r.err != nil:
		return r.err
	case !r.started:
		return r.fail(errors.New("trace: entry before Begin"))
	case r.ended:
		return r.fail(errors.New("trace: entry after End"))
	case at < r.at || at == r.at && delta < r.delta:
		return r.fail(fmt.Errorf("trace: entry at %d+%d after %d+%d",
			at, delta, r.at, r.delta))
	}
	r.at, r.delta = at, delta
	return nil
}

func (r *Recorder) process(p int) (int, error) {
	if p < 0 || p >= len(r.procMap) {
		return 0, r.fail(fmt.Errorf("trace: no process %d", p))
	}
	return r.procMap[p], nil
}

// Change implements Writer.
func (r *Recorder) Change(c Change) error {
	if err := r.ready(c.Time, c.Delta); err != nil {
		return err
	}
	if c.Signal < 0 || c.Signal >= len(r.sigMap) {
		return r.fail(fmt.Errorf("trace: no signal %d", c.Signal))
	}
	p, err := r.process(c.Process)
	if err != nil {
		return err
	}
	s := r.sigMap[c.Signal]
	if s < 0 {
		return nil // outside dump_scope
	}
	if err := r.t.checkValue(&r.t.Signals[s], c.Value); err != nil {
		return r.fail(fmt.Errorf("trace: %s at %d+%d: %w",
			r.t.Signals[s].Path, c.Time, c.Delta, err))
	}
	c.Signal, c.Process = s, p
	c.Value = cloneValue(c.Value)
	c.Modeled = c.Modeled || p > 0 && r.t.Processes[p-1].Modeled
	r.t.Changes = append(r.t.Changes, c)
	return nil
}

func cloneValue(v Value) Value {
	if v.Elems == nil {
		return v
	}
	out := Value{Int: v.Int, Real: v.Real,
		Elems: make([]Value, len(v.Elems))}
	for i, e := range v.Elems {
		out.Elems[i] = cloneValue(e)
	}
	return out
}

// Event implements Writer.
func (r *Recorder) Event(e Event) error {
	if err := r.ready(e.Time, e.Delta); err != nil {
		return err
	}
	p, err := r.process(e.Process)
	if err != nil {
		return err
	}
	e.Process = p
	r.t.Events = append(r.t.Events, e)
	return nil
}

// End implements Writer.
func (r *Recorder) End(at Time) error {
	switch {
	case r.err != nil:
		return r.err
	case !r.started:
		return r.fail(errors.New("trace: End before Begin"))
	case r.ended:
		return r.fail(errors.New("trace: End called twice"))
	case at < r.at:
		return r.fail(fmt.Errorf("trace: End at %d after %d", at, r.at))
	}
	r.ended = true
	r.t.Header.End = at
	return nil
}

// Trace returns the recorded trace after End.
func (r *Recorder) Trace() (*Trace, error) {
	if r.err != nil {
		return nil, r.err
	}
	if !r.ended {
		return nil, errors.New("trace: Trace before End")
	}
	r.thaw()
	t := r.t
	// The stream is already in (time, delta) order; sorting by signal
	// path inside each delta is what makes a race-free design give the
	// same bytes for every seed (#36).
	slices.SortStableFunc(t.Changes, func(a, b Change) int {
		return cmp.Or(cmp.Compare(a.Time, b.Time),
			cmp.Compare(a.Delta, b.Delta),
			cmp.Compare(a.Signal, b.Signal))
	})
	for i := 1; i < len(t.Changes); i++ {
		a, b := &t.Changes[i-1], &t.Changes[i]
		if a.Time == b.Time && a.Delta == b.Delta && a.Signal == b.Signal {
			return nil, fmt.Errorf("trace: %s changes twice at %d+%d",
				t.Signals[a.Signal].Path, a.Time, a.Delta)
		}
	}
	last := make([]*Value, len(t.Signals))
	kept := t.Changes[:0]
	for _, c := range t.Changes {
		if l := last[c.Signal]; l != nil && l.Equal(c.Value) {
			continue
		}
		kept = append(kept, c)
		last[c.Signal] = &kept[len(kept)-1].Value
	}
	t.Changes = kept
	// Events keep their emission order within a process; across
	// processes of one delta they are ordered by process path.
	slices.SortStableFunc(t.Events, func(a, b Event) int {
		return cmp.Or(cmp.Compare(a.Time, b.Time),
			cmp.Compare(a.Delta, b.Delta),
			cmp.Compare(a.Process, b.Process))
	})
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}
	return t, nil
}
