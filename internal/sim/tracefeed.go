package sim

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// traceSig is one recorded trace signal: a scalar or one-dimensional
// array signal, or one field of a record signal (record signals are
// recorded per field below a record scope, #36).
type traceSig struct {
	sig   SigID
	first int // leaf offset in the signal
	n     int
	tmpl  eval.Value
	logic bool // element or scalar type is std_ulogic-like
	real  bool
}

// typeTable names the trace types of one run; names are unique.
type typeTable struct {
	types []trace.Type
}

func (tt *typeTable) find(name string) (int, bool) {
	for i := range tt.types {
		if tt.types[i].Name == name {
			return i, true
		}
	}
	return 0, false
}

// add returns the name of the trace type for t (an equal definition
// reuses a name; a clash gets a numeric suffix).
func (tt *typeTable) add(t trace.Type) string {
	base := t.Name
	for n := 1; ; n++ {
		if n > 1 {
			t.Name = base + "_" + strconv.Itoa(n)
		}
		i, ok := tt.find(t.Name)
		if !ok {
			tt.types = append(tt.types, t)
			return t.Name
		}
		if sameTraceType(tt.types[i], t) {
			return t.Name
		}
	}
}

func sameTraceType(a, b trace.Type) bool {
	if a.Kind != b.Kind || a.Low != b.Low || a.High != b.High ||
		a.Elem != b.Elem || len(a.Literals) != len(b.Literals) ||
		len(a.Units) != len(b.Units) {
		return false
	}
	for i := range a.Literals {
		if a.Literals[i] != b.Literals[i] {
			return false
		}
	}
	for i := range a.Units {
		if a.Units[i] != b.Units[i] {
			return false
		}
	}
	return true
}

func typeName(t *eval.Type) string {
	n := strings.ToLower(t.Name)
	if n == "" {
		n = "anonymous"
	}
	return n
}

func isLogic(t *eval.Type) bool {
	lits := t.BaseType().Literals
	if t.Kind != eval.KindEnum || len(lits) != len(trace.LogicChars) {
		return false
	}
	for i, l := range lits {
		if l != "'"+string(trace.LogicChars[i])+"'" {
			return false
		}
	}
	return true
}

// scalarType returns the trace type of a scalar subtype.
func (tt *typeTable) scalarType(t *eval.Type) (string, bool) {
	tr := trace.Type{Name: typeName(t)}
	switch t.Kind {
	case eval.KindEnum:
		if isLogic(t) {
			tr.Kind = trace.Logic
			break
		}
		tr.Kind = trace.Enum
		tr.Literals = t.BaseType().Literals
	case eval.KindInteger:
		tr.Kind, tr.Low, tr.High = trace.Integer, t.Range.Low(),
			t.Range.High()
	case eval.KindFloating:
		tr.Kind = trace.Real
	case eval.KindPhysical:
		tr.Kind, tr.Low, tr.High = trace.Physical, t.Range.Low(),
			t.Range.High()
		for _, u := range t.BaseType().Units {
			tr.Units = append(tr.Units, trace.Unit{Name: u.Name,
				Mult: u.Factor})
		}
	default:
		return "", false
	}
	return tt.add(tr), true
}

// beginTrace declares the design to the writer.
func (k *Kernel) beginTrace() error {
	w := k.opts.Writer
	if w == nil {
		return nil
	}
	var tt typeTable
	var d trace.Design
	d.Scopes = append(d.Scopes, k.m.Scopes...)
	for i := range k.m.Signals {
		s := &k.m.Signals[i]
		if s.Implicit != ImplicitNone {
			continue
		}
		dir := s.Dir
		if dir == 0 {
			dir = trace.Internal
		}
		k.declare(&tt, &d, SigID(i), s.Path, s.Init, 0, dir)
	}
	d.Types = tt.types
	for _, p := range k.m.Processes {
		d.Processes = append(d.Processes, trace.Process{Path: p.Path,
			Modeled: p.Modeled})
	}
	return w.Begin(trace.Header{Testbench: k.m.Testbench,
		Seed: k.opts.Seed}, d)
}

