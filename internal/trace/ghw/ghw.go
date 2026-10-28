// Package ghw exports a native trace as GHW, the waveform format of GHDL
// (#60). Unlike VCD and FST it keeps VHDL types: enumerations with their
// literals, all nine std_ulogic values, integer and physical ranges,
// arrays with their index range, and records. The writer is pure Go and
// follows GHDL's writer (grt-waves.adb) and reader (libghw.c), so
// GHDL's ghwdump, GTKWave and Surfer open its files.
//
// Byte stability: the file has no date, version string or host field;
// strings are sorted, types are numbered in trace order and the
// hierarchy is written in name order, so the same trace always exports
// to the same bytes on every platform (P1). Words and reals are
// little-endian, as the header says.
//
// Types: std_ulogic and its subtypes are subtypes of an enumeration
// std_ulogic ('U' to '-') marked as the well-known std_ulogic type;
// other enumerations are enumeration types (one byte per value, two
// literals as GHDL's b2 kind; boolean and bit are marked well-known);
// an integer type is a subtype of a base integer type of the same name,
// 32-bit when its range fits and otherwise a 64-bit physical type with
// no units (ghwdump cannot print, and Surfer cannot read, GHW's 64-bit
// integer kind; the value encoding is the same); a physical type is a
// subtype of a 64-bit physical type with its units; a real type is a
// subtype of a float type over the whole float64 range (the trace keeps
// no real range). An array type is a one-dimensional array type indexed
// by an integer subtype over the whole 32-bit range (the trace's integer
// type when it has that range, else an anonymous subtype of a 32-bit
// base type named integer), and each array signal has an
// anonymous subtype with its range. A record scope whose fields are
// signals or records becomes one signal of a record type named after
// the scope's unit, with the fields in path order; when its fields have
// different modes (a mode view) the signal is an inout port. A record
// scope that holds anything else is a block.
//
// Hierarchy: instances (and levels without a scope entry), blocks,
// if-generates and packages keep their kind; a case-generate is an
// if-generate, the only generate GHW has without an iterator. A
// for-generate level named label(N), N an integer, is a for-generate
// with that iterator value; any other for-generate level is a block
// with the full name. Processes are listed by name. Within a scope the
// signals come first, then the scopes and processes, each by name.
// The design unit of an instance, the modeled mark, delta cycles and
// events are not in GHW.
//
// Values: the snapshot at time 0 holds every scalar's value after the
// last delta of time 0; a signal that has no value at time 0 shows the
// first value of its type (position 0, the low bound, or 0.0) until its
// first change. Then one cycle per time at which a settled value
// changes (trace.Settle), and an empty cycle at the end time when that
// is later. Times are in femtoseconds, GHW's unit.
package ghw

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Exporter is the GHW trace.Exporter.
type Exporter struct{}

// Name implements trace.Exporter.
func (Exporter) Name() string { return "ghw" }

// Export implements trace.Exporter.
func (Exporter) Export(t *trace.Trace) ([]byte, error) {
	return Export(t)
}

// Type kinds (enum ghdl_rtik in libghw.h).
const (
	rtikB2         = 22
	rtikE8         = 23
	rtikI32        = 25
	rtikF64        = 27
	rtikP64        = 29
	rtikArray      = 31
	rtikRecord     = 32
	rtikSubScalar  = 34
	rtikSubArray   = 35
	rangeDowntoBit = 0x80
)

// Hierarchy kinds (enum ghw_hie_kind).
const (
	hieBlock     = 3
	hieIfGen     = 4
	hieForGen    = 5
	hieInstance  = 6
	hiePackage   = 7
	hieProcess   = 13
	hieEOS       = 15
	hieSignal    = 16
	hiePortIn    = 17
	hiePortOut   = 18
	hiePortInout = 19
	hiePortBuf   = 20
	hiePortLink  = 21
)

// Well-known types (enum ghw_wkt_type).
const (
	wktBoolean   = 1
	wktBit       = 2
	wktStdULogic = 3
)

var dirKinds = [...]byte{
	trace.Internal: hieSignal, trace.In: hiePortIn, trace.Out: hiePortOut,
	trace.Inout: hiePortInout, trace.Buffer: hiePortBuf,
	trace.Linkage: hiePortLink,
}

