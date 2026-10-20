package sim

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// This file is the naive reference scheduler (#35 oracle): a direct,
// slow transcription of LRM 14.7.5 for tiny designs in the test process
// language, with its own projected-waveform code. It shares nothing
// with the kernel but the op type.

type tinySig struct {
	name     string
	init     int64
	resolved bool // std_logic, resolved with the table
}

type tinyDesign struct {
	sigs  []tinySig
	procs [][]op
}

type rtxn struct {
	at int64
	v  int64
}

type rdriver struct {
	cur  int64
	wave []rtxn
}

type rproc struct {
	pc, cur  int
	waiting  bool
	done     bool
	on       []SigID
	until    bool
	timeout  int64      // -1: none
	drivers  []*rdriver // by signal; nil: no driver
	untilSig SigID
	untilVal int64
}

type refResult struct {
	lines  []string
	status string
}

func refProject(old []rtxn, neu []rtxn, transport bool,
	reject int64,
) []rtxn {
	t0 := neu[0].at
	var kept []rtxn
	for _, x := range old {
		if x.at < t0 {
			kept = append(kept, x)
		}
	}
	if !transport {
		marked := make([]bool, len(kept))
		for i, x := range kept {
			marked[i] = x.at < t0-reject
		}
		// Step 3, literally: repeat until nothing changes.
		for changed := true; changed; {
			changed = false
			for i := range kept {
				if marked[i] {
					continue
				}
				nextMarked, nextV := true, neu[0].v // the new transaction
				if i+1 < len(kept) {
					nextMarked, nextV = marked[i+1], kept[i+1].v
				}
				if nextMarked && kept[i].v == nextV {
					marked[i], changed = true, true
				}
			}
		}
		var out []rtxn
		for i, x := range kept {
			if marked[i] {
				out = append(out, x)
			}
		}
		kept = out
	}
	return append(kept, neu...)
}

func refResolve(vals []int64) int64 {
	if len(vals) == 1 {
		return vals[0]
	}
	r := int64(lZ)
	for _, v := range vals {
		r = tableRes(r, v)
	}
	return r
}

