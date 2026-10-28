package ieeenative

// numeric.go: ieee.numeric_std. The arithmetic and comparison bodies
// take only operands whose elements are all 0, 1, L or H (TO_01 maps L
// and H to 0 and 1) and not null; on the others the package body
// warns or returns its metavalue result, so they are declined. A
// vector's leftmost element is its most significant bit whatever its
// direction (the body aliases it as L'length-1 downto 0).

import "github.com/connerohnesorge/gohard/v2/internal/eval"

const libNumeric = "numeric_std"

func init() {
	for _, op := range []string{"+", "-"} {
		sub := op == "-"
		add(libNumeric, op, cUns, arith(sub, cUns, cUns), cUns, cUns)
		add(libNumeric, op, cSgn, arith(sub, cSgn, cSgn), cSgn, cSgn)
		add(libNumeric, op, cUns, arith(sub, cUns, cInt), cUns, cInt)
		add(libNumeric, op, cUns, arith(sub, cInt, cUns), cInt, cUns)
		add(libNumeric, op, cSgn, arith(sub, cSgn, cInt), cSgn, cInt)
		add(libNumeric, op, cSgn, arith(sub, cInt, cSgn), cInt, cSgn)
	}
	for _, op := range []string{"=", "/=", "<", "<=", ">", ">="} {
		for _, ps := range [][2]class{{cUns, cUns}, {cSgn, cSgn},
			{cUns, cInt}, {cInt, cUns}, {cSgn, cInt}, {cInt, cSgn}} {
			add(libNumeric, op, cBool, compare(op, ps[0], ps[1]), ps[0],
				ps[1])
		}
	}
	add(libNumeric, "resize", cUns, resizeBody(false), cUns, cInt)
	add(libNumeric, "resize", cSgn, resizeBody(true), cSgn, cInt)
	add(libNumeric, "to_integer", cInt, toInteger(false), cUns)
	add(libNumeric, "to_integer", cInt, toInteger(true), cSgn)
	add(libNumeric, "to_unsigned", cUns, toVector(false), cInt, cInt)
	add(libNumeric, "to_signed", cSgn, toVector(true), cInt, cInt)
	add(libNumeric, "to_01", cUns, numericTo01, cUns, cLogic)
	add(libNumeric, "to_01", cSgn, numericTo01, cSgn, cLogic)
}

// bits returns the elements of v as bits, most significant first;
// ok false when v is null or an element is not 0, 1, L or H.
func bits(v eval.Value) ([]byte, bool) {
	if len(v.E) == 0 {
		return nil, false
	}
	out := make([]byte, len(v.E))
	for i, e := range v.E {
		p, ok := to01(e.I)
		if !ok {
			return nil, false
		}
		out[i] = byte(p - l0)
	}
	return out, true
}

// extend returns b (signed: two's complement) widened to n >= len(b)
// bits.
func extend(b []byte, signed bool, n int) []byte {
	if len(b) == n {
		return b
	}
	out := make([]byte, n)
	var fill byte
	if signed && len(b) > 0 {
		fill = b[0]
	}
	pad := n - len(b)
	for i := range pad {
		out[i] = fill
	}
	copy(out[pad:], b)
	return out
}

// intBits returns i as n-bit two's complement; ok false when it does
// not fit (unsigned: also when negative).
func intBits(i int64, n int, signed bool) ([]byte, bool) {
	if !signed && i < 0 {
		return nil, false
	}
	if n < 64 {
		if signed {
			lim := int64(1) << (n - 1)
			if i < -lim || i >= lim {
				return nil, false
			}
		} else if i >= int64(1)<<n {
			return nil, false
		}
	}
	out := make([]byte, n)
	u := uint64(i)
	for k := range n {
		bit := byte(0)
		if k < 64 {
			bit = byte(u >> k & 1)
		} else if i < 0 {
			bit = 1
		}
		out[n-1-k] = bit
	}
	return out, true
}

// operand is one argument of an arithmetic or comparison body as bits.
type operand struct {
	b      []byte
	signed bool
	isInt  bool
	i      int64
}

func operandOf(v eval.Value, c class) (operand, bool) {
	if c == cInt {
		return operand{isInt: true, i: v.I}, true
	}
	b, ok := bits(v)
	return operand{b: b, signed: c == cSgn}, ok
}

// arith is "+" or "-" (sub) on operands of classes a and b. Two
// vectors are resized to the longer one; an integer is converted to
// the vector's length (TO_UNSIGNED, TO_SIGNED), declined when it does
// not fit (the conversion warns). The result is indexed size-1 downto
// 0.
func arith(sub bool, a, b class) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		x, ok1 := operandOf(args[0], a)
		y, ok2 := operandOf(args[1], b)
		if !ok1 || !ok2 {
			return eval.Value{}, false
		}
		signed := a == cSgn || b == cSgn
		var size int
		switch {
		case x.isInt:
			size = len(y.b)
			if x.b, ok1 = intBits(x.i, size, signed); !ok1 {
				return eval.Value{}, false
			}
		case y.isInt:
			size = len(x.b)
			if y.b, ok2 = intBits(y.i, size, signed); !ok2 {
				return eval.Value{}, false
			}
		default:
			size = max(len(x.b), len(y.b))
		}
		l, r := extend(x.b, signed, size), extend(y.b, signed, size)
		pos := make([]int64, size)
		carry := byte(0)
		if sub {
			carry = 1 // L + not R + 1
		}
		for k := size - 1; k >= 0; k-- {
			rb := r[k]
			if sub {
				rb ^= 1
			}
			s := l[k] + rb + carry
			pos[k], carry = l0+int64(s&1), s>>1
		}
		return vector(rt, downto(size), pos), true
	}
}

