package sim

import (
	"bytes"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Race detection (#29, #35): a race-free design gives a byte-identical
// trace at every seed, so a difference between the traces of two seeds
// is a race, reported as sim/race naming two processes, never as a
// determinism failure.
//
// The two processes come from the run orders the kernel reports
// through Options.Observe: the first differing trace entry was caused
// at its own cycle (an assert or report) or by the processes of the
// last cycle that ran before it (a value change); among the processes
// that ran in that cycle in both runs, the pair whose relative order
// differs between the seeds is the race. The process the first
// differing entry is attributed to is preferred as one of the pair.

// RunOrder is the processes that ran in one cycle, in run order.
type RunOrder struct {
	Now   Time
	Delta uint32
	Procs []int32
}

// OrderLog records the run orders of one run: set Options.Observe to
// its Observe method.
type OrderLog struct {
	Orders []RunOrder
}

// Observe implements Options.Observe.
func (l *OrderLog) Observe(now Time, delta uint32, order []int32) {
	l.Orders = append(l.Orders, RunOrder{now, delta, order})
}

// RaceRun is one finished run for race detection.
type RaceRun struct {
	Seed   uint64
	Trace  *trace.Trace
	Orders []RunOrder
}

// Race is a difference between the traces of two seeds.
type Race struct {
	// ProcA and ProcB are the process paths, ProcA the lower in
	// declaration order.
	ProcA, ProcB string
	SeedA, SeedB uint64
	// Now and Delta locate the first differing trace entry.
	Now   Time
	Delta uint32
}

// Diagnostic returns the sim/race warning at src.
func (r Race) Diagnostic(src SrcRef) diag.Diagnostic {
	d := diag.New(diag.SimRace, r.ProcA, r.ProcB,
		strconv.FormatUint(r.SeedA, 10), strconv.FormatUint(r.SeedB, 10))
	if src.Path != "" {
		d = d.At(src.Path, src.Line, src.Col)
	}
	return d
}

// entry is one trace line for comparison: a change or an event.
type entry struct {
	at    Time
	delta uint32
	event bool
	proc  int
	text  []byte
}

func entries(t *trace.Trace) []entry {
	var out []entry
	ci, ei := 0, 0
	for ci < len(t.Changes) || ei < len(t.Events) {
		useEv := ei < len(t.Events) && (ci >= len(t.Changes) ||
			evFirst(&t.Events[ei], &t.Changes[ci]))
		if useEv {
			e := &t.Events[ei]
			ei++
			out = append(out, entry{at: e.Time, delta: e.Delta,
				event: true, proc: e.Process, text: eventKey(e)})
			continue
		}
		c := &t.Changes[ci]
		ci++
		out = append(out, entry{at: c.Time, delta: c.Delta,
			proc: c.Process, text: changeKey(t, c)})
	}
	return out
}

// evFirst orders an event before a change of the same cycle, as the
// canonical text does.
func evFirst(e *trace.Event, c *trace.Change) bool {
	if e.Time != c.Time {
		return e.Time < c.Time
	}
	return e.Delta <= c.Delta
}

func eventKey(e *trace.Event) []byte {
	var b bytes.Buffer
	b.WriteString(e.Kind.String())
	b.WriteByte(' ')
	b.WriteString(e.Severity.String())
	b.WriteByte(' ')
	b.WriteString(e.File)
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(e.Line))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(e.Process))
	b.WriteByte(' ')
	b.WriteString(e.Message)
	return b.Bytes()
}

func changeKey(t *trace.Trace, c *trace.Change) []byte {
	var b bytes.Buffer
	path, typ := "?", (*trace.Type)(nil)
	if c.Signal >= 0 && c.Signal < len(t.Signals) {
		s := &t.Signals[c.Signal]
		path = s.Path
		for i := range t.Types {
			if t.Types[i].Name == s.Type {
				typ = &t.Types[i]
			}
		}
	}
	b.WriteString(path)
	b.WriteByte(' ')
	if typ != nil {
		b.WriteString(t.FormatValue(typ, c.Value))
	} else {
		b.WriteString(strconv.FormatInt(c.Value.Int, 10))
	}
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(c.Process))
	if c.Modeled {
		b.WriteString(" modeled")
	}
	return b.Bytes()
}

