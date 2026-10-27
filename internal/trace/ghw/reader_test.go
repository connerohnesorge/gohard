package ghw

// A small GHW reader written from the reader side of GHDL's libghw.c
// (ghw_open, ghw_read_str, ghw_read_type, ghw_read_wk_types,
// ghw_read_hie, ghw_read_snapshot, ghw_read_cycle_*, ghw_read_directory,
// ghw_read_tailer), stricter than libghw where the writer promises more:
// sorted unique strings, an exact string size, every scalar signal
// number used once, and a directory that matches the sections. It is
// used to check the export against its source trace (roundtrip_test.go)
// and, through dump, against GHDL's own ghwdump (ghwdump_test.go).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// rtikI64 is GHW's 64-bit integer kind, which the writer does not use.
const rtikI64 = 26

type rrange struct {
	kind     byte
	downto   bool
	lo, hi   int64
	flo, fhi float64
}

type rtype struct {
	kind     byte
	name     string // "" is anonymous
	lits     []string
	wkt      byte
	units    [][2]any // name, int64
	base     int
	rng      rrange
	elem     int
	dims     []int
	fields   []field
	nscalars int
}

type rval struct {
	i int64
	f float64
}

type rhie struct {
	kind     byte
	name     string
	iterType int
	iter     rval
	typ      int
	sigs     []int
	kids     []*rhie
}

type rcycle struct {
	time int64
	nums []int
	vals []rval
}

type rsection struct {
	name string
	pos  int
}

type ghwFile struct {
	strs     []string // strs[0] is the anonymous name
	types    []rtype  // by id - 1
	top      []*rhie
	nscopes  int
	ndecls   int
	nbasic   int
	sigType  []int // scalar signal number -> base type id
	snapTime int64
	snap     []rval // by scalar signal number
	cycles   []rcycle
	sections []rsection // as read
	dir      []rsection // as the directory lists them
	dirPos   int
}

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) fail(format string, a ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("offset %d: "+format, append([]any{r.off}, a...)...)
	}
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || r.off+n > len(r.b) {
		r.fail("short file")
		return make([]byte, max(n, 0))
	}
	b := r.b[r.off : r.off+n]
	r.off += n
	return b
}

func (r *reader) u8() byte { return r.take(1)[0] }

func (r *reader) i32() int32 { return int32(binary.LittleEndian.Uint32(r.take(4))) }

func (r *reader) i64() int64 { return int64(binary.LittleEndian.Uint64(r.take(8))) }

func (r *reader) f64() float64 { return math.Float64frombits(binary.LittleEndian.Uint64(r.take(8))) }

func (r *reader) uleb() uint64 {
	var v uint64
	for sh := 0; ; sh += 7 {
		c := r.u8()
		if r.err != nil {
			return 0
		}
		if sh > 63 {
			r.fail("uleb128 too long")
			return 0
		}
		v |= uint64(c&0x7f) << sh
		if c&0x80 == 0 {
			return v
		}
	}
}

// sleb is ghw_read_lsleb128; values written by the 32-bit writer read
// the same.
func (r *reader) sleb() int64 {
	var v int64
	sh := 0
	for {
		c := r.u8()
		if r.err != nil {
			return 0
		}
		if sh > 63 {
			r.fail("sleb128 too long")
			return 0
		}
		v |= int64(c&0x7f) << sh
		sh += 7
		if c&0x80 == 0 {
			if c&0x40 != 0 && sh < 64 {
				v |= -1 << sh
			}
			return v
		}
	}
}

func (r *reader) sleb32() int64 {
	v := r.sleb()
	if v < math.MinInt32 || v > math.MaxInt32 {
		r.fail("sleb128 %d beyond 32 bits", v)
	}
	return v
}

func (r *reader) tag(want string) {
	if got := string(r.take(4)); got != want+"\x00" {
		r.fail("got %q, want %q", got, want)
	}
}

func (r *reader) zeros() {
	if !bytes.Equal(r.take(4), []byte{0, 0, 0, 0}) {
		r.fail("section header not zero")
	}
}

