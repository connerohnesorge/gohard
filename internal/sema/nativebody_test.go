package sema

import (
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/eval"
)

// The property of the native ieee bodies (#59, P4): on generated
// arguments, every native body either declines or returns exactly the
// value the interpreted body returns, index range and subtype names
// included, on a call the interpreted body neither fails nor reports
// on (as callers see it: checked against the result subtype); and a
// call through an evaluator with native bodies (declined
// calls interpreted) returns the same value, error and reports as one
// without. Edge cases come first (empty and 1-element vectors, 63 to
// 65 bits, metavalues, integer bounds); a failure prints its seed,
// which -nativebody.seed replays.

var nativeSeed = flag.Uint64("nativebody.seed", 0,
	"replay the native-body property from this seed (0: the default)")

type reportLog struct{ got []string }

func (r *reportLog) Report(_ *SeqStmt, assertion bool, sev int64,
	msg string,
) error {
	r.got = append(r.got, fmt.Sprintf("%v/%d/%s", assertion, sev, msg))
	return nil
}

// nativeEnv is one standard's evaluators: interpreted and native.
type nativeEnv struct {
	interp, native *Evaluator
	ilog, nlog     *reportLog
	fns            []*Decl
}

func loadNative(t *testing.T, std string) *nativeEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = std
	src := "library ieee;\nuse ieee.std_logic_1164.all;\n" +
		"use ieee.numeric_std.all;\nentity e is end entity;\n"
	r := Analyze(Input{Config: cfg, Jobs: env.Sequential{},
		Files: []Source{{Path: "e.vhd", Text: []byte(src)}}})
	lib, ok := r.Library("ieee")
	if !ok {
		t.Fatal("no library ieee")
	}
	e := &nativeEnv{interp: NewEvaluator(r, 50_000_000),
		native: NewEvaluator(r, 50_000_000),
		ilog:   &reportLog{}, nlog: &reportLog{}}
	e.interp.SetReporter(e.ilog)
	e.native.SetReporter(e.nlog)
	e.native.SetNativeBodies(true)
	for _, name := range []Key{"std_logic_1164", "numeric_std"} {
		u, ok := lib.Unit(name)
		if !ok {
			t.Fatalf("no ieee.%s", name)
		}
		for _, d := range u.Region.Decls {
			if d.Kind != DeclFunction || d.Implicit {
				continue
			}
			nb, err := e.native.nativeOf(d, eval.NewBudget(0))
			if err != nil {
				t.Fatalf("%s: %v", d.Text, err)
			}
			if nb.fn != nil {
				e.fns = append(e.fns, d)
			}
		}
	}
	return e
}

type ngen struct {
	r  env.SplitMix64
	it int
}

func (g *ngen) n(k int) int { return env.Intn(&g.r, k) }

// vecLen picks a vector length: the edge lengths first.
func (g *ngen) vecLen() int {
	if g.it < 12 {
		return []int{0, 1, 2, 63, 64, 65, 3, 8, 31, 32, 33, 72}[g.it]
	}
	if g.n(5) == 0 {
		return g.n(73)
	}
	return 1 + g.n(12)
}

// elem picks a STD_ULOGIC position under mode: 0 only 0 and 1, 1 also
// L and H, 2 one metavalue among 0 and 1, 3 anything.
func (g *ngen) elem(mode int) int64 {
	switch mode {
	case 0, 2:
		return 2 + int64(g.n(2))
	case 1:
		return []int64{2, 3, 6, 7}[g.n(4)]
	}
	return int64(g.n(9))
}

func (g *ngen) vector(pt *eval.Type, n int) eval.Value {
	mode := g.n(4)
	if g.it < 24 {
		mode = 0
	}
	el := make([]eval.Value, n)
	pattern := g.n(4)
	for i := range el {
		p := g.elem(mode)
		if g.it < 24 {
			switch pattern {
			case 0:
				p = 2
			case 1:
				p = 3
			case 2: // the most negative signed value
				p = 2
				if i == 0 {
					p = 3
				}
			}
		}
		el[i] = eval.Enum(pt.Elem, p)
	}
	if mode == 2 && n > 0 {
		el[g.n(n)] = eval.Enum(pt.Elem, []int64{0, 1, 4, 5, 8}[g.n(5)])
	}
	if pt.Elem.Kind == eval.KindEnum && len(pt.Elem.Literals) == 2 {
		for i := range el {
			el[i].I = int64(g.n(2)) // BIT
		}
	}
	k := int64(g.n(6))
	var r eval.Range
	switch g.n(4) {
	case 0:
		r = eval.Range{Left: int64(n) - 1, Right: 0, Dir: eval.Downto}
	case 1:
		r = eval.Range{Left: 0, Right: int64(n) - 1, Dir: eval.To}
	case 2:
		r = eval.Range{Left: k + int64(n) - 1, Right: k, Dir: eval.Downto}
	default:
		r = eval.Range{Left: k, Right: k + int64(n) - 1, Dir: eval.To}
	}
	return eval.Array(pt.ArraySubtype(r), []eval.Range{r}, el)
}