func runRef(d tinyDesign, timeLimit int64, deltaLimit int) refResult {
	n := len(d.sigs)
	cur := make([]int64, n)
	for i, s := range d.sigs {
		cur[i] = s.init
	}
	procs := make([]*rproc, len(d.procs))
	for i, ops := range d.procs {
		p := &rproc{timeout: -1, drivers: make([]*rdriver, n)}
		for _, o := range ops {
			if o.kind == opAssign && p.drivers[o.sig] == nil {
				p.drivers[o.sig] = &rdriver{cur: d.sigs[o.sig].init}
			}
		}
		procs[i] = p
	}
	effective := func(s int) (int64, bool) {
		var vals []int64
		for _, p := range procs {
			if dr := p.drivers[s]; dr != nil {
				vals = append(vals, dr.cur)
			}
		}
		if len(vals) == 0 {
			return 0, false
		}
		if d.sigs[s].resolved {
			return refResolve(vals), true
		}
		return vals[0], true
	}
	for s := range n {
		if d.sigs[s].resolved {
			if v, ok := effective(s); ok {
				cur[s] = v
			}
		}
	}
	var lines []string
	var batch []string
	for s := range n {
		batch = append(batch, fmt.Sprintf("0 0 %s %s", d.sigs[s].name,
			refVal(d.sigs[s], cur[s])))
	}
	slices.Sort(batch)
	lines = append(lines, batch...)
	now, delta := int64(0), 0
	exec := func(i int) error {
		p, ops := procs[i], d.procs[i]
		for range 1000 {
			if len(ops) == 0 {
				p.done = true
				return nil
			}
			o := ops[p.pc]
			p.cur = p.pc
			p.pc = (p.pc + 1) % len(ops)
			switch o.kind {
			case opAssign:
				dr := p.drivers[o.sig]
				neu := make([]rtxn, len(o.vals))
				for j := range neu {
					neu[j] = rtxn{now + o.afters[j], o.vals[j]}
				}
				rej := o.afters[0]
				if o.hasReject {
					rej = o.reject
				}
				dr.wave = refProject(dr.wave, neu, o.transport, rej)
			case opWait:
				if len(o.on) == 0 && !o.hasFor && !o.until {
					p.done = true
					return nil
				}
				p.waiting, p.on, p.until = true, o.on, o.until
				p.untilSig, p.untilVal = o.untilSig, o.untilVal
				p.timeout = -1
				if o.hasFor {
					p.timeout = now + o.dur
				}
				return nil
			}
		}
		return fmt.Errorf("loop")
	}
	for i := range procs {
		if err := exec(i); err != nil {
			return refResult{lines, "failed"}
		}
	}
	for {
		tn := int64(-1)
		for _, p := range procs {
			for _, dr := range p.drivers {
				if dr != nil && len(dr.wave) > 0 &&
					(tn < 0 || dr.wave[0].at < tn) {
					tn = dr.wave[0].at
				}
			}
			if p.waiting && p.timeout >= 0 && (tn < 0 || p.timeout < tn) {
				tn = p.timeout
			}
		}
		if tn < 0 {
			return refResult{lines, "quiet"}
		}
		if tn > now {
			if tn > timeLimit {
				return refResult{lines, "budget-exceeded"}
			}
			now, delta = tn, 0
		} else {
			if delta+1 > deltaLimit {
				return refResult{lines, "budget-exceeded"}
			}
			delta++
		}
		active := make([]bool, n)
		for _, p := range procs {
			for s, dr := range p.drivers {
				if dr != nil && len(dr.wave) > 0 && dr.wave[0].at == now {
					dr.cur = dr.wave[0].v
					dr.wave = dr.wave[1:]
					active[s] = true
				}
			}
		}
		event := make([]bool, n)
		batch = batch[:0]
		for s := range n {
			if !active[s] {
				continue
			}
			v, _ := effective(s)
			if v != cur[s] {
				cur[s], event[s] = v, true
				batch = append(batch, fmt.Sprintf("%d %d %s %s", now,
					delta, d.sigs[s].name, refVal(d.sigs[s], v)))
			}
		}
		slices.Sort(batch)
		lines = append(lines, batch...)
		var ready []int
		for i, p := range procs {
			if !p.waiting {
				continue
			}
			if p.timeout == now {
				ready = append(ready, i)
				continue
			}
			hit := false
			for _, s := range p.on {
				hit = hit || event[s]
			}
			if hit && (!p.until || cur[p.untilSig] == p.untilVal) {
				ready = append(ready, i)
			}
		}
		for _, i := range ready {
			procs[i].waiting = false
			if err := exec(i); err != nil {
				return refResult{lines, "failed"}
			}
		}
	}
}

func refVal(s tinySig, v int64) string {
	if s.resolved {
		return fmt.Sprint(v) // positions, as the rec writer shows them
	}
	return fmt.Sprint(v)
}

// genDesign generates a tiny design: integer signals each driven by
// one process, and possibly one resolved std_logic signal driven by
// several.
func genDesign(r *rng) tinyDesign {
	var d tinyDesign
	nInt := 1 + r.IntN(3)
	for i := range nInt {
		d.sigs = append(d.sigs, tinySig{name: fmt.Sprintf("tb.s%d", i),
			init: int64(r.IntN(3))})
	}
	hasRes := r.IntN(2) == 0
	if hasRes {
		d.sigs = append(d.sigs, tinySig{name: "tb.bus", init: lU,
			resolved: true})
	}
	nProc := 1 + r.IntN(3)
	owner := make([]int, nInt)
	for i := range owner {
		owner[i] = r.IntN(nProc)
	}
	for p := range nProc {
		var ops []op
		var own []SigID
		for s, o := range owner {
			if o == p {
				own = append(own, SigID(s))
			}
		}
		if hasRes && r.IntN(2) == 0 {
			own = append(own, SigID(nInt))
		}
		nOps := 1 + r.IntN(5)
		hasWait := false
		for range nOps {
			if len(own) > 0 && r.IntN(2) == 0 {
				s := own[r.IntN(len(own))]
				o := op{kind: opAssign, sig: s,
					transport: r.IntN(2) == 0}
				nw := 1 + r.IntN(2)
				at := int64(r.IntN(4))
				for range nw {
					v := int64(r.IntN(4))
					if d.sigs[s].resolved {
						v = int64(r.IntN(9))
					}
					o.vals = append(o.vals, v)
					o.afters = append(o.afters, at)
					at += 1 + int64(r.IntN(3))
				}
				if r.IntN(3) == 0 {
					o.hasReject = true
					o.reject = int64(r.IntN(int(o.afters[0]) + 1))
				}
				ops = append(ops, o)
				continue
			}
			ops = append(ops, genWait(r, len(d.sigs)))
			hasWait = true
		}
		if !hasWait {
			w := genWait(r, len(d.sigs))
			if len(w.on) == 0 && !w.hasFor {
				w.hasFor, w.dur = true, 1
			}
			ops = append(ops, w)
		}
		d.procs = append(d.procs, ops)
	}
	return d
}

