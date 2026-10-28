// Package vcd exports a native trace as a Value Change Dump (IEEE 1364
// §18), a lossy format every waveform viewer reads (#36).
//
// The nine std_ulogic values fold to the four VCD values (Fold): U, X,
// W and - become x, 0 and L become 0, 1 and H become 1, and Z becomes
// z. Only the value each signal settles on at each time is written
// (trace.Settle), so deltas and zero-width glitches do not appear. The
// header is fixed: there is no $date and the $version is the constant
// Version, so the same trace always gives the same bytes.
//
// Mapping: std_ulogic and bit-like enumerations ('0', '1') are wires,
// arrays of them are wire vectors named with their range (q [7:0]);
// other enumerations are string variables holding the literal;
// integers and physical values are integer vectors in two's complement
// (physical values in base units); reals are real variables. An array
// of any other element type gives one variable per element (a[0],
// a[1]...). Instances and packages are module scopes, blocks and
// generates begin scopes, and record scopes are module scopes. Arrays
// with a null range have no variable.
package vcd

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// Version is the fixed $version text.
const Version = "gohard"

// Exporter is the VCD trace.Exporter.
type Exporter struct{}

// Name implements trace.Exporter.
func (Exporter) Name() string { return "vcd" }

// Export implements trace.Exporter.
func (Exporter) Export(t *trace.Trace) ([]byte, error) {
	return Export(t), nil
}

// Fold maps a std_ulogic character to its VCD value. It is total over
// trace.LogicChars; any other byte folds to x.
func Fold(c byte) byte {
	switch c {
	case '0', 'L':
		return '0'
	case '1', 'H':
		return '1'
	case 'Z':
		return 'z'
	}
	return 'x' // U, X, W, -
}

// varKind is how one VCD variable encodes its values.
type varKind uint8

const (
	wire    varKind = iota + 1 // logic or bit-like, width 1 or vector
	str                        // enum literal
	integer                    // two's complement of width bits
	real
)

// leaf is one VCD variable: a whole signal, or element elem of an
// array signal of non-wire elements.
type leaf struct {
	sig   int
	elem  int // -1 for the whole signal
	kind  varKind
	width int
	ty    *trace.Type // scalar type of the values
	logic bool        // wire from std_ulogic rather than a bit enum
	vec   bool        // wire vector
	name  string
	scope []string
	id    string
}

// bitLike reports whether an enum is bit: exactly the literals '0'
// and '1'.
func bitLike(ty *trace.Type) bool {
	return ty.Kind == trace.Enum && len(ty.Literals) == 2 &&
		ty.Literals[0] == "'0'" && ty.Literals[1] == "'1'"
}