// FindRace compares the traces of two runs of model m. It returns the
// race when they differ in any entry or in their end time. When the
// cycle that caused the difference has several inverted pairs, the
// runs in more (other seeds of the same model) narrow them down: the
// racing pair is one whose relative order agrees with the outcome
// (trace equal to a's or not) in every run.
func FindRace(m *Model, a, b RaceRun, more ...RaceRun) (Race, bool) {
	if a.Seed > b.Seed {
		a, b = b, a
	}
	ea, eb := entries(a.Trace), entries(b.Trace)
	i := 0
	for i < len(ea) && i < len(eb) && ea[i].at == eb[i].at &&
		ea[i].delta == eb[i].delta && ea[i].event == eb[i].event &&
		bytes.Equal(ea[i].text, eb[i].text) {
		i++
	}
	var at Time
	var delta uint32
	event := false
	var attributed []int
	byPath := map[string]int{} // lookup only
	for i := range m.Processes {
		byPath[m.Processes[i].Path] = i
	}
	// Trace process indices are one-based into the trace's own
	// (path-sorted) process table; map them back to the model.
	modelProc := func(t *trace.Trace, p int) (int, bool) {
		if p <= 0 || p > len(t.Processes) {
			return 0, false
		}
		i, ok := byPath[t.Processes[p-1].Path]
		return i, ok
	}
	switch {
	case i < len(ea) || i < len(eb):
		first := func(e []entry) (Time, uint32, bool) {
			if i < len(e) {
				return e[i].at, e[i].delta, true
			}
			return 0, 0, false
		}
		ta, da, okA := first(ea)
		tb, db, okB := first(eb)
		at, delta = ta, da
		if !okA || okB && (tb < ta || tb == ta && db < da) {
			at, delta = tb, db
		}
		for k, e := range [][]entry{ea, eb} {
			if i < len(e) && e[i].at == at && e[i].delta == delta {
				event = event || e[i].event
				t := a.Trace
				if k == 1 {
					t = b.Trace
				}
				if p, ok := modelProc(t, e[i].proc); ok {
					attributed = append(attributed, p)
				}
			}
		}
	case a.Trace.Header.End != b.Trace.Header.End:
		at = min(a.Trace.Header.End, b.Trace.Header.End)
		delta = ^uint32(0)
	default:
		return Race{}, false
	}
	pa, pb := racePair(a, b, more, at, delta, event, attributed)
	name := func(p int32) string {
		if p >= 0 && int(p) < len(m.Processes) {
			return m.Processes[p].Path
		}
		return "the kernel"
	}
	if pa > pb && pb >= 0 {
		pa, pb = pb, pa
	}
	return Race{ProcA: name(pa), ProcB: name(pb), SeedA: a.Seed,
		SeedB: b.Seed, Now: at, Delta: delta}, true
}

// racePair names the two processes whose relative order differs in the
// cycle that caused the first difference at (at, delta).
func racePair(a, b RaceRun, more []RaceRun, at Time, delta uint32,
	event bool, attributed []int,
) (int32, int32) {
	oa, ob := a.Orders, b.Orders
	// The cycles of both runs agree up to the difference; walk them
	// in step and keep the last cycle at or before the cause.
	cause := func(o RunOrder) bool {
		if event {
			return o.Now < at || o.Now == at && o.Delta <= delta
		}
		return o.Now < at || o.Now == at && o.Delta < delta
	}
	var cand [][2]int32
	n := min(len(oa), len(ob))
	last := -1
	for c := range n {
		if !cause(oa[c]) || !cause(ob[c]) {
			break
		}
		if inversions(oa[c].Procs, ob[c].Procs) != nil {
			last = c
		}
	}
	if last >= 0 {
		cand = inversions(oa[last].Procs, ob[last].Procs)
		cand = consistent(cand, a, more, last)
	}
	for _, p := range attributed {
		for _, c := range cand {
			if int(c[0]) == p || int(c[1]) == p {
				return c[0], c[1]
			}
		}
	}
	if len(cand) > 0 {
		return cand[0][0], cand[0][1]
	}
	// No inverted pair (the order differed before any recorded cycle,
	// or the runs were not observed): name the attributed processes.
	ps := slices.Compact(slices.Sorted(slices.Values(attributed)))
	switch len(ps) {
	case 0:
		return -1, -1
	case 1:
		return int32(ps[0]), -1
	}
	return int32(ps[0]), int32(ps[1])
}