func genWait(r *rng, nSig int) op {
	o := op{kind: opWait}
	for s := range nSig {
		if r.IntN(2) == 0 {
			o.on = append(o.on, SigID(s))
		}
	}
	if r.IntN(3) == 0 {
		o.until = true
		o.untilSig = SigID(r.IntN(nSig))
		o.untilVal = int64(r.IntN(4))
		if len(o.on) == 0 {
			o.on = []SigID{o.untilSig}
		}
	}
	if r.IntN(2) == 0 || len(o.on) == 0 {
		o.hasFor, o.dur = true, int64(r.IntN(6))
	}
	return o
}

func (d tinyDesign) model() *Model {
	m := &Model{Testbench: "tb"}
	var types []*eval.Type
	for _, s := range d.sigs {
		sig := Signal{Path: s.name, Type: tInt, Init: iv(s.init)}
		if s.resolved {
			sig = Signal{Path: s.name, Type: tLogic,
				Init:    eval.Value{T: tLogic, I: s.init},
				Resolve: goResolved}
		}
		types = append(types, sig.Type)
		m.AddSignal(sig)
	}
	for i, ops := range d.procs {
		var drv []Target
		for _, o := range ops {
			if o.kind == opAssign && !slices.ContainsFunc(drv,
				func(t Target) bool { return t.Sig == o.sig }) {
				drv = append(drv, Whole(o.sig))
			}
		}
		m.Processes = append(m.Processes, Process{
			Path: fmt.Sprintf("tb.p%d", i), Drivers: drv,
			Code: &prog{ops: ops, sigT: types}})
	}
	return m
}

func kernelLines(t *testing.T, m *Model, o Options) ([]string, Status) {
	t.Helper()
	r := &rec{}
	o.Writer = r
	k := mustKernel(t, m, o)
	k.RunAll(5, nil)
	for i := 1; i < len(r.changes); i++ {
		a, b := r.changes[i-1], r.changes[i]
		if b.Time < a.Time || (b.Time == a.Time && b.Delta < a.Delta) {
			t.Fatalf("time decreased: %d/%d after %d/%d", b.Time,
				b.Delta, a.Time, a.Delta)
		}
	}
	return r.lines(), k.Status()
}

// TestReferenceScheduler compares the kernel with the naive reference
// scheduler on generated tiny designs, at several seeds (a race-free
// design gives the same trace for every seed), and checks that a
// cloned kernel continues exactly like the original.
func TestReferenceScheduler(t *testing.T) {
	const timeLimit, deltaLimit = 60, 20
	r := newRNG(35)
	for i := range 3000 {
		d := genDesign(r)
		want := runRef(d, timeLimit, deltaLimit)
		for seed := range uint64(4) {
			got, st := kernelLines(t, d.model(), Options{
				Order: seedOrder(seed), TimeBudget: timeLimit,
				DeltaBudget: deltaLimit, Seed: seed})
			if !slices.Equal(got, want.lines) ||
				st.String() != want.status {
				t.Fatalf("design %d seed %d: %+v\nkernel (%v):\n%s\n"+
					"reference (%s):\n%s", i, seed, d, st,
					strings.Join(got, "\n"), want.status,
					strings.Join(want.lines, "\n"))
			}
		}
		// Fork after a few cycles: both halves equal the full run.
		rec1 := &rec{}
		k := mustKernel(t, d.model(), Options{TimeBudget: timeLimit,
			DeltaBudget: deltaLimit, Writer: rec1})
		k.Run(1 + r.IntN(6))
		c := k.Clone()
		rec2 := &rec{changes: slices.Clone(rec1.changes),
			sigs: rec1.sigs}
		c.SetWriter(rec2)
		k.RunAll(3, nil)
		c.RunAll(4, nil)
		if !slices.Equal(rec1.lines(), want.lines) ||
			!slices.Equal(rec2.lines(), want.lines) {
			t.Fatalf("design %d: clone diverged", i)
		}
	}
}