var scopeKinds = [...]byte{
	trace.Instance: hieInstance, trace.Block: hieBlock,
	trace.ForGenerate: hieForGen, trace.IfGenerate: hieIfGen,
	trace.CaseGenerate: hieIfGen, trace.Record: hieBlock,
	trace.Package: hiePackage,
}

// gtype is one entry of the GHW type table; its id is its one-based
// position.
type gtype struct {
	kind   byte
	name   string // "" is anonymous
	lits   []string
	units  []trace.Unit
	wkt    byte
	base   int // subtypes: the base type id
	rkind  byte
	lo, hi int64   // range of an integer, enum or physical subtype
	downto bool    // array subtypes
	elem   int     // arrays: element type id
	dim    int     // arrays: index type id
	fields []field // records
}

type field struct {
	name string
	typ  int
}

// hnode is one hierarchy entry as written.
type hnode struct {
	kind  byte
	name  string
	iter  int64 // for-generate iterator value
	typ   int   // signals: type id
	first int   // signals: first scalar signal number
	count int   // signals: scalar count
	kids  []*hnode
}

// basic is one scalar signal of the GHW file: element elem of trace
// signal sig (elem is -1 for a scalar trace signal).
type basic struct {
	sig, elem int
	enc       byte
}

// tnode is one level of the trace hierarchy.
type tnode struct {
	name   string
	scope  *trace.Scope
	inner  map[string]*tnode // lookup only; kids is the order
	kids   []*tnode
	sigs   []tleaf
	procs  []string
	record bool // written as a record signal
}

type tleaf struct {
	name string
	sig  int
}

type writer struct {
	t      *trace.Trace
	types  []gtype
	sig    map[string]int  // trace type -> type id of its signals
	enc    map[string]byte // trace type -> value encoding
	index  int             // the index type of arrays and generates
	intB   int             // the 32-bit base type named integer
	dedup  map[string]int
	strs   map[string]bool
	list   []string // the strings, in first-use order
	basics []basic
	first  []int // trace signal -> first scalar number
	scopes int
	decls  int
	err    error
}

// Export writes t as GHW.
func Export(t *trace.Trace) ([]byte, error) {
	w := &writer{t: t, sig: map[string]int{}, enc: map[string]byte{},
		dedup: map[string]int{}, strs: map[string]bool{},
		first: make([]int, len(t.Signals))}
	w.declareTypes()
	top := w.hierarchy()
	if w.err != nil {
		return nil, w.err
	}
	return w.write(top)
}

func (w *writer) fail(format string, a ...any) {
	if w.err == nil {
		w.err = fmt.Errorf("ghw: "+format, a...)
	}
}

// str records s for the string table. GHW strings end at the first
// byte in 0..31 or 128..159, so names holding one cannot be written.
func (w *writer) str(s string) {
	for i := range len(s) {
		if c := s[i]; c < 32 || c >= 128 && c < 160 {
			w.fail("name %q has a byte GHW cannot hold", s)
		}
	}
	if s != "" && !w.strs[s] {
		w.strs[s] = true
		w.list = append(w.list, s)
	}
}

func (w *writer) add(g gtype) int {
	w.str(g.name)
	for _, l := range g.lits {
		w.str(l)
	}
	for _, u := range g.units {
		w.str(u.Name)
	}
	for _, f := range g.fields {
		w.str(f.name)
	}
	w.types = append(w.types, g)
	return len(w.types)
}

func fits32(v int64) bool { return v >= math.MinInt32 && v <= math.MaxInt32 }

// logicLits are the std_ulogic literals as VHDL writes them.
var logicLits = func() []string {
	l := make([]string, len(trace.LogicChars))
	for i := range l {
		l[i] = "'" + trace.LogicChars[i:i+1] + "'"
	}
	return l
}()

