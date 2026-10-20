package sema

// memo.go memoizes calls of pure functions for the simulator (#35):
// the ieee functions a design calls every cycle (TO_X01, RESOLVED,
// numeric_std "+") are called with the same few argument values over
// and over. A pure function's result depends only on its arguments,
// so a call whose arguments were seen before returns the recorded
// result and charges the recorded number of steps: memoization changes
// wall time only, never a value, a trace or a step count. Calls that
// read bindings or the simulator's context are not recorded, nor calls
// that computed a layout or a constant (a later call finds them
// cached and costs less: it is the one recorded).

import (
	"math"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/eval"
)

// memoLimit bounds the recorded calls of one analysis; past it calls
// are interpreted as usual.
const memoLimit = 1 << 16

// memoLeaves bounds the scalar elements of a call's arguments that
// are worth a key.
const memoLeaves = 256

type memoEntry struct {
	v     eval.Value
	steps int64
}

// WithMemo returns a copy of ev that memoizes pure function calls.
func (ev *Evaluator) WithMemo() *Evaluator {
	c := *ev
	c.memo = true
	return &c
}

// memoKey encodes a call of x on args; ok is false for arguments not
// worth or not safe to key (access, file and protected values, large
// composites).
func memoKey(x *Decl, args []eval.Value) (string, bool) {
	buf := make([]byte, 0, 64)
	buf = strconv.AppendUint(buf, uint64(x.Unit.Index), 36)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(x.Span.Start), 36)
	n := 0
	for _, a := range args {
		buf = append(buf, '|')
		var ok bool
		if buf, ok = appendValue(buf, a, &n); !ok {
			return "", false
		}
	}
	return string(buf), true
}

// appendValue appends the key of argument v, counting its elements in
// n.
func appendValue(buf []byte, v eval.Value, n *int) ([]byte, bool) {
	*n++
	if *n > memoLeaves || v.T == nil {
		return buf, false
	}
	switch v.T.Kind {
	case eval.KindInteger, eval.KindEnum, eval.KindPhysical:
		buf = appendType(buf, v.T)
		return strconv.AppendInt(buf, v.I, 36), true
	case eval.KindFloating:
		buf = appendType(buf, v.T)
		return strconv.AppendUint(buf, math.Float64bits(v.F), 36), true
	case eval.KindArray, eval.KindRecord:
		buf = appendType(buf, v.T)
		for _, r := range v.B {
			buf = strconv.AppendInt(buf, r.Left, 36)
			buf = append(buf, byte('0'+r.Dir))
			buf = strconv.AppendInt(buf, r.Right, 36)
		}
		buf = append(buf, '(')
		for _, e := range v.E {
			var ok bool
			if buf, ok = appendValue(buf, e, n); !ok {
				return buf, false
			}
			buf = append(buf, ',')
		}
		return append(buf, ')'), true
	}
	return buf, false
}

// appendType identifies the subtype t by its name and its constraint:
// the attributes a function body may read of an argument.
func appendType(buf []byte, t *eval.Type) []byte {
	buf = append(buf, t.Name...)
	buf = append(buf, '#')
	switch t.Kind {
	case eval.KindInteger, eval.KindEnum, eval.KindPhysical:
		buf = strconv.AppendInt(buf, t.Range.Left, 36)
		buf = append(buf, byte('0'+t.Range.Dir))
		buf = strconv.AppendInt(buf, t.Range.Right, 36)
	case eval.KindFloating:
		buf = strconv.AppendUint(buf, math.Float64bits(t.Real.Left), 36)
		buf = append(buf, byte('0'+t.Real.Dir))
		buf = strconv.AppendUint(buf, math.Float64bits(t.Real.Right), 36)
	}
	return append(buf, '#')
}

// memoCall is Call of the pure function x through the memo table.
func (ev *Evaluator) memoCall(x *Decl, args []eval.Value, b *eval.Budget,
) (eval.Value, error) {
	key, ok := "", x.Unit != nil
	if ok {
		key, ok = memoKey(x, args)
	}
	if !ok {
		return ev.pureCall(x, args, b)
	}
	if r, hit := ev.sh.memo[key]; hit {
		if err := b.Step(r.steps); err != nil {
			return eval.Value{}, err
		}
		return r.v, nil
	}
	u0, reads, dyn, fills := b.Used(), ev.sh.reads, ev.sh.dynReads,
		ev.sh.fills
	v, err := ev.pureCall(x, args, b)
	if err == nil && ev.sh.reads == reads && ev.sh.dynReads == dyn &&
		ev.sh.fills == fills && len(ev.sh.memo) < memoLimit {
		if ev.sh.memo == nil {
			ev.sh.memo = map[string]memoEntry{}
		}
		ev.sh.memo[key] = memoEntry{v: v, steps: b.Used() - u0}
	}
	return v, err
}