// Bits returns the two's-complement width that holds every value of
// [low, high]: unsigned when low >= 0.
func Bits(low, high int64) int {
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

func scalarLeaf(ty *trace.Type) leaf {
	switch {
	case ty.Kind == trace.Logic:
		return leaf{kind: wire, width: 1, logic: true}
	case bitLike(ty):
		return leaf{kind: wire, width: 1}
	case ty.Kind == trace.Enum:
		return leaf{kind: str, width: 1}
	case ty.Kind == trace.Real:
		return leaf{kind: real, width: 64}
	}
	return leaf{kind: integer, width: Bits(ty.Low, ty.High)}
}

func leaves(t *trace.Trace) []leaf {
	var out []leaf
	for i := range t.Signals {
		s := &t.Signals[i]
		parts := trace.SplitPath(s.Path)
		scope, name := parts[:len(parts)-1], parts[len(parts)-1]
		ty := t.SignalType(i)
		if ty.Kind != trace.Array {
			l := scalarLeaf(ty)
			l.sig, l.elem, l.ty, l.scope = i, -1, ty, scope
			l.name = ident(name)
			out = append(out, l)
			continue
		}
		if s.Len() == 0 {
			continue // a null array has no VCD variable
		}
		et := t.ElemType(ty)
		l := scalarLeaf(et)
		if l.kind == wire {
			l.sig, l.elem, l.ty, l.scope = i, -1, et, scope
			l.width, l.vec = s.Len(), true
			l.name = fmt.Sprintf("%s [%d:%d]", ident(name), s.Left,
				s.Right)
			out = append(out, l)
			continue
		}
		for k := range s.Len() {
			e := l
			e.sig, e.elem, e.ty, e.scope = i, k, et, scope
			e.name = fmt.Sprintf("%s[%d]", ident(name), s.Index(k))
			out = append(out, e)
		}
	}
	for i := range out {
		out[i].id = ID(i)
	}
	return out
}

// ID returns the identifier code of variable n: printable ASCII from
// '!' to '~', little-endian base 94.
func ID(n int) string {
	var b []byte
	for {
		b = append(b, byte('!'+n%94))
		n /= 94
		if n == 0 {
			return string(b)
		}
		n--
	}
}

// ident makes a path component safe as a VCD reference: bytes outside
// printable ASCII and spaces become \xHH.
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

func scopeType(t *trace.Trace, path string) string {
	for _, s := range t.Scopes {
		if s.Path == path {
			switch s.Kind {
			case trace.Block, trace.ForGenerate, trace.IfGenerate,
				trace.CaseGenerate:
				return "begin"
			}
		}
	}
	return "module"
}

// Export returns the VCD text of t.
func Export(t *trace.Trace) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "$comment\n  gohard trace export: testbench %s"+
		"\n$end\n", ident(t.Header.Testbench))
	b.WriteString("$version\n  " + Version + "\n$end\n")
	b.WriteString("$timescale\n  1 fs\n$end\n")
	ls := leaves(t)
	var open []string
	for _, l := range ls {
		k := 0
		for k < len(open) && k < len(l.scope) && open[k] == l.scope[k] {
			k++
		}
		for len(open) > k {
			b.WriteString("$upscope $end\n")
			open = open[:len(open)-1]
		}
		for _, p := range l.scope[k:] {
			open = append(open, p)
			fmt.Fprintf(&b, "$scope %s %s $end\n",
				scopeType(t, strings.Join(open, ".")), ident(p))
		}
		typ := map[varKind]string{wire: "wire", str: "string",
			integer: "integer", real: "real"}[l.kind]
		fmt.Fprintf(&b, "$var %s %d %s %s $end\n", typ, l.width, l.id,
			l.name)
	}
	for range open {
		b.WriteString("$upscope $end\n")
	}
	b.WriteString("$enddefinitions $end\n")

	bySig := make([][]int, len(t.Signals))
	for i, l := range ls {
		bySig[l.sig] = append(bySig[l.sig], i)
	}
	steps := trace.Settle(t)
	last := make([]string, len(ls))
	var lastTime trace.Time
	for n, st := range steps {
		fmt.Fprintf(&b, "#%d\n", st.Time)
		if n == 0 {
			b.WriteString("$dumpvars\n")
		}
		for _, c := range st.Changes {
			for _, i := range bySig[c.Signal] {
				// An array of variables writes only the elements
				// that changed.
				line := value(&ls[i], c.Value)
				if line != last[i] {
					b.WriteString(line)
					last[i] = line
				}
			}
		}
		if n == 0 {
			b.WriteString("$end\n")
		}
		lastTime = st.Time
	}
	if len(steps) == 0 || t.Header.End > lastTime {
		fmt.Fprintf(&b, "#%d\n", t.Header.End)
	}
	return b.Bytes()
}

// value returns the value-change line of l for v.
func value(l *leaf, v trace.Value) string {
	if l.elem >= 0 {
		v = v.Elems[l.elem]
	}
	var b strings.Builder
	switch l.kind {
	case wire:
		if !l.vec {
			b.WriteByte(wireChar(l, v))
			b.WriteString(l.id + "\n")
			return b.String()
		}
		b.WriteByte('b')
		for _, e := range v.Elems {
			b.WriteByte(wireChar(l, e))
		}
	case str:
		b.WriteString("s" + ident(l.ty.Literals[v.Int]))
	case integer:
		b.WriteByte('b')
		for k := l.width - 1; k >= 0; k-- {
			b.WriteByte(byte('0' + uint64(v.Int)>>k&1))
		}
	case real:
		b.WriteString("r" + strconv.FormatFloat(v.Real, 'g', -1, 64))
	}
	b.WriteString(" " + l.id + "\n")
	return b.String()
}

func wireChar(l *leaf, v trace.Value) byte {
	if l.logic {
		return Fold(v.Logic())
	}
	return byte('0' + v.Int) // bit: '0' is position 0
}
