// Package fst exports a native trace as FST, the compact waveform format
// of GTKWave that Surfer also reads (#36). The writer is pure Go and
// follows the block layout of GTKWave's fstapi.c (header, value-change
// blocks, geometry, hierarchy), so fstapi-based readers (GTKWave,
// fst2vcd) and wellen (Surfer) open its files.
//
// Byte stability: the date (Date), version string (Version) and block
// break policy (Exporter.BlockChanges) are fixed, value-change chains,
// frames, time tables and geometry are stored uncompressed, and the
// hierarchy is a gzip stream of stored (uncompressed) deflate blocks
// written by this package, so the same trace always exports to the same
// bytes on every platform and Go version. Reals are written in
// little-endian byte order; the header's endian test lets big-endian
// readers swap them.
//
// Mapping: all nine std_ulogic values are kept (U X 0 1 Z W L H -).
// VHDL type information is attached to every variable with a SUPVAR
// attribute (the VHDL type name, signal kind and data type, as
// fstWriterCreateVar2 does). Enumerations other than bit are SV enum
// variables with an enum table per type; integers are integer vectors
// of the width of their range; reals are reals; physical values are
// strings such as "10 ns" in the largest unit that divides them;
// strings (arrays of character) are strings. Arrays of std_ulogic or
// bit are vectors named with their range (q[7:0]); other arrays give one
// variable per element (a[0]...). Records are VHDL record scopes, and
// instances, blocks, generates and packages get their VHDL scope types.
// Only the value each signal settles on at each time is written
// (trace.Settle). Times are in femtoseconds (timescale 1 fs). Every
// block after the first restates the first variable's value at its
// start time (the end of the previous block), which works around a
// stale-index bug in fstapi's reader; readers see no change.
package fst

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/trace"
)

// Fixed header strings.
const (
	Version = "gohard"
	// Date is the asctime form of 2000-01-01T00:00:00Z, the default
	// simulation.env.epoch.
	Date = "Sat Jan  1 00:00:00 2000\n"
)

// DefaultBlockChanges is the block break policy: a value-change block
// is closed after the first time step that brings it to this many value
// changes.
const DefaultBlockChanges = 1 << 16

// Exporter is the FST trace.Exporter.
type Exporter struct {
	// BlockChanges overrides DefaultBlockChanges when > 0 (tests use
	// small blocks).
	BlockChanges int
}

// Name implements trace.Exporter.
func (Exporter) Name() string { return "fst" }

// Export implements trace.Exporter.
func (x Exporter) Export(t *trace.Trace) ([]byte, error) {
	n := x.BlockChanges
	if n <= 0 {
		n = DefaultBlockChanges
	}
	return export(t, min(n, 1<<20)), nil
}

// Block, scope, variable, attribute and type codes from fstapi.h.
const (
	blHdr        = 0
	blGeom       = 3
	blHier       = 4
	blVCDynAlias = 8

	stVCDScope   = 254
	stVCDUpscope = 255
	stAttrBegin  = 252

	scArchitecture = 12
	scRecord       = 15
	scBlock        = 17
	scForGenerate  = 18
	scIfGenerate   = 19
	scGenerate     = 20
	scPackage      = 21

	vtInteger = 1
	vtReal    = 3
	vtString  = 21
	vtLogic   = 23
	vtEnum    = 28

	atMisc       = 0
	mtSupVar     = 2
	mtEnumTable  = 7
	svtSignal    = 1
	sdtShift     = 10
	fileTypeVHDL = 1
)

// Supplemental VHDL data types (fstSupplementalDataType).
const (
	sdtNone = iota
	sdtBoolean
	sdtBit
	sdtBitVector
	sdtStdULogic
	sdtStdULogicVector
	sdtStdLogic
	sdtStdLogicVector
	sdtUnsigned
	sdtSigned
	sdtInteger
	sdtReal
	sdtNatural
	sdtPositive
	sdtTime
	sdtCharacter
	sdtString
)