// cmpBits compares x and y as numbers: -1, 0 or 1.
func cmpBits(x, y operand) int {
	n := max(len(x.b), len(y.b)) + 1
	if x.isInt || y.isInt {
		n = max(n, 65)
	}
	xb, yb := x.wide(n), y.wide(n)
	// Two's complement of n bits, both in range: the sign bit decides,
	// then the magnitude bits from the top.
	if xb[0] != yb[0] {
		if xb[0] == 1 {
			return -1
		}
		return 1
	}
	for k := 1; k < n; k++ {
		if xb[k] != yb[k] {
			if xb[k] < yb[k] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// wide returns o as n-bit two's complement (n is larger than its
// length, so an unsigned vector stays non-negative).
func (o operand) wide(n int) []byte {
	if o.isInt {
		b, _ := intBits(o.i, n, true)
		return b
	}
	return extend(o.b, o.signed, n)
}

// compare is a relational operator on operands of classes a and b: the
// numeric comparison, which every overload computes on 0/1 operands
// (an integer too wide for the vector included).
func compare(op string, a, b class) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		x, ok1 := operandOf(args[0], a)
		y, ok2 := operandOf(args[1], b)
		if !ok1 || !ok2 {
			return eval.Value{}, false
		}
		c := cmpBits(x, y)
		var t bool
		switch op {
		case "=":
			t = c == 0
		case "/=":
			t = c != 0
		case "<":
			t = c < 0
		case "<=":
			t = c <= 0
		case ">":
			t = c > 0
		default:
			t = c >= 0
		}
		return boolean(rt, t)
	}
}

// resizeBody is RESIZE (ARG; NEW_SIZE : NATURAL), indexed NEW_SIZE-1
// downto 0. The elements are copied as they are, metavalues included:
// UNSIGNED keeps the low bits and pads with '0'; SIGNED fills with
// ARG's leftmost element and keeps the low MINIMUM(lengths)-1 bits.
// A null ARG or result is declined.
func resizeBody(signed bool) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		arg, n := args[0].E, args[1].I
		if n < 1 || len(arg) == 0 || n > int64(len(arg))+1<<20 {
			return eval.Value{}, false
		}
		pos := make([]int64, n)
		for i := range pos {
			pos[i] = l0
		}
		keep := min(int64(len(arg)), n)
		if signed {
			for i := range pos {
				pos[i] = arg[0].I
			}
			keep = min(int64(len(arg)), n) - 1
		}
		for k := range keep {
			pos[n-1-k] = arg[int64(len(arg))-1-k].I
		}
		for _, p := range pos {
			if p < 0 || p > lDash {
				return eval.Value{}, false
			}
		}
		return vector(rt, downto(int(n)), pos), true
	}
}

// toInteger is TO_INTEGER of a 0/1 vector, declined when the value is
// outside the result subtype (the body fails there).
func toInteger(signed bool) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		b, ok := bits(args[0])
		if !ok {
			return eval.Value{}, false
		}
		neg := signed && b[0] == 1
		// Significant bits past the 63 of an int64 do not fit.
		for k := 0; k < len(b)-63; k++ {
			if (b[k] == 1) != neg {
				return eval.Value{}, false
			}
		}
		var u uint64
		for _, x := range b {
			u = u<<1 | uint64(x)
		}
		v := int64(u)
		if neg && len(b) < 64 {
			v -= int64(1) << len(b)
		} else if !neg && len(b) >= 64 && v < 0 {
			return eval.Value{}, false
		}
		if !rt.Range.Contains(v) {
			return eval.Value{}, false
		}
		return eval.Int(rt, v), true
	}
}

// toVector is TO_UNSIGNED and TO_SIGNED (ARG; SIZE : NATURAL),
// indexed SIZE-1 downto 0; declined for a null result or a value that
// does not fit (the body warns "vector truncated").
func toVector(signed bool) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		n := args[1].I
		if n < 1 || n > 1<<20 {
			return eval.Value{}, false
		}
		b, ok := intBits(args[0].I, int(n), signed)
		if !ok {
			return eval.Value{}, false
		}
		pos := make([]int64, n)
		for i, x := range b {
			pos[i] = l0 + int64(x)
		}
		return vector(rt, downto(int(n)), pos), true
	}
}

// numericTo01 is TO_01 (S : UNSIGNED or SIGNED; XMAP), the
// std_logic_1164 TO_01 of the elements; a null S (the body warns) is
// declined.
func numericTo01(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	if len(args[0].E) == 0 {
		return eval.Value{}, false
	}
	return to01Vector(args, rt)
}
