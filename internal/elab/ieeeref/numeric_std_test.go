package ieeeref

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// The check: for every function of ieee.numeric_std in the groups
// below, generated operands (0/1 vectors of 1 to 72 bits, integers
// that fit the vector they meet, edge values first) are passed to the
// interpreted body and the result is compared with a math/big model of
// the IEEE 1076-2008 definition, result width included.

type pkind uint8

const (
	kU pkind = iota + 1 // UNSIGNED
	kS                  // SIGNED
	kN                  // NATURAL
	kI                  // INTEGER
	kL                  // STD_ULOGIC
	kB                  // BOOLEAN
	kV                  // STD_ULOGIC_VECTOR
)

func kindOf(t *eval.Type) pkind {
	switch t.Kind {
	case eval.KindArray:
		n := strings.ToUpper(t.Name)
		switch {
		case strings.HasSuffix(n, "UNSIGNED"):
			return kU
		case strings.HasSuffix(n, "SIGNED"):
			return kS
		case strings.Contains(n, "STD_ULOGIC_VECTOR"),
			strings.Contains(n, "STD_LOGIC_VECTOR"):
			return kV
		}
	case eval.KindInteger:
		if t.Range.Low() >= 0 {
			return kN
		}
		return kI
	case eval.KindEnum:
		if len(t.Literals) == 9 {
			return kL
		}
		if len(t.Literals) == 2 && t.Literals[1] == "true" {
			return kB
		}
	}
	return 0
}

// fn is one numeric_std function overload.
type fn struct {
	d      *sema.Decl
	name   string
	params []*eval.Type
	kinds  []pkind
	res    pkind
}

func (f fn) sig() string {
	var ps []string
	for _, k := range f.kinds {
		ps = append(ps, "?USNILBV"[k:k+1])
	}
	return f.name + "(" + strings.Join(ps, ",") + ")"
}

type env0 struct {
	ev  *sema.Evaluator
	fns []fn
}

// load analyzes numeric_std; native makes its evaluator call the
// native Go bodies (internal/ieeenative, #59) instead of interpreting
// the functions that have one.
func load(t *testing.T, native bool) *env0 {
	t.Helper()
	cfg := config.Defaults()
	cfg.Standard = "2008"
	src := "library ieee;\nuse ieee.numeric_std.all;\n" +
		"entity e is end entity;\n"
	r := sema.Analyze(sema.Input{Config: cfg, Jobs: env.Sequential{},
		Files: []sema.Source{{Path: "e.vhd", Text: []byte(src)}}})
	lib, ok := r.Library("ieee")
	if !ok {
		t.Fatal("no library ieee")
	}
	u, ok := lib.Unit("numeric_std")
	if !ok {
		t.Fatal("no ieee.numeric_std")
	}
	ev := sema.NewEvaluator(r, 50_000_000)
	ev.SetNativeBodies(native)
	e := &env0{ev: ev}
	for _, d := range u.Region.Decls {
		if d.Kind != sema.DeclFunction || d.Implicit {
			continue
		}
		f := fn{d: d, name: strings.ToLower(strings.Trim(d.Text, `"`))}
		bad := false
		for _, p := range d.Params {
			lt, err := ev.Layout(p.Type)
			if err != nil {
				t.Fatalf("%s: %v", d.Text, err)
			}
			f.params = append(f.params, lt)
			k := kindOf(lt)
			bad = bad || k == 0
			f.kinds = append(f.kinds, k)
		}
		rt, err := ev.Layout(d.Type)
		if err != nil {
			t.Fatalf("%s: %v", d.Text, err)
		}
		f.res = kindOf(rt)
		if !bad && f.res != 0 {
			e.fns = append(e.fns, f)
		}
	}
	return e
}

type rng struct{ s env.SplitMix64 }

func (r *rng) n(k int) int { return env.Intn(&r.s, k) }

// operand is one argument: a vector (bits as an unsigned pattern of
// n bits) or an integer.
type operand struct {
	k    pkind
	n    int
	bits *big.Int // vectors
	i    int64    // integers
}

func pow2(n int) *big.Int { return new(big.Int).Lsh(big.NewInt(1), uint(n)) }