// fvar is one FST variable: a whole signal, or element elem of an
// array.
type fvar struct {
	sig, elem int
	vt        byte
	dir       byte
	length    int // bits (one byte each), 8 for a real, 0 varying
	isReal    bool
	name      string
	scope     []string
	typeName  string
	sdt       int
	enum      int // enum table handle, 0 for none
	enc       func(v trace.Value) []byte
}

func bitLike(ty *trace.Type) bool {
	return ty.Kind == trace.Enum && len(ty.Literals) == 2 &&
		ty.Literals[0] == "'0'" && ty.Literals[1] == "'1'"
}

func isString(t *trace.Trace, ty *trace.Type) bool {
	et := t.ElemType(ty)
	return et != nil && et.Kind == trace.Enum && et.Name == "character" &&
		len(et.Literals) == 256
}

// bits is the two's-complement width that holds [low, high].
func bits(low, high int64) int {
	n := 1
	if low >= 0 {
		for n < 64 && uint64(high)>>n != 0 {
			n++
		}
		return n
	}
	for n < 64 && (low < -(int64(1)<<(n-1)) || high > int64(1)<<(n-1)-1) {
		n++
	}
	return n
}

func binary64(v uint64, width int) []byte {
	b := make([]byte, width)
	for k := range width {
		b[width-1-k] = byte('0' + v>>k&1)
	}
	return b
}

// scalar fills the encoding of a scalar type ty into v.
func scalar(v *fvar, ty *trace.Type, enums map[string]int) {
	switch {
	case ty.Kind == trace.Logic:
		v.vt, v.length = vtLogic, 1
		v.sdt = sdtStdULogic
		if ty.Name == "std_logic" {
			v.sdt = sdtStdLogic
		}
		v.enc = func(x trace.Value) []byte { return []byte{x.Logic()} }
	case bitLike(ty):
		v.vt, v.length = vtLogic, 1
		if ty.Name == "bit" {
			v.sdt = sdtBit
		}
		v.enc = func(x trace.Value) []byte {
			return []byte{byte('0' + x.Int)}
		}
	case ty.Kind == trace.Enum:
		n := bits(0, int64(len(ty.Literals)-1))
		v.vt, v.length, v.enum = vtEnum, n, enums[ty.Name]
		switch ty.Name {
		case "boolean":
			v.sdt = sdtBoolean
		case "character":
			v.sdt = sdtCharacter
		}
		v.enc = func(x trace.Value) []byte {
			return binary64(uint64(x.Int), n)
		}
	case ty.Kind == trace.Integer:
		n := bits(ty.Low, ty.High)
		v.vt, v.length, v.sdt = vtInteger, n, sdtInteger
		switch ty.Name {
		case "natural":
			v.sdt = sdtNatural
		case "positive":
			v.sdt = sdtPositive
		}
		v.enc = func(x trace.Value) []byte {
			return binary64(uint64(x.Int), n)
		}
	case ty.Kind == trace.Real:
		v.vt, v.length, v.isReal, v.sdt = vtReal, 8, true, sdtReal
		v.enc = func(x trace.Value) []byte {
			return binary.LittleEndian.AppendUint64(nil,
				math.Float64bits(x.Real))
		}
	case ty.Kind == trace.Physical:
		v.vt, v.length = vtString, 0
		if ty.Name == "time" {
			v.sdt = sdtTime
		}
		units := ty.Units
		v.enc = func(x trace.Value) []byte {
			return []byte(physical(x.Int, units))
		}
	}
}

// physical formats n base units in the largest unit that divides it.
func physical(n int64, units []trace.Unit) string {
	u := units[0]
	for i := len(units) - 1; i > 0 && n != 0; i-- {
		if n%units[i].Mult == 0 {
			u = units[i]
			break
		}
	}
	return strconv.FormatInt(n/u.Mult, 10) + " " + u.Name
}