// declareTypes adds every trace type: the scalars in name order, then
// the arrays.
func (w *writer) declareTypes() {
	ulogic := 0
	for i := range w.t.Types {
		ty := &w.t.Types[i]
		switch ty.Kind {
		case trace.Logic:
			if ulogic == 0 {
				ulogic = w.add(gtype{kind: rtikE8, name: "std_ulogic",
					lits: logicLits, wkt: wktStdULogic})
			}
			w.sig[ty.Name] = w.add(gtype{kind: rtikSubScalar,
				name: ty.Name, base: ulogic, rkind: rtikE8,
				hi: int64(len(logicLits) - 1)})
			w.enc[ty.Name] = rtikE8
		case trace.Enum:
			if len(ty.Literals) > 256 {
				w.fail("enumeration %s has %d literals; GHW holds 256",
					ty.Name, len(ty.Literals))
				continue
			}
			g := gtype{kind: rtikE8, name: ty.Name, lits: ty.Literals}
			if len(ty.Literals) == 2 {
				g.kind = rtikB2
				switch {
				case ty.Name == "boolean" && ty.Literals[0] == "false" &&
					ty.Literals[1] == "true":
					g.wkt = wktBoolean
				case ty.Name == "bit" && ty.Literals[0] == "'0'" &&
					ty.Literals[1] == "'1'":
					g.wkt = wktBit
				}
			}
			w.sig[ty.Name] = w.add(g)
			w.enc[ty.Name] = g.kind
		case trace.Integer:
			// GHW's 64-bit integer kind is read neither by ghwdump
			// (values) nor by Surfer, while its 64-bit physical kind
			// is, with the same value encoding.
			k := byte(rtikP64)
			if fits32(ty.Low) && fits32(ty.High) {
				k = rtikI32
			}
			b := w.add(gtype{kind: k, name: ty.Name})
			if k == rtikI32 && ty.Name == "integer" {
				w.intB = b
			}
			w.sig[ty.Name] = w.add(gtype{kind: rtikSubScalar,
				name: ty.Name, base: b, rkind: k, lo: ty.Low, hi: ty.High})
			w.enc[ty.Name] = k
		case trace.Physical:
			b := w.add(gtype{kind: rtikP64, name: ty.Name,
				units: ty.Units})
			w.sig[ty.Name] = w.add(gtype{kind: rtikSubScalar,
				name: ty.Name, base: b, rkind: rtikP64, lo: ty.Low,
				hi: ty.High})
			w.enc[ty.Name] = rtikP64
		case trace.Real:
			b := w.add(gtype{kind: rtikF64, name: ty.Name})
			w.sig[ty.Name] = w.add(gtype{kind: rtikSubScalar,
				name: ty.Name, base: b, rkind: rtikF64})
			w.enc[ty.Name] = rtikF64
		}
	}
	for i := range w.t.Types {
		ty := &w.t.Types[i]
		if ty.Kind != trace.Array {
			continue
		}
		w.sig[ty.Name] = w.add(gtype{kind: rtikArray, name: ty.Name,
			elem: w.sig[ty.Elem], dim: w.indexType()})
		w.enc[ty.Name] = w.enc[ty.Elem]
	}
}

// indexType returns the type that indexes arrays and for-generates: a
// subtype over the whole 32-bit range, as Surfer reads an array of
// std_ulogic as a vector only when its index type has a range. It is
// the trace's integer type when that has this range, else an anonymous
// subtype of the 32-bit integer base type.
func (w *writer) indexType() int {
	if w.index != 0 {
		return w.index
	}
	ty, ok := w.t.Type("integer")
	if ok && ty.Kind == trace.Integer && ty.Low == math.MinInt32 &&
		ty.High == math.MaxInt32 {
		w.index = w.sig["integer"]
		return w.index
	}
	if w.intB == 0 {
		w.intB = w.add(gtype{kind: rtikI32, name: "integer"})
	}
	w.index = w.add(gtype{kind: rtikSubScalar, base: w.intB,
		rkind: rtikI32, lo: math.MinInt32, hi: math.MaxInt32})
	return w.index
}