// consistent keeps the pairs of cand whose relative order in cycle c
// predicts, in every run of more, whether its trace equals a's; it
// returns cand unchanged when no pair survives.
func consistent(cand [][2]int32, a RaceRun, more []RaceRun, c int,
) [][2]int32 {
	if len(more) == 0 {
		return cand
	}
	enc := trace.Encode(a.Trace)
	same := make([]bool, len(more))
	for i, r := range more {
		same[i] = bytes.Equal(enc, trace.Encode(r.Trace))
	}
	before := func(o []RunOrder, p, q int32) (bool, bool) {
		if c >= len(o) {
			return false, false
		}
		ip, iq := slices.Index(o[c].Procs, p), slices.Index(o[c].Procs, q)
		if ip < 0 || iq < 0 {
			return false, false
		}
		return ip < iq, true
	}
	var out [][2]int32
	for _, pq := range cand {
		ref, _ := before(a.Orders, pq[0], pq[1])
		ok := true
		for i, r := range more {
			bf, known := before(r.Orders, pq[0], pq[1])
			if known && (bf == ref) != same[i] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, pq)
		}
	}
	if len(out) == 0 {
		return cand
	}
	return out
}

// inversions returns the pairs (p, q), p < q, of processes that ran in
// both orders with p and q in a different relative order, sorted.
func inversions(a, b []int32) [][2]int32 {
	pos := func(o []int32, p int32) int { return slices.Index(o, p) }
	var out [][2]int32
	for i, p := range a {
		for _, q := range a[i+1:] {
			bp, bq := pos(b, p), pos(b, q)
			if bp < 0 || bq < 0 || bp < bq {
				continue
			}
			x, y := min(p, q), max(p, q)
			out = append(out, [2]int32{x, y})
		}
	}
	slices.SortFunc(out, func(x, y [2]int32) int {
		if x[0] != y[0] {
			return int(x[0] - y[0])
		}
		return int(x[1] - y[1])
	})
	return out
}

// CheckRaces runs model m at every seed through run and compares each
// trace with the first seed's. It returns one sim/race warning for the
// first seed whose trace differs, or none. run must build a fresh
// kernel for the seed and record its orders (OrderLog).
func CheckRaces(m *Model, seeds []uint64,
	run func(seed uint64) (RaceRun, error),
) ([]diag.Diagnostic, error) {
	if len(seeds) < 2 {
		return nil, nil
	}
	base, err := run(seeds[0])
	if err != nil {
		return nil, err
	}
	enc := trace.Encode(base.Trace)
	runs := make([]RaceRun, 0, len(seeds)-1)
	for _, s := range seeds[1:] {
		r, err := run(s)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	for _, r := range runs {
		if bytes.Equal(enc, trace.Encode(r.Trace)) {
			continue
		}
		race, ok := FindRace(m, base, r, runs...)
		if !ok {
			// Same entries but different bytes: still a race.
			race = Race{ProcA: "the kernel", ProcB: "the kernel",
				SeedA: base.Seed, SeedB: r.Seed}
		}
		return []diag.Diagnostic{race.Diagnostic(m.Src)}, nil
	}
	return nil, nil
}
