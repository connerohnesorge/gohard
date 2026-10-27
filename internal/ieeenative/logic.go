package ieeenative

// logic.go: ieee.std_logic_1164. The tables are the package body's
// constants, row and column in STD_ULOGIC position order (U X 0 1 Z W
// L H -).

import "github.com/connerohnesorge/gohard/internal/eval"

type table [9][9]int64

var resolutionTable = table{
	{lU, lU, lU, lU, lU, lU, lU, lU, lU},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
	{lU, lX, l0, lX, l0, l0, l0, l0, lX},
	{lU, lX, lX, l1, l1, l1, l1, l1, lX},
	{lU, lX, l0, l1, lZ, lW, lL, lH, lX},
	{lU, lX, l0, l1, lW, lW, lW, lW, lX},
	{lU, lX, l0, l1, lL, lW, lL, lW, lX},
	{lU, lX, l0, l1, lH, lW, lW, lH, lX},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
}

var andTable = table{
	{lU, lU, l0, lU, lU, lU, l0, lU, lU},
	{lU, lX, l0, lX, lX, lX, l0, lX, lX},
	{l0, l0, l0, l0, l0, l0, l0, l0, l0},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{lU, lX, l0, lX, lX, lX, l0, lX, lX},
	{lU, lX, l0, lX, lX, lX, l0, lX, lX},
	{l0, l0, l0, l0, l0, l0, l0, l0, l0},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{lU, lX, l0, lX, lX, lX, l0, lX, lX},
}

var orTable = table{
	{lU, lU, lU, l1, lU, lU, lU, l1, lU},
	{lU, lX, lX, l1, lX, lX, lX, l1, lX},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{l1, l1, l1, l1, l1, l1, l1, l1, l1},
	{lU, lX, lX, l1, lX, lX, lX, l1, lX},
	{lU, lX, lX, l1, lX, lX, lX, l1, lX},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{l1, l1, l1, l1, l1, l1, l1, l1, l1},
	{lU, lX, lX, l1, lX, lX, lX, l1, lX},
}

var xorTable = table{
	{lU, lU, lU, lU, lU, lU, lU, lU, lU},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{lU, lX, l1, l0, lX, lX, l1, l0, lX},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
	{lU, lX, l0, l1, lX, lX, l0, l1, lX},
	{lU, lX, l1, l0, lX, lX, l1, l0, lX},
	{lU, lX, lX, lX, lX, lX, lX, lX, lX},
}

var notTable = [9]int64{lU, lX, l1, l0, lX, lX, l1, l0, lX}

// The strength strippers' tables (cvt_to_x01, cvt_to_x01z,
// cvt_to_ux01).
var (
	toX01  = [9]int64{lX, lX, l0, l1, lX, lX, l0, l1, lX}
	toX01Z = [9]int64{lX, lX, l0, l1, lZ, lX, l0, l1, lX}
	toUX01 = [9]int64{lU, lX, l0, l1, lX, lX, l0, l1, lX}
)

// logicPos returns the STD_ULOGIC position of v; ok false outside
// 0..8.
func logicPos(v eval.Value) (int64, bool) {
	return v.I, v.I >= 0 && v.I < 9
}

// binop is a two-operand logical operator: its table and whether the
// result is negated (nand, nor, xnor).
type binop struct {
	t   *table
	neg bool
}

func (o binop) apply(a, b int64) int64 {
	r := o.t[a][b]
	if o.neg {
		r = notTable[r]
	}
	return r
}

var binops = []struct {
	name string
	op   binop
}{
	{"and", binop{&andTable, false}}, {"nand", binop{&andTable, true}},
	{"or", binop{&orTable, false}}, {"nor", binop{&orTable, true}},
	{"xor", binop{&xorTable, false}}, {"xnor", binop{&xorTable, true}},
}

const lib1164 = "std_logic_1164"

func init() {
	add(lib1164, "resolved", cLogic, resolvedBody, cVec)
	for _, b := range binops {
		add(lib1164, b.name, cLogic, logicScalar(b.op), cLogic, cLogic)
		add(lib1164, b.name, cVec, logicVector(b.op), cVec, cVec)
	}
	add(lib1164, "not", cLogic, notScalar, cLogic)
	add(lib1164, "not", cVec, notVector, cVec)
	for _, c := range []struct {
		name string
		tab  *[9]int64
	}{{"to_x01", &toX01}, {"to_x01z", &toX01Z}, {"to_ux01", &toUX01}} {
		add(lib1164, c.name, cLogic, convScalar(c.tab), cLogic)
		add(lib1164, c.name, cVec, convVector(c.tab), cVec)
		add(lib1164, c.name, cLogic, bitScalar, cBit)
		add(lib1164, c.name, cVec, bitVector, cBitVec)
	}
	add(lib1164, "to_01", cLogic, to01Scalar, cLogic, cLogic)
	add(lib1164, "to_01", cVec, to01Vector, cVec, cLogic)
}