// declare adds the trace signals of the value shape v at leaf offset
// first of signal s. Shapes the trace cannot hold (arrays of composites,
// multi-dimensional arrays) are not recorded.
func (k *Kernel) declare(tt *typeTable, d *trace.Design, s SigID,
	path string, v eval.Value, first int, dir trace.Dir,
) {
	t := v.T
	if t == nil {
		return
	}
	switch t.Kind {
	case eval.KindRecord:
		d.Scopes = append(d.Scopes, trace.Scope{Path: path,
			Kind: trace.Record, Unit: typeName(t)})
		off := first
		for i, e := range v.E {
			name := "f" + strconv.Itoa(i)
			if i < len(t.BaseType().Fields) {
				name = t.BaseType().Fields[i].Name
			} else if i < len(t.Fields) {
				name = t.Fields[i].Name
			}
			k.declare(tt, d, s, path+"."+name, e, off, dir)
			off += leaves(e)
		}
		return
	case eval.KindArray:
		if len(v.B) != 1 || t.Elem == nil || !t.Elem.Kind.IsScalar() {
			return
		}
		elem, ok := tt.scalarType(t.Elem)
		if !ok {
			return
		}
		name := tt.add(trace.Type{Name: typeName(t), Kind: trace.Array,
			Elem: elem})
		b := v.B[0]
		if ld := k.m.Signals[s].LeafDirs; first < len(ld) &&
			ld[first] != 0 {
			dir = ld[first]
		}
		d.Signals = append(d.Signals, trace.Signal{Path: path,
			Type: name, Dir: dir, Left: b.Left, Right: b.Right,
			Downto: b.Dir == eval.Downto})
		k.addTraceSig(s, first, len(v.E), v, isLogic(t.Elem),
			t.Elem.Kind == eval.KindFloating)
		return
	}
	name, ok := tt.scalarType(t)
	if !ok {
		return
	}
	if ld := k.m.Signals[s].LeafDirs; first < len(ld) && ld[first] != 0 {
		dir = ld[first]
	}
	d.Signals = append(d.Signals, trace.Signal{Path: path, Type: name,
		Dir: dir})
	k.addTraceSig(s, first, 1, v, isLogic(t), t.Kind == eval.KindFloating)
}

func (k *Kernel) addTraceSig(s SigID, first, n int, tmpl eval.Value,
	logic, real bool,
) {
	k.sigs[s].traces = append(k.sigs[s].traces, int32(len(k.traceSigs)))
	k.traceSigs = append(k.traceSigs, traceSig{sig: s, first: first,
		n: n, tmpl: tmpl, logic: logic, real: real})
}

// traceValue converts the current value of trace signal ts.
func (k *Kernel) traceValue(ts *traceSig) trace.Value {
	nets := k.sigs[ts.sig].nets[ts.first : ts.first+ts.n]
	conv := func(v eval.Value) trace.Value {
		if ts.real {
			return trace.Float(v.F)
		}
		return trace.Int(v.I)
	}
	if ts.tmpl.T != nil && ts.tmpl.T.Kind == eval.KindArray {
		e := make([]trace.Value, len(nets))
		for i, ni := range nets {
			e[i] = conv(k.nets[ni].cur)
		}
		return trace.Value{Elems: e}
	}
	return conv(k.nets[nets[0]].cur)
}

func (k *Kernel) emitInitial() {
	w := k.opts.Writer
	if w == nil {
		return
	}
	for i := range k.traceSigs {
		ts := &k.traceSigs[i]
		err := w.Change(trace.Change{Signal: i, Value: k.traceValue(ts),
			Modeled: k.m.Signals[ts.sig].Modeled})
		if err != nil {
			k.fail(err)
			return
		}
	}
}

// emitChanges writes the trace changes of the signals with an event in
// this cycle, attributed to the lowest-numbered process whose driver
// was applied (a choice independent of the seed).
func (k *Kernel) emitChanges(changed []int32) {
	w := k.opts.Writer
	if w == nil {
		return
	}
	for _, s := range changed {
		for _, ti := range k.sigs[s].traces {
			ts := &k.traceSigs[ti]
			proc := int32(-1)
			for _, ni := range k.sigs[s].nets[ts.first : ts.first+ts.n] {
				n := &k.nets[ni]
				if n.eventCyc == k.cyc && n.actProc >= 0 &&
					(proc < 0 || n.actProc < proc) {
					proc = n.actProc
				}
			}
			modeled := k.m.Signals[s].Modeled
			if proc >= 0 && k.m.Processes[proc].Modeled {
				modeled = true
			}
			err := w.Change(trace.Change{Time: k.now, Delta: k.delta,
				Signal: int(ti), Value: k.traceValue(ts),
				Process: int(proc) + 1, Modeled: modeled})
			if err != nil {
				k.fail(err)
				return
			}
		}
	}
}