// integer picks an integer of pt, near the powers of two of the
// vector length w (sizes, and values that do or do not fit).
func (g *ngen) integer(pt *eval.Type, w int) eval.Value {
	lo, hi := pt.Range.Low(), pt.Range.High()
	var v int64
	switch c := g.n(8); {
	case g.it < 4:
		v = []int64{0, 1, lo, hi}[g.it]
	case c == 0:
		v = int64(g.n(80))
	case c <= 3 && w > 0 && w < 63:
		v = int64(1)<<uint(w-g.n(2)) - int64(g.n(3)) + 1
		if g.n(2) == 0 {
			v = -v
		}
	case c == 4:
		v = int64(g.n(1<<20)) - 1<<19
	default:
		v = int64(g.r.Uint64())
	}
	v = max(lo, min(hi, v))
	return eval.Int(pt, v)
}

// args generates the arguments of d, whose parameters have the
// subtypes params. A size parameter (SIZE, NEW_SIZE) stays small: the
// interpreted body allocates a vector of that size.
func (g *ngen) args(d *Decl, params []*eval.Type) []eval.Value {
	args := make([]eval.Value, len(params))
	n := g.vecLen()
	w := 0
	for i, pt := range params {
		if pt.Kind == eval.KindArray {
			m := n
			if g.n(4) == 0 {
				m = g.vecLen()
			}
			args[i] = g.vector(pt, m)
			w = max(w, m)
		}
	}
	for i, pt := range params {
		switch pt.Kind {
		case eval.KindEnum:
			args[i] = eval.Enum(pt, pt.Range.Low()+
				int64(g.n(int(pt.Range.Len()))))
		case eval.KindInteger:
			args[i] = g.integer(pt, w)
			if n := d.Params[i].Name; n == "size" || n == "new_size" {
				args[i].I = int64(g.n(80))
			}
		}
	}
	return args
}

// sameValue describes the first difference between a and b ("" when
// they are the same value of the same subtype).
func sameValue(a, b eval.Value) string {
	if (a.T == nil) != (b.T == nil) {
		return "one value has no type"
	}
	if a.T != nil {
		if a.T.Kind != b.T.Kind || a.T.Name != b.T.Name {
			return fmt.Sprintf("type %s %s, want %s %s", a.T.Kind,
				a.T.Name, b.T.Kind, b.T.Name)
		}
		if a.T.Kind.IsScalar() && a.T.Range != b.T.Range {
			return fmt.Sprintf("subtype %v, want %v", a.T.Range,
				b.T.Range)
		}
		if fmt.Sprint(a.T.Bounds) != fmt.Sprint(b.T.Bounds) {
			return fmt.Sprintf("subtype bounds %v, want %v", a.T.Bounds,
				b.T.Bounds)
		}
	}
	if a.I != b.I || a.F != b.F {
		return fmt.Sprintf("value %d, want %d", a.I, b.I)
	}
	if fmt.Sprint(a.B) != fmt.Sprint(b.B) {
		return fmt.Sprintf("bounds %v, want %v", a.B, b.B)
	}
	if len(a.E) != len(b.E) {
		return fmt.Sprintf("%d elements, want %d", len(a.E), len(b.E))
	}
	for i := range a.E {
		if d := sameValue(a.E[i], b.E[i]); d != "" {
			return fmt.Sprintf("element %d: %s", i, d)
		}
	}
	return ""
}

func showArgs(args []eval.Value) string {
	var s []string
	for _, a := range args {
		if a.Kind() != eval.KindArray {
			s = append(s, fmt.Sprint(a.I))
			continue
		}
		var b strings.Builder
		for _, e := range a.E {
			if e.T != nil && int(e.I) < len(e.T.Literals) {
				b.WriteString(strings.Trim(e.T.Literals[e.I], "'"))
			}
		}
		s = append(s, fmt.Sprintf("%v:%q", a.B, b.String()))
	}
	return strings.Join(s, ", ")
}

func TestNativeBodies(t *testing.T) {
	iters := 300
	if testing.Short() {
		iters = 60
	}
	seed := *nativeSeed
	if seed == 0 {
		seed = 59
	}
	for _, std := range []string{"2008", "2019"} {
		t.Run(std, func(t *testing.T) {
			e := loadNative(t, std)
			if len(e.fns) < 60 {
				t.Fatalf("%d functions with a native body, want 60 "+
					"or more", len(e.fns))
			}
			for fi, d := range e.fns {
				nb, _ := e.native.nativeOf(d, eval.NewBudget(0))
				s := seed + uint64(fi)*1_000_003
				g := &ngen{r: env.SplitMix64{State: s}}
				taken := 0
				for g.it = 0; g.it < iters; g.it++ {
					args := g.args(d, nb.params)
					if checkNative(t, e, d, nb, args) {
						taken++
					}
				}
				if taken == 0 {
					t.Errorf("%s %s(%d): the native body declined "+
						"every call (seed %d)", d.Unit.Name, d.Text,
						len(d.Params), s)
				}
				if t.Failed() {
					t.Fatalf("%s %s(%d): replay with -nativebody.seed "+
						"%d", d.Unit.Name, d.Text, len(d.Params), seed)
				}
			}
			t.Logf("%d native bodies checked, %d calls each", len(e.fns),
				iters)
		})
	}
}