// Resolve is the resolution function RESOLVED on the driver values
// drivers (STD_ULOGIC positions), without building its array argument:
// a single driver's value, else the table folded from 'Z'. ok false
// for a position outside STD_ULOGIC.
func Resolve(drivers []eval.Value) (int64, bool) {
	if len(drivers) == 1 {
		return logicPos(drivers[0])
	}
	r := int64(lZ)
	for _, d := range drivers {
		p, ok := logicPos(d)
		if !ok {
			return 0, false
		}
		r = resolutionTable[r][p]
	}
	return r, true
}

// resolvedBody is RESOLVED (s : STD_ULOGIC_VECTOR) return STD_ULOGIC.
func resolvedBody(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	if len(args[0].E) == 0 {
		return eval.Value{}, false
	}
	p, ok := Resolve(args[0].E)
	if !ok {
		return eval.Value{}, false
	}
	return scalar(rt, p)
}

func logicScalar(op binop) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		a, ok1 := logicPos(args[0])
		b, ok2 := logicPos(args[1])
		if !ok1 || !ok2 {
			return eval.Value{}, false
		}
		return scalar(rt, op.apply(a, b))
	}
}

// logicVector is a logical operator on two vectors of one length:
// the result is indexed 1 to the length. A length mismatch, which the
// body reports, is declined.
func logicVector(op binop) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		l, r := args[0].E, args[1].E
		if len(l) != len(r) {
			return eval.Value{}, false
		}
		pos := make([]int64, len(l))
		for i := range l {
			a, ok1 := logicPos(l[i])
			b, ok2 := logicPos(r[i])
			if !ok1 || !ok2 {
				return eval.Value{}, false
			}
			pos[i] = op.apply(a, b)
		}
		return vector(rt, upto(len(l)), pos), true
	}
}

func notScalar(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	a, ok := logicPos(args[0])
	if !ok {
		return eval.Value{}, false
	}
	return scalar(rt, notTable[a])
}

func notVector(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	return mapVector(args[0], rt, &notTable, upto)
}

// mapVector maps each element of v through tab into a vector of
// subtype rt indexed by rng(length).
func mapVector(v eval.Value, rt *eval.Type, tab *[9]int64,
	rng func(int) eval.Range,
) (eval.Value, bool) {
	pos := make([]int64, len(v.E))
	for i, e := range v.E {
		p, ok := logicPos(e)
		if !ok {
			return eval.Value{}, false
		}
		pos[i] = tab[p]
	}
	return vector(rt, rng(len(v.E)), pos), true
}

func convScalar(tab *[9]int64) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		p, ok := logicPos(args[0])
		if !ok {
			return eval.Value{}, false
		}
		return scalar(rt, tab[p])
	}
}

func convVector(tab *[9]int64) Func {
	return func(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
		return mapVector(args[0], rt, tab, upto)
	}
}

// bitPos is the STD_ULOGIC position of BIT position b ('0' or '1').
func bitPos(b int64) (int64, bool) {
	return l0 + b, b == 0 || b == 1
}

// bitScalar is To_X01, To_X01Z and To_UX01 of a BIT.
func bitScalar(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	p, ok := bitPos(args[0].I)
	if !ok {
		return eval.Value{}, false
	}
	return scalar(rt, p)
}

// bitVector is To_X01, To_X01Z and To_UX01 of a BIT_VECTOR.
func bitVector(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	pos := make([]int64, len(args[0].E))
	for i, e := range args[0].E {
		p, ok := bitPos(e.I)
		if !ok {
			return eval.Value{}, false
		}
		pos[i] = p
	}
	return vector(rt, upto(len(pos)), pos), true
}

// to01 is TO_01 of one element: '0' and 'L' are '0', '1' and 'H' are
// '1'; ok false for the others.
func to01(p int64) (int64, bool) {
	switch p {
	case l0, lL:
		return l0, true
	case l1, lH:
		return l1, true
	}
	return 0, false
}

func to01Scalar(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	p, ok := to01(args[0].I)
	if !ok {
		if p, ok = logicPos(args[1]); !ok {
			return eval.Value{}, false
		}
	}
	return scalar(rt, p)
}

// to01Vector is TO_01 (s : STD_ULOGIC_VECTOR; xmap) indexed s'length-1
// downto 0: every element xmap when one is not 0, 1, L or H.
func to01Vector(args []eval.Value, rt *eval.Type) (eval.Value, bool) {
	s := args[0].E
	x, ok := logicPos(args[1])
	if !ok {
		return eval.Value{}, false
	}
	pos := make([]int64, len(s))
	bad := false
	for i, e := range s {
		p, ok := to01(e.I)
		bad = bad || !ok
		pos[i] = p
	}
	if bad {
		for i := range pos {
			pos[i] = x
		}
	}
	return vector(rt, downto(len(s)), pos), true
}

// ToX01 is To_X01 of the STD_ULOGIC value v as a position; ok false
// outside STD_ULOGIC.
func ToX01(v eval.Value) (int64, bool) {
	p, ok := logicPos(v)
	if !ok || v.Kind() != eval.KindEnum {
		return 0, false
	}
	return toX01[p], true
}
