package psl

import (
	"errors"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// builtin.go runs the PSL built-in functions in a directive's booleans
// (IEEE 1850 5.2.3, #111): prev, stable, rose and fell read the value
// their argument had at earlier ticks of the directive's clock; onehot,
// onehot0 and isunknown read its value at this tick. The runner
// samples every argument once at time 0 (the initial values, as NVC
// 1.23 does) and then at every tick, before the booleans are
// evaluated, keeping the last samples each call reads.

type bKind uint8

const (
	bPrev bKind = iota
	bStable
	bRose
	bFell
	bOnehot
	bOnehot0
	bIsunknown
)

var builtinKinds = map[string]bKind{"prev": bPrev, "stable": bStable,
	"rose": bRose, "fell": bFell, "onehot": bOnehot, "onehot0": bOnehot0,
	"isunknown": bIsunknown}

// builtin is one built-in function call of a directive.
type builtin struct {
	// call is the call as the directive's booleans hold it: the
	// runner's evaluator answers it with its value (builtinVals).
	call *sema.Expr
	kind bKind
	arg  *sema.Expr
	// depth is the number of earlier samples of arg the call reads:
	// prev's count, 1 for stable, rose and fell, 0 for the others.
	depth int
}

// builtins compiles the built-in calls of the boolean e, inner calls
// first (an argument may make one), each call once.
func (b *builder) builtins(e *sema.Expr) {
	walkExpr(e, func(x *sema.Expr) {
		name := sema.PSLBuiltin(x)
		if name == "" || b.calls[x] {
			return
		}
		b.calls[x] = true
		if bi := b.builtin(x, name); bi != nil {
			b.dir.builtins = append(b.dir.builtins, bi)
		}
	})
}

// builtin compiles the call x of the built-in function name. A clock
// argument (prev(e, n, clk), rose(e, clk), ...) and ended are not
// simulated yet: the directive reports unsupported/construct instead
// of running.
func (b *builder) builtin(x *sema.Expr, name string) *builtin {
	k, ok := builtinKinds[name]
	if !ok || len(x.Args) == 0 {
		b.unsupported("the built-in function " + name)
		return nil
	}
	bi := &builtin{call: x, kind: k, arg: x.Args[0]}
	// params are the arguments before the clock argument, if the
	// function takes one.
	params, clocked := 1, false
	switch k {
	case bPrev:
		bi.depth, params, clocked = 1, 2, true
		if len(x.Args) > 1 {
			v, err := b.p.Ev.Value(x.Args[1])
			if err != nil || v.T == nil || v.T.Kind != eval.KindInteger ||
				v.I < 1 || v.I > maxCount {
				b.unsupported("the count " +
					strings.TrimSpace(x.Args[1].Node.Text()) + " of prev")
				return nil
			}
			bi.depth = int(v.I)
		}
	case bStable, bRose, bFell:
		bi.depth, clocked = 1, true
	}
	switch {
	case clocked && len(x.Args) == params+1:
		b.unsupported("the clock argument of " + name)
		return nil
	case len(x.Args) > params:
		b.unsupported("the built-in function " + name + " with " +
			strconv.Itoa(len(x.Args)) + " arguments")
		return nil
	}
	return bi
}

// firstBuiltin returns the name of the first built-in call in e's
// tree, "" when it makes none.
func firstBuiltin(e *sema.Expr) string {
	name := ""
	walkExpr(e, func(x *sema.Expr) {
		if name == "" {
			name = sema.PSLBuiltin(x)
		}
	})
	return name
}

// walkExpr calls f on every expression of e's tree, operands before
// the expression that holds them.
func walkExpr(e *sema.Expr, f func(*sema.Expr)) {
	if e == nil {
		return
	}
	walkExpr(e.Prefix, f)
	for _, a := range e.Args {
		walkExpr(a, f)
	}
	for _, a := range e.Assocs {
		for _, c := range a.Choices {
			walkExpr(c, f)
		}
		walkExpr(a.Value, f)
	}
	if r := e.Range; r != nil {
		walkExpr(r.Left, f)
		walkExpr(r.Right, f)
		walkExpr(r.Attr, f)
	}
	f(e)
}

// maxSampled bounds the values the samples of one built-in call keep:
// its depth times the size of its argument's value (sizeAtMost), so
// prev(mem, 2000) of a large array fails at its first sample, time 0,
// instead of growing by a copy of mem at every tick (P7).
const maxSampled = 1 << 20

// sizeAtMost returns the number of values v is made of (itself and
// every element, nested ones included), or limit+1 when it is more
// than limit.
func sizeAtMost(v eval.Value, limit int) int {
	n := 1
	for _, e := range v.E {
		if n > limit {
			break
		}
		n += sizeAtMost(e, limit-n)
	}
	return min(n, limit+1)
}

// tooManySamples is the run-time error of the call bi, whose samples
// would keep more than maxSampled values.
func (r *runner) tooManySamples(bi *builtin) error {
	return errors.New(strings.TrimSpace(bi.call.Node.Text()) +
		" in PSL directive " + r.dir.label + " would keep more than " +
		strconv.Itoa(maxSampled) + " sampled values")
}

// samples holds the last values of one built-in's argument, at most
// the call's depth: a ring, oldest first until it is full, then
// starting at next.
type samples struct {
	v    []eval.Value
	next int
}

// push records v as the newest sample, dropping the oldest past
// depth.
func (s *samples) push(v eval.Value, depth int) {
	if depth == 0 {
		return
	}
	if len(s.v) < depth {
		s.v = append(s.v, v)
		s.next = len(s.v) % depth
		return
	}
	s.v[s.next] = v
	s.next = (s.next + 1) % depth
}

// back returns the k-th newest sample (k = 1 is the newest) or, when
// fewer than k were taken, the default value of cur's subtype with
// cur's bounds (LRM 6.4.2.3: T'LEFT for a scalar).
func (s *samples) back(k int, cur eval.Value) (eval.Value, error) {
	n := len(s.v)
	if k < 1 || k > n {
		return defaultLike(cur)
	}
	return s.v[((s.next-k)%n+n)%n], nil
}

func (s samples) clone() samples {
	s.v = append([]eval.Value(nil), s.v...)
	return s
}

// defaultLike returns v with every scalar replaced by the default
// value of its subtype.
func defaultLike(v eval.Value) (eval.Value, error) {
	switch v.Kind() {
	case eval.KindArray, eval.KindRecord:
		out := v
		out.E = make([]eval.Value, len(v.E))
		for i, e := range v.E {
			d, err := defaultLike(e)
			if err != nil {
				return eval.Value{}, err
			}
			out.E[i] = d
		}
		return out, nil
	}
	return eval.Default(nil, v.T)
}

// value is the call's value at this tick, given its argument's value
// cur and the earlier samples h. A boolean of BIT or STD_ULOGIC is
// true for '1' and 'H' (truth), and isunknown finds an element that is
// not '0', '1', 'L' or 'H' (as NVC 1.23 reads them).
func (bi *builtin) value(cur eval.Value, h *samples) (eval.Value, error) {
	switch bi.kind {
	case bPrev:
		return h.back(bi.depth, cur)
	case bStable, bRose, bFell:
		p, err := h.back(1, cur)
		if err != nil {
			return eval.Value{}, err
		}
		now, was := high(cur), high(p)
		switch bi.kind {
		case bStable:
			return eval.Bool(cur.Equal(p)), nil
		case bRose:
			return eval.Bool(now && !was), nil
		}
		return eval.Bool(!now && was), nil
	case bOnehot, bOnehot0:
		n := 0
		for _, e := range elements(cur) {
			if high(e) {
				n++
			}
		}
		if bi.kind == bOnehot {
			return eval.Bool(n == 1), nil
		}
		return eval.Bool(n <= 1), nil
	}
	for _, e := range elements(cur) {
		if x01(e) < 0 {
			return eval.Bool(true), nil
		}
	}
	return eval.Bool(false), nil
}

func high(v eval.Value) bool {
	t, _ := truth(v)
	return t
}

// elements are the elements of an array value, or the value itself.
func elements(v eval.Value) []eval.Value {
	if v.Kind() == eval.KindArray {
		return v.E
	}
	return []eval.Value{v}
}

// builtinVals answers the built-in calls of a directive's booleans
// with their values at the tick: the sema.Dynamic of the directive's
// evaluator, whose signals are bound to their values, so it owns
// nothing else.
type builtinVals map[*sema.Expr]eval.Value

func (builtinVals) Ref(*sema.Decl) (eval.Ref, bool) { return nil, false }

func (builtinVals) Load(eval.Ref) (eval.Value, error) {
	return eval.Value{}, errors.New("a PSL directive owns no object")
}

func (m builtinVals) Lower(_ *sema.Evaluator, e *sema.Expr,
	_ *eval.Budget,
) (eval.Expr, bool, error) {
	v, ok := m[e]
	if !ok {
		return nil, false, nil
	}
	return &eval.Const{V: v}, true, nil
}

func (builtinVals) Call(eval.Func, []eval.Value, *eval.Budget) (
	eval.Value, bool, error,
) {
	return eval.Value{}, false, nil
}