func (r *reader) order() {
	if !bytes.Equal(r.take(4), []byte{1, 4, 1, 0}) {
		r.fail("bad size and byte order block")
	}
}

func (r *reader) section(f *ghwFile, want string) {
	f.sections = append(f.sections, rsection{want, r.off})
	r.tag(want)
}

func (r *reader) strid(f *ghwFile) string {
	id := r.uleb()
	if id >= uint64(len(f.strs)) {
		r.fail("string id %d of %d", id, len(f.strs))
		return ""
	}
	return f.strs[id]
}

func (r *reader) typeid(f *ghwFile) int {
	id := r.uleb()
	if id < 1 || id > uint64(len(f.types)) {
		r.fail("type id %d of %d", id, len(f.types))
		return 1
	}
	return int(id)
}

func (f *ghwFile) typ(id int) *rtype { return &f.types[id-1] }

// baseOf is ghw_get_base_type.
func (f *ghwFile) baseOf(id int) int {
	switch t := f.typ(id); t.kind {
	case rtikSubScalar, rtikSubArray:
		return t.base
	}
	return id
}

// scalars is get_nbr_elements; -1 for unbounded types.
func (f *ghwFile) scalars(id int) int {
	switch t := f.typ(id); t.kind {
	case rtikB2, rtikE8, rtikI32, rtikI64, rtikF64, rtikP64,
		rtikSubScalar:
		return 1
	case rtikSubArray, rtikRecord:
		return t.nscalars
	}
	return -1
}

func (r *reader) rng() rrange {
	k := r.u8()
	g := rrange{kind: k & 0x7f, downto: k&0x80 != 0}
	switch g.kind {
	case rtikB2, rtikE8:
		g.lo, g.hi = int64(r.u8()), int64(r.u8())
	case rtikI32:
		g.lo, g.hi = r.sleb32(), r.sleb32()
	case rtikI64, rtikP64:
		g.lo, g.hi = r.sleb(), r.sleb()
	case rtikF64:
		g.flo, g.fhi = r.f64(), r.f64()
	default:
		r.fail("range kind %d", g.kind)
	}
	return g
}

func rangeLen(g rrange) int {
	n := g.hi - g.lo
	if g.downto {
		n = -n
	}
	if n < 0 {
		return 0
	}
	return int(n) + 1
}

func (r *reader) value(f *ghwFile, id int) rval {
	switch f.typ(f.baseOf(id)).kind {
	case rtikB2, rtikE8:
		return rval{i: int64(r.u8())}
	case rtikI32:
		return rval{i: r.sleb32()}
	case rtikI64, rtikP64:
		return rval{i: r.sleb()}
	case rtikF64:
		return rval{f: r.f64()}
	}
	r.fail("value of type kind %d", f.typ(id).kind)
	return rval{}
}

func parseGHW(data []byte) (*ghwFile, error) {
	r := &reader{b: data}
	f := &ghwFile{}
	if !bytes.Equal(r.take(16),
		[]byte("GHDLwave\n\x10\x00\x01\x01\x04\x01\x00")) {
		return nil, errors.New("bad file header")
	}
	r.readStrings(f)
	r.readTypes(f)
	r.readWellKnown(f)
	r.readHier(f)
	r.section(f, "EOH")
	r.readValues(f)
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(data) {
		return nil, fmt.Errorf("%d bytes after the tailer",
			len(data)-r.off)
	}
	if len(f.dir) != len(f.sections)-1 {
		return nil, fmt.Errorf("directory lists %d sections, file has %d",
			len(f.dir), len(f.sections)-1)
	}
	for i, s := range f.dir {
		if s != f.sections[i] {
			return nil, fmt.Errorf("directory entry %d is %v, section "+
				"is %v", i, s, f.sections[i])
		}
	}
	if d := f.sections[len(f.sections)-2]; d.name != "DIR" ||
		d.pos != f.dirPos {
		return nil, fmt.Errorf("tailer points at %d, directory at %v",
			f.dirPos, d)
	}
	return f, nil
}