// signalType returns the type id of trace signal i.
func (w *writer) signalType(i int) int {
	s := &w.t.Signals[i]
	if w.t.SignalType(i).Kind != trace.Array {
		return w.sig[s.Type]
	}
	if !fits32(s.Left) || !fits32(s.Right) {
		w.fail("array %s has bounds beyond 32 bits", s.Path)
	}
	key := fmt.Sprintf("a %d %d %d %t", w.sig[s.Type], s.Left, s.Right,
		s.Downto)
	if id, ok := w.dedup[key]; ok {
		return id
	}
	id := w.add(gtype{kind: rtikSubArray, base: w.sig[s.Type],
		rkind: rtikI32, lo: s.Left, hi: s.Right, downto: s.Downto})
	w.dedup[key] = id
	return id
}

// tree builds the trace hierarchy from the scopes, signals and
// processes.
func (w *writer) tree() *tnode {
	root := &tnode{inner: map[string]*tnode{}}
	get := func(parts []string) *tnode {
		n := root
		for _, p := range parts {
			c := n.inner[p]
			if c == nil {
				c = &tnode{name: p, inner: map[string]*tnode{}}
				n.inner[p] = c
				n.kids = append(n.kids, c)
			}
			n = c
		}
		return n
	}
	for i := range w.t.Scopes {
		get(trace.SplitPath(w.t.Scopes[i].Path)).scope = &w.t.Scopes[i]
	}
	for i := range w.t.Signals {
		parts := trace.SplitPath(w.t.Signals[i].Path)
		n := get(parts[:len(parts)-1])
		n.sigs = append(n.sigs, tleaf{name: parts[len(parts)-1], sig: i})
	}
	for _, p := range w.t.Processes {
		parts := trace.SplitPath(p.Path)
		n := get(parts[:len(parts)-1])
		n.procs = append(n.procs, parts[len(parts)-1])
	}
	markRecords(root)
	return root
}

// markRecords marks the record scopes that become record signals: those
// that hold only signals and such records, at least one signal in all.
func markRecords(n *tnode) bool {
	all := true
	for _, c := range n.kids {
		if !markRecords(c) {
			all = false
		}
	}
	n.record = n.scope != nil && n.scope.Kind == trace.Record && all &&
		len(n.procs) == 0 && (len(n.sigs) > 0 || len(n.inner) > 0)
	return n.record
}

// member is a signal or record child of a node, in name order.
type member struct {
	name string
	sig  int    // -1 for a record
	rec  *tnode // the record
}

func members(n *tnode) []member {
	var ms []member
	for _, l := range n.sigs {
		ms = append(ms, member{name: l.name, sig: l.sig})
	}
	for _, c := range n.kids {
		if c.record {
			ms = append(ms, member{name: c.name, sig: -1, rec: c})
		}
	}
	slices.SortStableFunc(ms, func(a, b member) int {
		return strings.Compare(a.name, b.name)
	})
	return ms
}

// hierarchy plans the HIE section: it numbers the scalar signals in
// write order and adds the array subtypes and record types.
func (w *writer) hierarchy() *hnode {
	top := &hnode{}
	w.block(w.tree(), top)
	return top
}