var vectorSDT = map[string]int{
	"std_logic_vector":  sdtStdLogicVector,
	"std_ulogic_vector": sdtStdULogicVector,
	"signed":            sdtSigned,
	"unsigned":          sdtUnsigned,
	"bit_vector":        sdtBitVector,
}

var dirCodes = [...]byte{
	trace.Internal: 0, trace.In: 1, trace.Out: 2, trace.Inout: 3,
	trace.Buffer: 4, trace.Linkage: 5,
}

func vars(t *trace.Trace, enums map[string]int) []fvar {
	var out []fvar
	for i := range t.Signals {
		s := &t.Signals[i]
		parts := trace.SplitPath(s.Path)
		base := fvar{
			sig: i, elem: -1, dir: dirCodes[s.Dir],
			scope:    parts[:len(parts)-1],
			name:     ident(parts[len(parts)-1]),
			typeName: s.Type,
		}
		ty := t.SignalType(i)
		if ty.Kind != trace.Array {
			v := base
			scalar(&v, ty, enums)
			out = append(out, v)
			continue
		}
		if s.Len() == 0 {
			continue // a null array has no FST variable
		}
		et := t.ElemType(ty)
		if isString(t, ty) {
			v := base
			v.vt, v.sdt = vtString, sdtString
			v.enc = func(x trace.Value) []byte {
				b := make([]byte, len(x.Elems))
				for k, e := range x.Elems {
					b[k] = byte(e.Int)
				}
				return b
			}
			out = append(out, v)
			continue
		}
		var ev fvar
		scalar(&ev, et, enums)
		if ev.vt == vtLogic {
			v := base
			v.vt, v.sdt = vtLogic, ev.sdt
			if sdt, ok := vectorSDT[ty.Name]; ok {
				v.sdt = sdt
			}
			n := s.Len()
			v.length = n
			v.name += fmt.Sprintf("[%d:%d]", s.Left, s.Right)
			v.enc = func(x trace.Value) []byte {
				b := make([]byte, 0, n)
				for _, e := range x.Elems {
					b = append(b, ev.enc(e)...)
				}
				return b
			}
			out = append(out, v)
			continue
		}
		for k := range s.Len() {
			v := base
			scalar(&v, et, enums)
			v.typeName, v.elem = et.Name, k
			v.name += fmt.Sprintf("[%d]", s.Index(k))
			out = append(out, v)
		}
	}
	return out
}