// num is the numeric value of a vector (two's complement when signed)
// or integer.
func (o operand) num() *big.Int {
	switch o.k {
	case kU, kV:
		return new(big.Int).Set(o.bits)
	case kS:
		v := new(big.Int).Set(o.bits)
		if o.n > 0 && v.Bit(o.n-1) == 1 {
			v.Sub(v, pow2(o.n))
		}
		return v
	}
	return big.NewInt(o.i)
}

// wrap is v as an n-bit pattern.
func wrap(v *big.Int, n int) *big.Int {
	m := pow2(n)
	r := new(big.Int).Mod(v, m)
	return r
}

func (e *env0) value(t *eval.Type, o operand) eval.Value {
	switch o.k {
	case kU, kS, kV:
		r := eval.Range{Left: int64(o.n - 1), Right: 0, Dir: eval.Downto}
		el := make([]eval.Value, o.n)
		for i := range o.n {
			pos := int64(2) // '0'
			if o.bits.Bit(o.n-1-i) == 1 {
				pos = 3
			}
			el[i] = eval.Enum(t.Elem, pos)
		}
		return eval.Array(t.ArraySubtype(r), []eval.Range{r}, el)
	case kL:
		return eval.Enum(t, 2+o.i)
	}
	return eval.Int(t, o.i)
}

// result is what a call returned or should return.
type result struct {
	k    pkind
	n    int
	bits *big.Int
	i    int64
	b    bool
}

func (r result) String() string {
	switch r.k {
	case kU, kS, kV:
		return fmt.Sprintf("%d'%s", r.n, r.bits.Text(2))
	case kB:
		return fmt.Sprint(r.b)
	}
	return fmt.Sprint(r.i)
}

func (r result) equal(s result) bool {
	if r.k != s.k {
		return false
	}
	switch r.k {
	case kU, kS, kV:
		return r.n == s.n && r.bits.Cmp(s.bits) == 0
	case kB:
		return r.b == s.b
	}
	return r.i == s.i
}

func decode(k pkind, v eval.Value) (result, error) {
	r := result{k: k}
	switch k {
	case kU, kS, kV:
		r.n, r.bits = len(v.E), new(big.Int)
		for _, e := range v.E {
			r.bits.Lsh(r.bits, 1)
			switch e.I {
			case 3:
				r.bits.SetBit(r.bits, 0, 1)
			case 2:
			default:
				return r, fmt.Errorf("metavalue %s in result",
					e.T.Literals[e.I])
			}
		}
	case kB:
		r.b = v.I == 1
	case kL:
		r.i = v.I - 2
		if r.i != 0 && r.i != 1 {
			return r, fmt.Errorf("metavalue %s in result", v.T.Literals[v.I])
		}
	default:
		r.i = v.I
	}
	return r, nil
}

func vec(k pkind, n int, v *big.Int) result {
	return result{k: k, n: n, bits: wrap(v, n)}
}

// width is the vector length among the operands (the first vector's).
func width(os []operand) (int, pkind) {
	for _, o := range os {
		if o.k == kU || o.k == kS {
			return o.n, o.k
		}
	}
	return 0, 0
}

func bigCmp(a, b *big.Int) int { return a.Cmp(b) }

