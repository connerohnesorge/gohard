// Package ieeenative holds native Go bodies of the ieee functions a
// simulation calls most (#59): the std_logic_1164 resolution function,
// logical operators and strength strippers, and the numeric_std
// additions, subtractions, comparisons and conversions. The rule of
// docs/native-bodies.md admits them: each package takes more than 20%
// of kernel time on some corpus design.
//
// A native body computes the value the interpreted body (the vendored
// source run by internal/sema) returns, with the same index range and
// subtype. It covers the common case only and declines the rest (ok
// false), which the caller then interprets: every input on which the
// body reports (a numeric_std metavalue or null-argument warning, a
// truncated conversion, a length mismatch) or fails (a result outside
// its subtype) runs from source, so assertions keep their message and
// source line. The bodies are pure functions of their arguments; they
// never allocate a type and never read the world.
//
// The oracles are the interpreted bodies (property tests on generated
// inputs, internal/sema) and, for numeric_std, the math/big model in
// internal/elab/ieeeref; the corpus traces must not change (#59).
package ieeenative

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// Func is a native body: the value of a call on args (in parameter
// order), whose result subtype is rt (the function's declared result
// subtype). ok false declines the call: the caller interprets it.
type Func func(args []eval.Value, rt *eval.Type) (v eval.Value, ok bool)

// class is the kind of a parameter or result as the bodies see it.
type class uint8

const (
	cNone   class = iota
	cLogic        // STD_ULOGIC and its subtypes (X01, UX01, ...)
	cBit          // BIT
	cBool         // BOOLEAN
	cInt          // INTEGER and NATURAL
	cVec          // STD_ULOGIC_VECTOR, STD_LOGIC_VECTOR
	cUns          // UNSIGNED, UNRESOLVED_UNSIGNED
	cSgn          // SIGNED, UNRESOLVED_SIGNED
	cBitVec       // BIT_VECTOR
)

// stdULogic are the literals of STD_ULOGIC in position order.
var stdULogic = []string{"'u'", "'x'", "'0'", "'1'", "'z'", "'w'",
	"'l'", "'h'", "'-'"}

// STD_ULOGIC positions.
const (
	lU = iota
	lX
	l0
	l1
	lZ
	lW
	lL
	lH
	lDash
)

func isLogic(t *eval.Type) bool {
	if t == nil || t.Kind != eval.KindEnum ||
		len(t.Literals) != len(stdULogic) {
		return false
	}
	for i, l := range t.Literals {
		if strings.ToLower(l) != stdULogic[i] {
			return false
		}
	}
	return t.Range.Low() >= 0 && t.Range.High() < int64(len(stdULogic))
}

func isBit(t *eval.Type) bool {
	return t != nil && t.Kind == eval.KindEnum && len(t.Literals) == 2 &&
		t.Literals[0] == "'0'" && t.Literals[1] == "'1'"
}

// classOf classifies t; cNone for a type no native body takes.
func classOf(t *eval.Type) class {
	if t == nil {
		return cNone
	}
	switch t.Kind {
	case eval.KindEnum:
		switch {
		case isLogic(t):
			return cLogic
		case isBit(t):
			return cBit
		case len(t.Literals) == 2 && t.Literals[0] == "false" &&
			t.Literals[1] == "true":
			return cBool
		}
	case eval.KindInteger:
		return cInt
	case eval.KindArray:
		if len(t.Index) != 1 {
			return cNone
		}
		if isBit(t.Elem) {
			return cBitVec
		}
		if !isLogic(t.Elem) {
			return cNone
		}
		n := strings.ToUpper(t.BaseType().Name)
		switch {
		case strings.HasSuffix(n, "UNSIGNED"):
			return cUns
		case strings.HasSuffix(n, "SIGNED"):
			return cSgn
		case strings.HasSuffix(n, "STD_ULOGIC_VECTOR"),
			strings.HasSuffix(n, "STD_LOGIC_VECTOR"):
			return cVec
		}
	}
	return cNone
}

// sig is a function's name and the classes of its parameters and
// result: the key of a native body.
type sig struct {
	unit, name string
	params     string // one byte per parameter: '0' + class
	result     class
}

func sigOf(unit, name string, params []*eval.Type, rt *eval.Type,
) (sig, bool) {
	s := sig{unit: strings.ToLower(unit),
		name: strings.ToLower(strings.Trim(name, `"`)), result: classOf(rt)}
	if s.result == cNone {
		return s, false
	}
	b := make([]byte, len(params))
	for i, p := range params {
		c := classOf(p)
		if c == cNone {
			return s, false
		}
		b[i] = '0' + byte(c)
	}
	s.params = string(b)
	return s, true
}

func classes(cs ...class) string {
	b := make([]byte, len(cs))
	for i, c := range cs {
		b[i] = '0' + byte(c)
	}
	return string(b)
}

// bodies are the native bodies by signature (lookup only).
var bodies = map[sig]Func{}

func add(unit, name string, result class, f Func, params ...class) {
	bodies[sig{unit: unit, name: name, params: classes(params...),
		result: result}] = f
}

// Lookup returns the native body of the function name (an operator
// symbol with or without its quotes) of the ieee package unit
// ("std_logic_1164", "numeric_std"), whose parameters have the
// subtypes params and whose result has the subtype rt; nil when it
// has none.
func Lookup(unit, name string, params []*eval.Type, rt *eval.Type) Func {
	s, ok := sigOf(unit, name, params, rt)
	if !ok {
		return nil
	}
	return bodies[s]
}

// Cost is the evaluation steps a native call on args charges: one,
// plus one per scalar element of the arguments, so a call stays
// bounded by its input like the interpreted body (P7).
func Cost(args []eval.Value) int64 {
	n := int64(1)
	for _, a := range args {
		n += int64(len(a.E))
	}
	return n
}

// vector returns the one-dimensional array of subtype rt with index
// range r whose elements have positions pos.
func vector(rt *eval.Type, r eval.Range, pos []int64) eval.Value {
	e := make([]eval.Value, len(pos))
	for i, p := range pos {
		e[i] = eval.Enum(rt.Elem, p)
	}
	return eval.Array(rt.ArraySubtype(r), []eval.Range{r}, e)
}

// downto is (n-1 downto 0).
func downto(n int) eval.Range {
	return eval.Range{Left: int64(n - 1), Right: 0, Dir: eval.Downto}
}

// upto is (1 to n).
func upto(n int) eval.Range {
	return eval.Range{Left: 1, Right: int64(n), Dir: eval.To}
}

// scalar returns position p of the scalar subtype rt; ok false when p
// is outside it (the interpreted body fails there).
func scalar(rt *eval.Type, p int64) (eval.Value, bool) {
	if !rt.Range.Contains(p) {
		return eval.Value{}, false
	}
	return eval.Enum(rt, p), true
}

func boolean(rt *eval.Type, b bool) (eval.Value, bool) {
	if b {
		return scalar(rt, 1)
	}
	return scalar(rt, 0)
}