// checkNative checks one call and reports whether the native body
// took it.
func checkNative(t *testing.T, e *nativeEnv, d *Decl, nb nbody,
	args []eval.Value,
) bool {
	t.Helper()
	name := fmt.Sprintf("%s.%s(%s)", d.Unit.Name, d.Text, showArgs(args))
	e.ilog.got, e.nlog.got = nil, nil
	want, werr := e.interp.Call(d, args, eval.NewBudget(50_000_000))
	nbud := eval.NewBudget(50_000_000)
	got, gerr := e.native.Call(d, args, nbud)
	if (werr == nil) != (gerr == nil) {
		t.Errorf("%s: error %v, interpreted %v", name, gerr, werr)
		return false
	}
	// Every caller checks a result against the call's subtype
	// (eval.Call), which sets a scalar's subtype and an array's type;
	// the values are compared as callers see them.
	if werr == nil {
		want, werr = eval.Check(want, nb.rt)
		got, gerr = eval.Check(got, nb.rt)
		if werr != nil || gerr != nil {
			t.Errorf("%s: result check %v, interpreted %v", name, gerr,
				werr)
			return false
		}
		if diff := sameValue(got, want); diff != "" {
			t.Errorf("%s: %s", name, diff)
		}
	}
	if fmt.Sprint(e.nlog.got) != fmt.Sprint(e.ilog.got) {
		t.Errorf("%s: reports %v, interpreted %v", name, e.nlog.got,
			e.ilog.got)
	}
	v, ok := nb.fn(args, nb.rt)
	if !ok {
		return false
	}
	if werr != nil || len(e.ilog.got) > 0 {
		t.Errorf("%s: native body took a call the interpreted body "+
			"fails (%v) or reports on (%v)", name, werr, e.ilog.got)
		return true
	}
	// The call is charged at least a step per element of its
	// arguments and of its result, as the evaluator charges for the
	// elements it builds (P7).
	elems := int64(len(v.E))
	for _, a := range args {
		elems += int64(len(a.E))
	}
	if nbud.Used() <= elems {
		t.Errorf("%s: native call charged %d steps for %d elements",
			name, nbud.Used(), elems)
	}
	if v, err := eval.Check(v, nb.rt); err != nil {
		t.Errorf("%s: native result check: %v", name, err)
	} else if diff := sameValue(v, want); diff != "" {
		t.Errorf("%s: native %s", name, diff)
	}
	return true
}

// TestNativeCostCoversResult: a native body that builds a result much
// larger than its arguments (TO_UNSIGNED, TO_SIGNED and RESIZE to a
// million elements) is charged a step per result element, so the step
// budget stops a loop of such calls as it stops the interpreted body
// (P7).
func TestNativeCostCoversResult(t *testing.T) {
	const size = 1_000_000
	e := loadNative(t, "2008")
	calls := 0
	for _, d := range e.fns {
		name := strings.ToLower(d.Text)
		if d.Unit.Name != "numeric_std" || len(d.Params) != 2 ||
			name != "to_unsigned" && name != "to_signed" &&
				name != "resize" {
			continue
		}
		nb, _ := e.native.nativeOf(d, eval.NewBudget(0))
		var arg eval.Value
		switch pt := nb.params[0]; pt.Kind {
		case eval.KindInteger:
			arg = eval.Int(pt, 0)
		case eval.KindArray:
			if pt.Index[0].Kind != eval.KindInteger {
				continue
			}
			r := eval.Range{Left: 0, Right: 0, Dir: eval.Downto}
			arg = eval.Array(pt.ArraySubtype(r), []eval.Range{r},
				[]eval.Value{eval.Enum(pt.Elem, 3)})
		default:
			continue
		}
		if nb.params[1].Kind != eval.KindInteger {
			continue
		}
		args := []eval.Value{arg, eval.Int(nb.params[1], size)}
		if _, ok := nb.fn(args, nb.rt); !ok {
			t.Fatalf("%s(%s): the native body declined", d.Text,
				showArgs(args))
		}
		calls++
		b := eval.NewBudget(0)
		v, err := e.native.Call(d, args, b)
		if err != nil {
			t.Fatalf("%s: %v", d.Text, err)
		}
		if len(v.E) != size || b.Used() <= size {
			t.Errorf("%s(%s): %d elements for %d steps, want %d "+
				"elements for more than %d steps", d.Text,
				showArgs(args[:1]), len(v.E), b.Used(), size, size)
		}
		if _, err := e.native.Call(d, args,
			eval.NewBudget(size/2)); err != eval.ErrBudget {
			t.Errorf("%s: under a budget of %d steps: %v, want %v",
				d.Text, size/2, err, eval.ErrBudget)
		}
	}
	if calls < 3 {
		t.Errorf("%d calls checked, want TO_UNSIGNED, TO_SIGNED and "+
			"RESIZE", calls)
	}
}