// ident makes a name safe for fst2vcd output: bytes outside printable
// ASCII and spaces become \xHH.
func ident(s string) string {
	var b strings.Builder
	for i := range len(s) {
		if c := s[i]; c <= ' ' || c > '~' {
			fmt.Fprintf(&b, "\\x%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// enumTables returns the enum table handle of every non-bit enum type
// that a variable uses, numbered from 1 in type-name order.
func enumTables(t *trace.Trace) ([]*trace.Type, map[string]int) {
	used := map[string]bool{}
	for i := range t.Signals {
		ty := t.SignalType(i)
		if ty.Kind == trace.Array {
			if isString(t, ty) {
				continue
			}
			ty = t.ElemType(ty)
		}
		if ty.Kind == trace.Enum && !bitLike(ty) {
			used[ty.Name] = true
		}
	}
	var list []*trace.Type
	handles := map[string]int{}
	for i := range t.Types {
		if used[t.Types[i].Name] {
			list = append(list, &t.Types[i])
			handles[t.Types[i].Name] = len(list)
		}
	}
	return list, handles
}

func varint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func svarint(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 && c&0x40 == 0 || v == -1 && c&0x40 != 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

func u64(b []byte, v uint64) []byte {
	return binary.BigEndian.AppendUint64(b, v)
}

func cstr(b []byte, s string) []byte { return append(append(b, s...), 0) }

// escape is fstUtilityBinToEsc: C escapes and octal for the rest.
func escape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; c {
		case '\a', '\b', '\f', '\n', '\r', '\t', '\v':
			b.WriteByte('\\')
			b.WriteByte("abfnrtv"[strings.IndexByte("\a\b\f\n\r\t\v", c)])
		case '\'', '"', '\\', '?':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c > ' ' && c <= '~' {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "\\%03o", c)
			}
		}
	}
	return b.String()
}

var scopeCodes = [...]byte{
	trace.Instance: scArchitecture, trace.Block: scBlock,
	trace.ForGenerate: scForGenerate, trace.IfGenerate: scIfGenerate,
	trace.CaseGenerate: scGenerate, trace.Record: scRecord,
	trace.Package: scPackage,
}

// scopeCode returns the FST scope type and component of a scope path;
// a level without a Scope entry is an instance.
func scopeCode(t *trace.Trace, path string) (byte, string) {
	for _, s := range t.Scopes {
		if s.Path == path {
			return scopeCodes[s.Kind], s.Unit
		}
	}
	return scArchitecture, ""
}

// hierarchy returns the uncompressed hierarchy bytes and the scope
// count.
func hierarchy(t *trace.Trace, vs []fvar, enums []*trace.Type) (
	[]byte, int,
) {
	var b []byte
	for h, ty := range enums {
		n := bits(0, int64(len(ty.Literals)-1))
		f := []string{ty.Name, strconv.Itoa(len(ty.Literals))}
		for _, l := range ty.Literals {
			f = append(f, escape(l))
		}
		for k := range ty.Literals {
			f = append(f, string(binary64(uint64(k), n)))
		}
		b = append(b, stAttrBegin, atMisc, mtEnumTable)
		b = cstr(b, strings.Join(f, " "))
		b = varint(b, uint64(h+1))
	}
	scopes := 0
	var open []string
	for _, v := range vs {
		k := 0
		for k < len(open) && k < len(v.scope) && open[k] == v.scope[k] {
			k++
		}
		for len(open) > k {
			b = append(b, stVCDUpscope)
			open = open[:len(open)-1]
		}
		for _, p := range v.scope[k:] {
			open = append(open, p)
			code, unit := scopeCode(t, strings.Join(open, "."))
			b = append(b, stVCDScope, code)
			b = cstr(cstr(b, ident(p)), unit)
			scopes++
		}
		if v.enum > 0 {
			b = append(b, stAttrBegin, atMisc, mtEnumTable, 0)
			b = varint(b, uint64(v.enum))
		}
		b = append(b, stAttrBegin, atMisc, mtSupVar)
		b = cstr(b, v.typeName)
		b = varint(b, svtSignal<<sdtShift|uint64(v.sdt))
		b = append(b, v.vt, v.dir)
		b = cstr(b, v.name)
		b = varint(b, uint64(v.length))
		b = varint(b, 0) // no alias
	}
	for range open {
		b = append(b, stVCDUpscope)
	}
	return b, scopes
}

// gzipStored wraps data in a gzip member of stored deflate blocks.
func gzipStored(data []byte) []byte {
	b := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}
	for first := true; first || len(data) > 0; first = false {
		n := min(len(data), 0xffff)
		final := byte(0)
		if n == len(data) {
			final = 1
		}
		b = append(b, final)
		b = binary.LittleEndian.AppendUint16(b, uint16(n))
		b = binary.LittleEndian.AppendUint16(b, ^uint16(n))
		b = append(b, data[:n]...)
		data = data[n:]
	}
	return b
}

func gzipTrailer(b, data []byte) []byte {
	b = binary.LittleEndian.AppendUint32(b, crc32.ChecksumIEEE(data))
	return binary.LittleEndian.AppendUint32(b, uint32(len(data)))
}

// change is one value of one variable at a time-table index.
type change struct {
	idx int
	val []byte
}

func export(t *trace.Trace, blockChanges int) []byte {
	enumList, enums := enumTables(t)
	vs := vars(t, enums)
	bySig := make([][]int, len(t.Signals))
	for i, v := range vs {
		bySig[v.sig] = append(bySig[v.sig], i)
	}
	steps := trace.Settle(t)
	last := t.Header.End
	if len(steps) > 0 {
		last = max(last, steps[len(steps)-1].Time)
	}

	var body []byte
	sections := 0
	start, end := last, last
	if len(steps) > 0 {
		start = steps[0].Time
	}
	if len(vs) > 0 {
		// cur is the last value written for each variable; set marks
		// the variables that have one.
		cur := make([][]byte, len(vs))
		set := make([]bool, len(vs))
		for i, v := range vs {
			switch {
			case v.isReal:
				cur[i] = binary.LittleEndian.AppendUint64(nil,
					0x7ff8000000000000) // NaN
			case v.length > 0:
				cur[i] = bytes.Repeat([]byte{'x'}, v.length)
			}
		}
		first := true
		var prev trace.Time
		for len(steps) > 0 || first {
			var times []trace.Time
			chains := make([][]change, len(vs))
			var frame []byte
			for i, v := range vs {
				if v.length > 0 {
					frame = append(frame, cur[i]...)
				}
			}
			idx := 0
			if !first {
				// A later block starts at the end of the previous
				// one, with that time's values in its frame.
				times = append(times, prev)
				idx = 1
				// fstapi's reader keeps the index of the previous
				// block's last chain and, unless handle 1 has a chain,
				// can turn a handle without one into a false alias.
				// Restating handle 1's current value at the block's
				// start time changes nothing and avoids that.
				if set[0] {
					chains[0] = append(chains[0],
						change{idx: 0, val: cur[0]})
				}
			}
			n := 0
			for len(steps) > 0 && n < blockChanges {
				st := steps[0]
				steps = steps[1:]
				times = append(times, st.Time)
				for _, c := range st.Changes {
					for _, i := range bySig[c.Signal] {
						x := c.Value
						if vs[i].elem >= 0 {
							x = x.Elems[vs[i].elem]
						}
						val := vs[i].enc(x)
						if set[i] && bytes.Equal(cur[i], val) {
							continue // another element changed
						}
						cur[i], set[i] = val, true
						chains[i] = append(chains[i],
							change{idx: idx, val: val})
						n++
					}
				}
				idx++
			}
			if len(steps) == 0 && (len(times) == 0 ||
				times[len(times)-1] < last) {
				times = append(times, last)
			}
			body = append(body, block(vs, times, frame, chains)...)
			sections++
			prev = times[len(times)-1]
			end = prev
			first = false
		}
	}

	hier, scopes := hierarchy(t, vs, enumList)
	var geom []byte
	for _, v := range vs {
		switch {
		case v.isReal:
			geom = varint(geom, 0)
		case v.length == 0:
			geom = varint(geom, 0xffffffff)
		default:
			geom = varint(geom, uint64(v.length))
		}
	}

	var out []byte
	out = append(out, blHdr)
	out = u64(out, 329)
	out = u64(out, uint64(start))
	out = u64(out, uint64(end))
	out = binary.LittleEndian.AppendUint64(out,
		math.Float64bits(2.7182818284590452354))
	out = u64(out, 1<<27) // memory used by writer (fixed)
	out = u64(out, uint64(scopes))
	out = u64(out, uint64(len(vs)))
	out = u64(out, uint64(len(vs)))
	out = u64(out, uint64(sections))
	out = append(out, byte(0xf1)) // timescale 10^-15 s
	out = append(out, fixed(Version, 128)...)
	out = append(out, fixed(Date, 119)...)
	out = append(out, fileTypeVHDL)
	out = u64(out, 0) // timezero
	out = append(out, body...)

	if len(vs) > 0 {
		out = append(out, blGeom)
		out = u64(out, uint64(len(geom)+24))
		out = u64(out, uint64(len(geom)))
		out = u64(out, uint64(len(vs)))
		out = append(out, geom...)
	}

	gz := gzipTrailer(gzipStored(hier), hier)
	out = append(out, blHier)
	out = u64(out, uint64(16+len(gz)))
	out = u64(out, uint64(len(hier)))
	return append(out, gz...)
}

func fixed(s string, n int) []byte {
	b := make([]byte, n)
	copy(b, s)
	return b
}

// block encodes one value-change block (FST_BL_VCDATA_DYN_ALIAS2).
func block(vs []fvar, times []trace.Time, frame []byte,
	chains [][]change,
) []byte {
	var b []byte
	b = u64(b, uint64(times[0]))
	b = u64(b, uint64(times[len(times)-1]))
	// memory needed to traverse the chains; patched below
	memAt := len(b)
	b = u64(b, 0)
	b = varint(b, uint64(len(frame)))
	b = varint(b, uint64(len(frame))) // stored uncompressed
	b = varint(b, uint64(len(vs)))
	b = append(b, frame...)
	b = varint(b, uint64(len(vs)))
	vcStart := len(b)
	b = append(b, 'Z')
	pos := make([]int, len(vs))
	mem := 0
	for i, cs := range chains {
		if len(cs) == 0 {
			continue
		}
		data := chain(&vs[i], cs)
		pos[i] = len(b) - vcStart
		b = varint(b, 0) // not compressed
		b = append(b, data...)
		mem += len(data)
	}
	indexAt := len(b)
	zeros, prev := 0, 0
	for i := range vs {
		if pos[i] == 0 {
			zeros++
			continue
		}
		if zeros > 0 {
			b = varint(b, uint64(zeros)<<1)
			zeros = 0
		}
		b = svarint(b, int64(pos[i]-prev)<<1|1)
		prev = pos[i]
	}
	if zeros > 0 {
		b = varint(b, uint64(zeros)<<1)
	}
	b = u64(b, uint64(len(b)-indexAt))
	var tt []byte
	var tprev trace.Time
	for _, at := range times {
		tt = varint(tt, uint64(at-tprev))
		tprev = at
	}
	b = append(b, tt...)
	b = u64(b, uint64(len(tt)))
	b = u64(b, uint64(len(tt)))
	b = u64(b, uint64(len(times)))
	binary.BigEndian.PutUint64(b[memAt:], uint64(mem))

	out := []byte{blVCDynAlias}
	out = u64(out, uint64(8+len(b)))
	return append(out, b...)
}

// rcv1 codes the non-binary one-bit values (FST_RCV_STR "xzhuwl-?").
func rcv1(c byte) uint64 {
	switch c {
	case 'x', 'X':
		return 0
	case 'z', 'Z':
		return 1
	case 'h', 'H':
		return 2
	case 'u', 'U':
		return 3
	case 'w', 'W':
		return 4
	case 'l', 'L':
		return 5
	}
	return 6 // '-' and anything else
}

// chain encodes the value changes of one variable in one block.
func chain(v *fvar, cs []change) []byte {
	var b []byte
	prev := 0
	for _, c := range cs {
		d := uint64(c.idx - prev)
		prev = c.idx
		switch {
		case v.length == 0 && !v.isReal: // varying length
			b = varint(b, d<<1)
			b = varint(b, uint64(len(c.val)))
			b = append(b, c.val...)
		case v.length == 1:
			x := c.val[0]
			if x == '0' || x == '1' {
				b = varint(b, uint64(x&1)<<1|d<<2)
			} else {
				b = varint(b, 1|rcv1(x)<<1|d<<4)
			}
		default:
			binaryOnly := true
			for _, x := range c.val {
				if x != '0' && x != '1' {
					binaryOnly = false
					break
				}
			}
			if !binaryOnly {
				b = varint(b, d<<1|1)
				b = append(b, c.val...)
				continue
			}
			b = varint(b, d<<1)
			packed := make([]byte, (len(c.val)+7)/8)
			for k, x := range c.val {
				packed[k/8] |= (x & 1) << (7 - k%8)
			}
			b = append(b, packed...)
		}
	}
	return b
}