// block fills h with the entries of trace level n.
func (w *writer) block(n *tnode, h *hnode) {
	for _, m := range members(n) {
		w.decls++
		w.str(m.name)
		if m.rec == nil {
			s := &w.t.Signals[m.sig]
			e := &hnode{kind: dirKinds[s.Dir], name: m.name,
				typ: w.signalType(m.sig), first: len(w.basics) + 1}
			w.leaves(m.sig)
			e.count = len(w.basics) + 1 - e.first
			h.kids = append(h.kids, e)
			continue
		}
		typ, dir := w.recordType(m.rec)
		e := &hnode{kind: dirKinds[dir], name: m.name, typ: typ,
			first: len(w.basics) + 1}
		w.recordLeaves(m.rec)
		e.count = len(w.basics) + 1 - e.first
		h.kids = append(h.kids, e)
	}
	type sub struct {
		name string
		n    *tnode // nil for a process
	}
	var subs []sub
	for _, c := range n.kids {
		if !c.record {
			subs = append(subs, sub{c.name, c})
		}
	}
	for _, p := range n.procs {
		subs = append(subs, sub{name: p})
	}
	slices.SortStableFunc(subs, func(a, b sub) int {
		return cmp.Or(strings.Compare(a.name, b.name),
			cmp.Compare(boolInt(a.n == nil), boolInt(b.n == nil)))
	})
	for _, s := range subs {
		if s.n == nil {
			w.str(s.name)
			h.kids = append(h.kids, &hnode{kind: hieProcess,
				name: s.name})
			continue
		}
		w.scopes++
		e := &hnode{kind: hieInstance, name: s.name}
		if s.n.scope != nil {
			e.kind = scopeKinds[s.n.scope.Kind]
		}
		if e.kind == hieForGen {
			if base, v, ok := forIndex(s.name); ok {
				e.name, e.iter = base, v
				w.indexType()
			} else {
				e.kind = hieBlock
			}
		}
		w.str(e.name)
		w.block(s.n, e)
		h.kids = append(h.kids, e)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// forIndex splits a for-generate level name label(N) whose N is a
// 32-bit integer in its canonical decimal form.
func forIndex(name string) (string, int64, bool) {
	i := strings.LastIndexByte(name, '(')
	if i <= 0 || !strings.HasSuffix(name, ")") {
		return "", 0, false
	}
	v, err := strconv.ParseInt(name[i+1:len(name)-1], 10, 32)
	if err != nil || strconv.FormatInt(v, 10) != name[i+1:len(name)-1] {
		return "", 0, false
	}
	return name[:i], v, true
}

// leaves numbers the scalars of trace signal i.
func (w *writer) leaves(i int) {
	w.first[i] = len(w.basics) + 1
	enc := w.enc[w.t.Signals[i].Type]
	if w.t.SignalType(i).Kind != trace.Array {
		w.basics = append(w.basics, basic{sig: i, elem: -1, enc: enc})
		return
	}
	for k := range w.t.Signals[i].Len() {
		w.basics = append(w.basics, basic{sig: i, elem: k, enc: enc})
	}
}

func (w *writer) recordLeaves(n *tnode) {
	for _, m := range members(n) {
		if m.rec == nil {
			w.leaves(m.sig)
		} else {
			w.recordLeaves(m.rec)
		}
	}
}

// recordType returns the record type of record level n and the mode of
// its signal: the fields' common mode, or inout when they differ.
func (w *writer) recordType(n *tnode) (int, trace.Dir) {
	var fs []field
	var dir trace.Dir
	mixed := false
	for _, m := range members(n) {
		var typ int
		var d trace.Dir
		if m.rec == nil {
			typ, d = w.signalType(m.sig), w.t.Signals[m.sig].Dir
		} else {
			typ, d = w.recordType(m.rec)
		}
		if dir == 0 {
			dir = d
		} else if d != dir {
			mixed = true
		}
		fs = append(fs, field{name: m.name, typ: typ})
	}
	if mixed {
		dir = trace.Inout
	}
	key := "r " + n.scope.Unit
	for _, f := range fs {
		key += fmt.Sprintf(" %q %d", f.name, f.typ)
	}
	if id, ok := w.dedup[key]; ok {
		return id, dir
	}
	id := w.add(gtype{kind: rtikRecord, name: n.scope.Unit, fields: fs})
	w.dedup[key] = id
	return id, dir
}

// out is the byte sink with GHW's encodings.
type out struct {
	b    []byte
	dir  []section
	strs map[string]int
}

type section struct {
	name string
	pos  int
}

func (o *out) section(name string) {
	o.dir = append(o.dir, section{name, len(o.b)})
	o.raw(name)
}

func (o *out) raw(s string) { o.b = append(append(o.b, s...), 0) }

func (o *out) byte(c byte) { o.b = append(o.b, c) }

func (o *out) i32(v int32) {
	o.b = binary.LittleEndian.AppendUint32(o.b, uint32(v))
}

func (o *out) i64(v int64) {
	o.b = binary.LittleEndian.AppendUint64(o.b, uint64(v))
}

func (o *out) f64(v float64) {
	o.b = binary.LittleEndian.AppendUint64(o.b, math.Float64bits(v))
}

func (o *out) uleb(v uint64) {
	for v >= 0x80 {
		o.b = append(o.b, byte(v)|0x80)
		v >>= 7
	}
	o.b = append(o.b, byte(v))
}

func (o *out) sleb(v int64) {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 && c&0x40 == 0 || v == -1 && c&0x40 != 0 {
			o.b = append(o.b, c)
			return
		}
		o.b = append(o.b, c|0x80)
	}
}

func (o *out) strid(s string) { o.uleb(uint64(o.strs[s])) }

func (o *out) zeros() { o.b = append(o.b, 0, 0, 0, 0) }

// order is the size and byte order block of the header, directory and
// tailer: little-endian, 4-byte words, 1-byte file offsets.
func (o *out) order() { o.b = append(o.b, 1, 4, 1, 0) }

func (w *writer) write(top *hnode) ([]byte, error) {
	o := &out{strs: map[string]int{}}
	o.b = append(o.b, "GHDLwave\n"...)
	o.b = append(o.b, 16, 0, 1) // header length, version 0.1
	o.order()
	w.writeStrings(o)
	w.writeTypes(o)
	w.writeWellKnown(o)
	o.section("HIE")
	o.zeros()
	o.i32(int32(w.scopes))
	o.i32(int32(w.decls))
	o.i32(int32(len(w.basics)))
	for _, h := range top.kids {
		writeHier(o, h, w.index)
	}
	o.byte(0)
	o.section("EOH")
	if err := w.writeValues(o); err != nil {
		return nil, err
	}
	pos := len(o.b)
	o.section("DIR")
	o.order()
	o.i32(int32(len(o.dir)))
	for _, s := range o.dir {
		o.b = append(o.b, s.name...)
		o.byte(0)
		o.i32(int32(s.pos))
	}
	o.raw("EOD")
	o.section("TAI")
	o.order()
	o.i32(int32(pos))
	if pos > math.MaxInt32 {
		return nil, fmt.Errorf("ghw: export of %d bytes is over the "+
			"2 GiB GHW offsets hold", len(o.b))
	}
	return o.b, nil
}

// writeStrings writes the sorted string table, each string stored as
// the length of the prefix it shares with the previous one and the
// rest.
func (w *writer) writeStrings(o *out) {
	strs := slices.Sorted(slices.Values(w.list))
	total := 0
	for i, s := range strs {
		o.strs[s] = i + 1
		total += len(s)
	}
	o.section("STR")
	o.zeros()
	o.i32(int32(len(strs)))
	o.i32(int32(total))
	for i, s := range strs {
		k := 0
		if i > 0 {
			prev := strs[i-1]
			for k < len(prev) && k < len(s) && prev[k] == s[k] {
				k++
			}
			for n := k; ; {
				c := byte(n % 32)
				n /= 32
				if n == 0 {
					o.byte(c)
					break
				}
				o.byte(c | 0x80)
			}
		}
		o.b = append(o.b, s[k:]...)
	}
	if len(strs) > 0 {
		o.byte(0) // the last string's end
	}
	o.raw("EOS")
}

func (o *out) rng(kind byte, lo, hi int64, downto bool) {
	k := kind
	if downto {
		k |= rangeDowntoBit
	}
	o.byte(k)
	switch kind {
	case rtikB2, rtikE8:
		o.byte(byte(lo))
		o.byte(byte(hi))
	case rtikF64:
		o.f64(-math.MaxFloat64)
		o.f64(math.MaxFloat64)
	default:
		o.sleb(lo)
		o.sleb(hi)
	}
}

func (w *writer) writeTypes(o *out) {
	o.section("TYP")
	o.zeros()
	o.i32(int32(len(w.types)))
	for _, g := range w.types {
		o.byte(g.kind)
		o.strid(g.name)
		switch g.kind {
		case rtikB2, rtikE8:
			o.uleb(uint64(len(g.lits)))
			for _, l := range g.lits {
				o.strid(l)
			}
		case rtikP64:
			o.uleb(uint64(len(g.units)))
			for _, u := range g.units {
				o.strid(u.Name)
				o.sleb(u.Mult)
			}
		case rtikSubScalar:
			o.uleb(uint64(g.base))
			o.rng(g.rkind, g.lo, g.hi, false)
		case rtikArray:
			o.uleb(uint64(g.elem))
			o.uleb(1)
			o.uleb(uint64(g.dim))
		case rtikSubArray:
			o.uleb(uint64(g.base))
			o.rng(rtikI32, g.lo, g.hi, g.downto)
		case rtikRecord:
			o.uleb(uint64(len(g.fields)))
			for _, f := range g.fields {
				o.strid(f.name)
				o.uleb(uint64(f.typ))
			}
		}
	}
	o.byte(0)
}

func (w *writer) writeWellKnown(o *out) {
	o.section("WKT")
	o.zeros()
	for i, g := range w.types {
		if g.wkt != 0 {
			o.byte(g.wkt)
			o.uleb(uint64(i + 1))
		}
	}
	o.byte(0)
}

func writeHier(o *out, h *hnode, index int) {
	o.byte(h.kind)
	o.strid(h.name)
	switch h.kind {
	case hieProcess:
		return
	case hieSignal, hiePortIn, hiePortOut, hiePortInout, hiePortBuf,
		hiePortLink:
		o.uleb(uint64(h.typ))
		for k := range h.count {
			o.uleb(uint64(h.first + k))
		}
		return
	case hieForGen:
		o.uleb(uint64(index))
		o.sleb(h.iter)
	}
	for _, k := range h.kids {
		writeHier(o, k, index)
	}
	o.byte(hieEOS)
}

// initial is the value a scalar shows before its first change.
func (w *writer) initial(b basic) trace.Value {
	ty := w.t.SignalType(b.sig)
	if b.elem >= 0 {
		ty = w.t.ElemType(ty)
	}
	if ty.Kind == trace.Integer || ty.Kind == trace.Physical {
		return trace.Int(ty.Low)
	}
	return trace.Value{}
}

func (o *out) value(enc byte, v trace.Value) {
	switch enc {
	case rtikB2, rtikE8:
		o.byte(byte(v.Int))
	case rtikF64:
		o.f64(v.Real)
	default:
		o.sleb(v.Int)
	}
}

// writeValues writes the snapshot at time 0 and the cycle section.
func (w *writer) writeValues(o *out) error {
	cur := make([]trace.Value, len(w.basics)+1)
	for i, b := range w.basics {
		cur[i+1] = w.initial(b)
	}
	type chg struct {
		num int
		v   trace.Value
	}
	var chs []chg
	apply := func(st trace.Step) {
		chs = chs[:0]
		for _, s := range st.Changes {
			n := w.first[s.Signal]
			if w.t.SignalType(s.Signal).Kind != trace.Array {
				if !cur[n].Equal(s.Value) {
					chs = append(chs, chg{n, s.Value})
				}
				continue
			}
			for k, e := range s.Value.Elems {
				if !cur[n+k].Equal(e) {
					chs = append(chs, chg{n + k, e})
				}
			}
		}
		slices.SortFunc(chs, func(a, b chg) int {
			return cmp.Compare(a.num, b.num)
		})
		for _, c := range chs {
			cur[c.num] = c.v
		}
	}
	steps := trace.Settle(w.t)
	if len(steps) > 0 && steps[0].Time == 0 {
		apply(steps[0])
		steps = steps[1:]
	}
	o.section("SNP")
	o.zeros()
	o.i64(0)
	for i, b := range w.basics {
		o.value(b.enc, cur[i+1])
	}
	o.raw("ESN")
	last := trace.Time(0)
	end := w.t.Header.End
	if end > math.MaxInt64 {
		return fmt.Errorf("ghw: end time %d is beyond 64-bit signed "+
			"femtoseconds", end)
	}
	if len(steps) == 0 && end == 0 {
		return nil
	}
	o.section("CYC")
	cycle := func(at trace.Time, first bool) {
		if first {
			o.i64(int64(at))
		} else {
			o.sleb(int64(at - last))
		}
		last = at
		prev := 0
		for _, c := range chs {
			o.uleb(uint64(c.num - prev))
			prev = c.num
			o.value(w.basics[c.num-1].enc, c.v)
		}
		o.byte(0)
	}
	for i, st := range steps {
		apply(st)
		cycle(st.Time, i == 0)
	}
	if len(steps) == 0 || end > last {
		chs = chs[:0]
		cycle(end, len(steps) == 0)
	}
	o.sleb(-1)
	o.raw("ECY")
	return nil
}