// model is the IEEE definition of f on os; ok false skips the case
// (an operand outside the function's domain, such as a zero divisor).
func model(f fn, os []operand) (result, bool) {
	vk := f.res
	switch f.name {
	case "+", "-", "*", "/", "rem", "mod":
		if len(os) == 1 {
			return model1(f, os[0])
		}
		a, b := os[0].num(), os[1].num()
		la, lb := os[0].n, os[1].n
		vecA, vecB := os[0].k <= kS, os[1].k <= kS
		var w int
		switch f.name {
		case "+", "-":
			w = max(la, lb)
			if !vecA {
				w = lb
			} else if !vecB {
				w = la
			}
		case "*":
			w = la + lb
			if !vecA {
				w = 2 * lb
			} else if !vecB {
				w = 2 * la
			}
		case "/":
			w = la
			if !vecA {
				w = lb
			}
		default:
			w = lb
			if !vecB {
				w = la
			}
		}
		v := new(big.Int)
		switch f.name {
		case "+":
			v.Add(a, b)
		case "-":
			v.Sub(a, b)
		case "*":
			v.Mul(a, b)
		case "/", "rem", "mod":
			if b.Sign() == 0 {
				return result{}, false
			}
			switch f.name {
			case "/":
				v.Quo(a, b)
			case "rem":
				v.Rem(a, b)
			default:
				v.Rem(a, b)
				if v.Sign() != 0 && v.Sign() != b.Sign() {
					v.Add(v, b)
				}
			}
		}
		return vec(vk, w, v), true
	case "abs":
		return model1(f, os[0])
	case "=", "/=", "<", "<=", ">", ">=", "?=", "?/=", "?<", "?<=", "?>",
		"?>=":
		c := bigCmp(os[0].num(), os[1].num())
		var t bool
		switch strings.TrimPrefix(f.name, "?") {
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
		if vk == kL {
			return result{k: kL, i: b2i(t)}, true
		}
		return result{k: kB, b: t}, true
	case "maximum", "minimum":
		w, _ := width(os)
		if os[0].k <= kS && os[1].k <= kS {
			w = max(os[0].n, os[1].n)
		}
		a, b := os[0].num(), os[1].num()
		m := a
		if (f.name == "maximum") == (b.Cmp(a) > 0) {
			m = b
		}
		return vec(vk, w, m), true
	case "shift_left", "shift_right", "rotate_left", "rotate_right",
		"sll", "srl", "sla", "sra", "rol", "ror":
		return shiftModel(f.name, os[0], int(os[1].i)), true
	case "resize":
		n := int(os[1].i)
		if os[1].k <= kS {
			n = os[1].n
		}
		return resizeModel(os[0], n), true
	case "to_integer":
		return result{k: vk, i: os[0].num().Int64()}, true
	case "to_unsigned", "to_signed":
		n := int(os[1].i)
		if os[1].k <= kS {
			n = os[1].n
		}
		return vec(vk, n, big.NewInt(os[0].i)), true
	case "std_match":
		if os[0].k == kL {
			return result{k: kB, b: os[0].i == os[1].i}, true
		}
		eq := os[0].n == os[1].n && os[0].bits.Cmp(os[1].bits) == 0
		return result{k: kB, b: eq}, true
	case "and", "or", "xor", "nand", "nor", "xnor", "not":
		return logicModel(f, os)
	}
	return result{}, false
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func model1(f fn, o operand) (result, bool) {
	v := o.num()
	switch f.name {
	case "-":
		v.Neg(v)
	case "abs":
		v.Abs(v)
	default:
		return result{}, false
	}
	return vec(f.res, o.n, v), true
}

func shiftModel(name string, o operand, c int) result {
	n := o.n
	bits := new(big.Int).Set(o.bits)
	sign := o.k == kS && n > 0 && bits.Bit(n-1) == 1
	shl := func(c int) *big.Int {
		return wrap(new(big.Int).Lsh(bits, uint(c)), n)
	}
	shr := func(c int, arith bool) *big.Int {
		if c >= n {
			if arith && sign {
				return new(big.Int).Sub(pow2(n), big.NewInt(1))
			}
			return new(big.Int)
		}
		r := new(big.Int).Rsh(bits, uint(c))
		if arith && sign {
			fill := new(big.Int).Sub(pow2(n), pow2(n-c))
			r.Or(r, fill)
		}
		return r
	}
	rol := func(c int) *big.Int {
		c %= n
		if c < 0 {
			c += n
		}
		hi := new(big.Int).Lsh(bits, uint(c))
		lo := new(big.Int).Rsh(bits, uint(n-c))
		return wrap(hi.Or(hi, lo), n)
	}
	signed := o.k == kS
	var r *big.Int
	switch name {
	case "shift_left":
		r = shl(c)
	case "shift_right":
		r = shr(c, signed)
	case "rotate_left":
		r = rol(c)
	case "rotate_right":
		r = rol(-c)
	case "sll", "sla":
		if c >= 0 {
			r = shl(c)
		} else {
			r = shr(-c, signed && name == "sla")
		}
	case "srl", "sra":
		if c >= 0 {
			r = shr(c, signed && name == "sra")
		} else {
			r = shl(-c)
		}
	case "rol":
		r = rol(c)
	case "ror":
		r = rol(-c)
	}
	return result{k: o.k, n: n, bits: r}
}

func resizeModel(o operand, n int) result {
	if o.k == kU || n >= o.n {
		return vec(o.k, n, o.num())
	}
	if n == 0 {
		return result{k: o.k, bits: new(big.Int)}
	}
	// SIGNED: the sign bit, then the n-1 rightmost bits.
	low := wrap(o.bits, n-1)
	if o.bits.Bit(o.n-1) == 1 {
		low.SetBit(low, n-1, 1)
	}
	return result{k: o.k, n: n, bits: low}
}

func logicModel(f fn, os []operand) (result, bool) {
	op := func(a, b uint) uint {
		switch strings.TrimPrefix(f.name, "n") {
		case "and":
			a &= b
		case "or":
			a |= b
		case "xor":
			a ^= b
		case "xnor":
			a = 1 ^ (a ^ b)
		case "ot": // not
			a = 1 - a
		}
		if f.name == "nand" || f.name == "nor" {
			a = 1 - a
		}
		return a & 1
	}
	if len(os) == 1 {
		o := os[0]
		if f.res == kL { // reduction
			acc := o.bits.Bit(0)
			for i := 1; i < o.n; i++ {
				acc = uint(op(acc, o.bits.Bit(i)))
			}
			if f.name == "nand" || f.name == "nor" || f.name == "xnor" {
				// op already negated each step: recompute plainly.
				return result{}, false
			}
			return result{k: kL, i: int64(acc)}, o.n > 0
		}
		r := new(big.Int)
		for i := range o.n {
			r.SetBit(r, i, op(o.bits.Bit(i), 0))
		}
		return result{k: o.k, n: o.n, bits: r}, true
	}
	a, b := os[0], os[1]
	if a.k <= kS && b.k <= kS && a.n != b.n {
		return result{}, false // a length mismatch is an error
	}
	v := a
	if a.k == kL {
		v = b
	}
	r := new(big.Int)
	for i := range v.n {
		x, y := uint(0), uint(0)
		if a.k == kL {
			x = uint(a.i)
		} else {
			x = a.bits.Bit(i)
		}
		if b.k == kL {
			y = uint(b.i)
		} else {
			y = b.bits.Bit(i)
		}
		r.SetBit(r, i, op(x, y))
	}
	return result{k: v.k, n: v.n, bits: r}, true
}

// gen generates the operands of one call of f; i < 16 gives edge
// cases (all-zero and all-one vectors, zero and extreme integers).
func gen(r *rng, f fn, it int) []operand {
	os := make([]operand, len(f.kinds))
	edge := it < 16
	vecLen := 1 + r.n(12)
	if r.n(6) == 0 {
		vecLen = 1 + r.n(72)
	}
	for i, k := range f.kinds {
		if k != kU && k != kS && k != kV {
			continue
		}
		n := vecLen
		if r.n(3) == 0 && !sameLength(f) {
			n = 1 + r.n(12)
		}
		o := operand{k: k, n: n, bits: new(big.Int)}
		switch {
		case edge && it%4 == 0:
		case edge && it%4 == 1:
			o.bits = new(big.Int).Sub(pow2(n), big.NewInt(1))
		case edge && it%4 == 2:
			o.bits = pow2(n - 1) // the most negative signed value
		default:
			for b := range n {
				o.bits.SetBit(o.bits, b, uint(r.n(2)))
			}
		}
		if f.name == "to_integer" && n > 31 {
			o.n, o.bits = 31, wrap(o.bits, 31)
		}
		os[i] = o
	}
	w, wk := width(os)
	for i, k := range f.kinds {
		switch k {
		case kL:
			os[i] = operand{k: kL, i: int64(r.n(2))}
		case kN, kI:
			os[i] = operand{k: k, i: genInt(r, f, k, w, wk, edge, it)}
		}
	}
	return os
}

// sameLength reports whether f needs vectors of one length.
func sameLength(f fn) bool {
	switch f.name {
	case "and", "or", "xor", "nand", "nor", "xnor", "std_match":
		return true
	}
	return false
}

func genInt(r *rng, f fn, k pkind, w int, wk pkind, edge bool,
	it int,
) int64 {
	switch f.name {
	case "shift_left", "shift_right", "rotate_left", "rotate_right":
		return int64(r.n(w + 3))
	case "sll", "srl", "sla", "sra", "rol", "ror":
		return int64(r.n(2*w+7) - (w + 3))
	case "resize":
		return int64(r.n(w + 6))
	case "to_unsigned", "to_signed":
		return int64(1 + r.n(40))
	}
	// An integer that fits the vector it meets (to_unsigned and
	// to_signed get their value from their size, below).
	bits := min(w, 31)
	if w == 0 {
		bits = 31
	}
	lo, hi := int64(0), int64(1)<<bits-1
	if k == kI || wk == kS {
		lo, hi = -(int64(1) << (bits - 1)), int64(1)<<(bits-1)-1
		if k == kN {
			lo = 0
		}
	}
	if edge {
		return []int64{0, hi, lo, min(1, hi)}[it%4]
	}
	return lo + int64(r.n(int(min(hi-lo+1, 1<<31))))
}

// fixConversions makes the value of to_unsigned and to_signed fit its
// size.
func fixConversions(r *rng, f fn, os []operand) {
	if f.name != "to_unsigned" && f.name != "to_signed" {
		return
	}
	n := int(os[1].i)
	if os[1].k <= kS {
		n = os[1].n
	}
	n = min(n, 31)
	if f.name == "to_unsigned" {
		os[0].i = int64(r.n(1 << uint(min(n, 30))))
		return
	}
	half := int64(1) << uint(min(n-1, 30))
	os[0].i = int64(r.n(int(2*half))) - half
}

// groups are the functions the check covers, by name.
var groups = map[string][]string{
	"arithmetic": {"+", "-", "*", "/", "rem", "mod", "abs"},
	"compare": {"=", "/=", "<", "<=", ">", ">=", "?=", "?/=", "?<", "?<=",
		"?>", "?>=", "maximum", "minimum", "std_match"},
	"shift": {"shift_left", "shift_right", "rotate_left", "rotate_right",
		"sll", "srl", "sla", "sra", "rol", "ror"},
	"resize":     {"resize"},
	"conversion": {"to_integer", "to_unsigned", "to_signed"},
	"logical":    {"and", "or", "xor", "nand", "nor", "xnor", "not"},
}

// TestNumericStdAgainstBig runs every covered overload on generated
// operands and compares with math/big.
func TestNumericStdAgainstBig(t *testing.T) { againstBig(t, false) }

// TestNumericStdNativeAgainstBig is the same check of the native
// bodies (#59): the calls they take run in Go, the others from source.
func TestNumericStdNativeAgainstBig(t *testing.T) { againstBig(t, true) }

func againstBig(t *testing.T, native bool) {
	e := load(t, native)
	iters := 60
	if testing.Short() {
		iters = 20
	}
	covered := map[string]bool{}
	r := &rng{env.SplitMix64{State: 35}}
	for _, f := range e.fns {
		group := ""
		for g, names := range groups {
			if slices.Contains(names, f.name) {
				group = g
			}
		}
		if group == "" {
			continue
		}
		if f.kinds[0] == kV || len(f.kinds) > 1 && f.kinds[1] == kV {
			continue // std_ulogic_vector std_match: compare group only
		}
		ran := 0
		for it := range iters {
			os := gen(r, f, it)
			fixConversions(r, f, os)
			want, ok := model(f, os)
			if !ok {
				continue
			}
			args := make([]eval.Value, len(os))
			for i, o := range os {
				args[i] = e.value(f.params[i], o)
			}
			b := eval.NewBudget(50_000_000)
			v, err := e.ev.Call(f.d, args, b)
			if err != nil {
				t.Fatalf("%s %v: %v", f.sig(), show(os), err)
			}
			got, err := decode(f.res, v)
			if err != nil {
				t.Fatalf("%s %v: %v", f.sig(), show(os), err)
			}
			if !got.equal(want) {
				t.Fatalf("%s %v: got %v, want %v", f.sig(), show(os), got,
					want)
			}
			ran++
		}
		if ran > 0 {
			covered[f.sig()] = true
		}
	}
	// Every named function has at least one overload checked.
	for g, names := range groups {
		for _, n := range names {
			found := false
			for s := range covered {
				found = found || strings.HasPrefix(s, n+"(")
			}
			if !found {
				t.Errorf("%s: no overload of %s was checked", g, n)
			}
		}
	}
	t.Logf("%d overloads checked", len(covered))
}

func show(os []operand) string {
	var s []string
	for _, o := range os {
		switch o.k {
		case kU, kS, kV:
			s = append(s, fmt.Sprintf("%d'%s", o.n, o.bits.Text(2)))
		default:
			s = append(s, fmt.Sprint(o.i))
		}
	}
	return strings.Join(s, ", ")
}