func (r *reader) readStrings(f *ghwFile) {
	r.section(f, "STR")
	r.zeros()
	n, size := int(r.i32()), int(r.i32())
	if n < 0 || n > len(r.b) {
		r.fail("%d strings", n)
		return
	}
	f.strs = []string{""}
	total := 0
	var c byte
	prevLen := 0
	for i := 1; i <= n && r.err == nil; i++ {
		prev := f.strs[i-1]
		if prevLen > len(prev) {
			r.fail("string %d shares %d bytes of %q", i, prevLen, prev)
			return
		}
		s := []byte(prev[:prevLen])
		for {
			c = r.u8()
			if r.err != nil || c < 32 || c >= 128 && c < 160 {
				break
			}
			s = append(s, c)
		}
		if i > 1 && string(s) <= prev {
			r.fail("strings not sorted: %q after %q", s, prev)
		}
		f.strs = append(f.strs, string(s))
		total += len(s)
		prevLen = int(c & 0x1f)
		for sh := 5; c >= 128; sh += 5 {
			c = r.u8()
			prevLen |= int(c&0x1f) << sh
		}
	}
	if n > 0 && (c != 0 || prevLen != 0) {
		r.fail("last string ends with %d", c)
	}
	if total != size {
		r.fail("string size %d, header says %d", total, size)
	}
	r.tag("EOS")
}

func (r *reader) readTypes(f *ghwFile) {
	r.section(f, "TYP")
	r.zeros()
	n := int(r.i32())
	for i := 0; i < n && r.err == nil; i++ {
		t := rtype{kind: r.u8()}
		t.name = r.strid(f)
		switch t.kind {
		case rtikB2, rtikE8:
			for range r.uleb() {
				t.lits = append(t.lits, r.strid(f))
			}
		case rtikI32, rtikI64, rtikF64:
		case rtikP64:
			for range r.uleb() {
				t.units = append(t.units, [2]any{r.strid(f), r.sleb()})
			}
		case rtikSubScalar:
			t.base = r.typeid(f)
			t.rng = r.rng()
			if f.typ(t.base).kind != t.rng.kind &&
				!(f.typ(t.base).kind == rtikB2 && t.rng.kind == rtikE8) {
				r.fail("subtype %s: range kind %d of base kind %d",
					t.name, t.rng.kind, f.typ(t.base).kind)
			}
		case rtikArray:
			t.elem = r.typeid(f)
			for range r.uleb() {
				t.dims = append(t.dims, r.typeid(f))
			}
		case rtikSubArray:
			t.base = r.typeid(f)
			b := f.typ(t.base)
			if b.kind != rtikArray || len(b.dims) != 1 {
				r.fail("array subtype of kind %d", b.kind)
				break
			}
			t.rng = r.rng()
			if t.rng.kind != rtikI32 {
				r.fail("array range kind %d", t.rng.kind)
			}
			if f.scalars(b.elem) < 0 {
				r.fail("unbounded element")
			}
			t.nscalars = rangeLen(t.rng) * f.scalars(b.elem)
		case rtikRecord:
			for range r.uleb() {
				fl := field{name: r.strid(f), typ: r.typeid(f)}
				if f.scalars(fl.typ) < 0 {
					r.fail("unbounded field")
				}
				t.nscalars += max(f.scalars(fl.typ), 0)
				t.fields = append(t.fields, fl)
			}
		default:
			r.fail("type kind %d", t.kind)
		}
		f.types = append(f.types, t)
	}
	if r.u8() != 0 {
		r.fail("types not ended by 0")
	}
}

func (r *reader) readWellKnown(f *ghwFile) {
	r.section(f, "WKT")
	r.zeros()
	for r.err == nil {
		k := r.u8()
		if k == 0 {
			return
		}
		id := r.typeid(f)
		if t := f.typ(id); t.kind == rtikB2 || t.kind == rtikE8 {
			t.wkt = k
		}
	}
}

func (r *reader) readHier(f *ghwFile) {
	r.section(f, "HIE")
	r.zeros()
	f.nscopes, f.ndecls = int(r.i32()), int(r.i32())
	f.nbasic = int(r.i32())
	if f.nbasic < 0 || f.nbasic > len(r.b) {
		r.fail("%d signals", f.nbasic)
		return
	}
	f.sigType = make([]int, f.nbasic+1)
	var stack []*rhie
	add := func(h *rhie) {
		if len(stack) == 0 {
			f.top = append(f.top, h)
		} else {
			p := stack[len(stack)-1]
			p.kids = append(p.kids, h)
		}
	}
	for r.err == nil {
		k := r.u8()
		switch k {
		case 0:
			if len(stack) != 0 {
				r.fail("hierarchy ends inside a scope")
			}
			return
		case hieEOS:
			if len(stack) == 0 {
				r.fail("end of scope at the top")
				return
			}
			stack = stack[:len(stack)-1]
			continue
		}
		h := &rhie{kind: k, name: r.strid(f)}
		switch k {
		case hieProcess:
			add(h)
		case hieBlock, hieIfGen, hieForGen, hieInstance, hiePackage:
			if k == hieForGen {
				h.iterType = r.typeid(f)
				h.iter = r.value(f, h.iterType)
			}
			add(h)
			stack = append(stack, h)
		case hieSignal, hiePortIn, hiePortOut, hiePortInout, hiePortBuf,
			hiePortLink:
			h.typ = r.typeid(f)
			r.signalNumbers(f, h.typ, &h.sigs)
			add(h)
		default:
			r.fail("hierarchy kind %d", k)
		}
	}
}

// signalNumbers is ghw_read_signal.
func (r *reader) signalNumbers(f *ghwFile, id int, sigs *[]int) {
	if r.err != nil {
		return
	}
	switch t := f.typ(id); t.kind {
	case rtikB2, rtikE8, rtikSubScalar:
		n := r.uleb()
		if n == 0 || n > uint64(f.nbasic) {
			r.fail("signal number %d of %d", n, f.nbasic)
			return
		}
		if f.sigType[n] != 0 {
			r.fail("signal number %d used twice", n)
		}
		f.sigType[n] = f.baseOf(id)
		*sigs = append(*sigs, int(n))
	case rtikSubArray:
		el := f.typ(t.base).elem
		for range rangeLen(t.rng) {
			r.signalNumbers(f, el, sigs)
		}
	case rtikRecord:
		for _, fl := range t.fields {
			r.signalNumbers(f, fl.typ, sigs)
		}
	default:
		r.fail("signal of type kind %d", t.kind)
	}
}

func (r *reader) readValues(f *ghwFile) {
	for i := 1; i <= f.nbasic; i++ {
		if f.sigType[i] == 0 {
			r.fail("signal number %d unused", i)
			return
		}
	}
	r.section(f, "SNP")
	r.zeros()
	f.snapTime = r.i64()
	f.snap = make([]rval, f.nbasic+1)
	for i := 1; i <= f.nbasic && r.err == nil; i++ {
		f.snap[i] = r.value(f, f.sigType[i])
	}
	r.tag("ESN")
	if bytes.HasPrefix(r.b[r.off:], []byte("CYC\x00")) {
		r.section(f, "CYC")
		at := r.i64()
		for r.err == nil {
			c := rcycle{time: at}
			n := 0
			for r.err == nil {
				d := r.uleb()
				if d == 0 {
					break
				}
				n += int(d)
				if n > f.nbasic {
					r.fail("signal number %d of %d", n, f.nbasic)
					return
				}
				c.nums = append(c.nums, n)
				c.vals = append(c.vals, r.value(f, f.sigType[n]))
			}
			f.cycles = append(f.cycles, c)
			d := r.sleb()
			if d == -1 {
				break
			}
			if d < 0 {
				r.fail("time goes back by %d", -d)
			}
			at += d
		}
		r.tag("ECY")
	}
	f.sections = append(f.sections, rsection{"DIR", r.off})
	r.tag("DIR")
	r.order()
	n := int(r.i32())
	for i := 0; i < n && r.err == nil; i++ {
		name := r.take(4)
		f.dir = append(f.dir, rsection{
			strings.TrimRight(string(name), "\x00"), int(r.i32())})
	}
	r.tag("EOD")
	f.sections = append(f.sections, rsection{"TAI", r.off})
	r.tag("TAI")
	r.order()
	f.dirPos = int(r.i32())
}
