package ghw

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
)

// view is what a GHW file says about a trace: its types, scopes,
// record signals, scalar and array signals and processes, by path.
type view struct {
	types   []string
	scopes  []string // "path kind"
	records []string // "path unit"
	signals []vsig
	procs   []string
}

type vsig struct {
	path string
	desc string // "type dir [left dir right]"
	nums []int  // scalar signal numbers, left to right
}

func typeDesc(ty trace.Type) string {
	s := ty.Name + " " + ty.Kind.String()
	switch ty.Kind {
	case trace.Enum:
		s += " " + strings.Join(ty.Literals, " ")
	case trace.Integer:
		s += fmt.Sprintf(" %d %d", ty.Low, ty.High)
	case trace.Physical:
		s += fmt.Sprintf(" %d %d", ty.Low, ty.High)
		for _, u := range ty.Units {
			s += fmt.Sprintf(" %s %d", u.Name, u.Mult)
		}
	case trace.Array:
		s += " " + ty.Elem
	}
	return s
}

func sigDesc(typ string, dir trace.Dir, arr bool, l, r int64,
	downto bool,
) string {
	s := typ + " " + dir.String()
	if arr {
		d := "to"
		if downto {
			d = "downto"
		}
		s += fmt.Sprintf(" %d %s %d", l, d, r)
	}
	return s
}

var hieScopes = map[byte]trace.ScopeKind{
	hieInstance: trace.Instance, hieBlock: trace.Block,
	hieForGen: trace.ForGenerate, hieIfGen: trace.IfGenerate,
	hiePackage: trace.Package,
}

var hieDirs = map[byte]trace.Dir{
	hieSignal: trace.Internal, hiePortIn: trace.In, hiePortOut: trace.Out,
	hiePortInout: trace.Inout, hiePortBuf: trace.Buffer,
	hiePortLink: trace.Linkage,
}

// decode reads the view out of a parsed file and checks the order the
// writer promises: in every scope the signals come first, then the
// scopes and processes, each group by name.
func decode(f *ghwFile) (*view, error) {
	v := &view{}
	for _, t := range f.types {
		if t.name == "" {
			continue
		}
		ty := trace.Type{Name: t.name}
		switch t.kind {
		case rtikB2, rtikE8:
			if t.wkt == wktStdULogic {
				continue // the base of the logic subtypes
			}
			ty.Kind, ty.Literals = trace.Enum, t.lits
		case rtikSubScalar:
			b := f.typ(t.base)
			switch b.kind {
			case rtikE8:
				if b.wkt != wktStdULogic || t.rng.lo != 0 || t.rng.hi != 8 {
					return nil, fmt.Errorf("subtype %s of enum %s",
						t.name, b.name)
				}
				ty.Kind = trace.Logic
			case rtikI32, rtikI64:
				ty.Kind, ty.Low, ty.High = trace.Integer, t.rng.lo, t.rng.hi
			case rtikP64:
				ty.Kind, ty.Low, ty.High = trace.Physical, t.rng.lo,
					t.rng.hi
				if len(b.units) == 0 {
					ty.Kind = trace.Integer // beyond 32 bits
				}
				for _, u := range b.units {
					ty.Units = append(ty.Units, trace.Unit{
						Name: u[0].(string), Mult: u[1].(int64)})
				}
			case rtikF64:
				ty.Kind = trace.Real
			default:
				return nil, fmt.Errorf("subtype %s of kind %d", t.name,
					b.kind)
			}
			if b.name != t.name && b.kind != rtikE8 {
				return nil, fmt.Errorf("subtype %s of base %s", t.name,
					b.name)
			}
		case rtikArray:
			ty.Kind, ty.Elem = trace.Array, f.typ(t.elem).name
		default:
			continue // bases, records and anonymous subtypes
		}
		v.types = append(v.types, typeDesc(ty))
	}
	slices.Sort(v.types)
	var walk func(hs []*rhie, prefix string) error
	var leaf func(path string, typ int, dir trace.Dir, nums []int)
	leaf = func(path string, typ int, dir trace.Dir, nums []int) {
		t := f.typ(typ)
		switch t.kind {
		case rtikRecord:
			v.records = append(v.records, path+" "+t.name)
			off := 0
			for _, fl := range t.fields {
				n := f.scalars(fl.typ)
				leaf(path+"."+fl.name, fl.typ, dir, nums[off:off+n])
				off += n
			}
		case rtikSubArray:
			v.signals = append(v.signals, vsig{path: path,
				desc: sigDesc(f.typ(t.base).name, dir, true, t.rng.lo,
					t.rng.hi, t.rng.downto), nums: nums})
		default:
			v.signals = append(v.signals, vsig{path: path,
				desc: sigDesc(t.name, dir, false, 0, 0, false),
				nums: nums})
		}
	}
	walk = func(hs []*rhie, prefix string) error {
		phase, last := 0, ""
		for _, h := range hs {
			name := h.name
			if h.kind == hieForGen {
				if f.typ(f.baseOf(h.iterType)).kind != rtikI32 {
					return fmt.Errorf("for-generate %s iterator kind",
						name)
				}
				name += "(" + strconv.FormatInt(h.iter.i, 10) + ")"
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			p := 1
			if _, ok := hieDirs[h.kind]; ok {
				p = 0
			}
			if p < phase || p == phase && name < last {
				return fmt.Errorf("%s out of order", path)
			}
			phase, last = p, name
			switch {
			case h.kind == hieProcess:
				v.procs = append(v.procs, path)
			case p == 0:
				leaf(path, h.typ, hieDirs[h.kind], h.sigs)
			default:
				v.scopes = append(v.scopes,
					path+" "+hieScopes[h.kind].String())
				if err := walk(h.kids, path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(f.top, ""); err != nil {
		return nil, err
	}
	slices.Sort(v.scopes)
	slices.Sort(v.records)
	slices.Sort(v.procs)
	slices.SortFunc(v.signals, func(a, b vsig) int {
		return strings.Compare(a.path, b.path)
	})
	return v, nil
}

// prefixOf reports whether path q lies strictly under path p, both as
// SplitPath elements.
func prefixOf(p, q []string) bool {
	return len(q) > len(p) && slices.Equal(p, q[:len(p)])
}

var forName = regexp.MustCompile(`^(.+)\((0|-?[1-9][0-9]*)\)$`)

// expect is the view the package documentation promises for tr,
// computed from the trace alone.
func expect(tr *trace.Trace) *view {
	v := &view{}
	for _, ty := range tr.Types {
		v.types = append(v.types, typeDesc(ty))
	}
	slices.Sort(v.types)
	scope := map[string]*trace.Scope{}
	for i := range tr.Scopes {
		scope[tr.Scopes[i].Path] = &tr.Scopes[i]
	}
	// Every level: the scopes and the proper prefixes of every path.
	levels := map[string][]string{}
	note := func(path string, self bool) {
		parts := trace.SplitPath(path)
		n := len(parts) - 1
		if self {
			n++
		}
		for k := 1; k <= n; k++ {
			levels[strings.Join(parts[:k], ".")] = parts[:k]
		}
	}
	for _, s := range tr.Scopes {
		note(s.Path, true)
	}
	for _, s := range tr.Signals {
		note(s.Path, false)
	}
	for _, p := range tr.Processes {
		note(p.Path, false)
	}
	memo := map[string]bool{}
	var recordable func(path string) bool
	recordable = func(path string) bool {
		if r, ok := memo[path]; ok {
			return r
		}
		p := levels[path]
		ok := scope[path] != nil && scope[path].Kind == trace.Record
		sigs := 0
		for _, s := range tr.Signals {
			if prefixOf(p, trace.SplitPath(s.Path)) {
				sigs++
			}
		}
		for _, pr := range tr.Processes {
			if prefixOf(p, trace.SplitPath(pr.Path)) {
				ok = false
			}
		}
		for l, lp := range levels {
			if ok && prefixOf(p, lp) && !recordable(l) {
				ok = false
			}
		}
		memo[path] = ok && sigs > 0
		return memo[path]
	}
	// outer returns the outermost recordable level holding parts.
	outer := func(parts []string) []string {
		for k := 1; k < len(parts); k++ {
			if recordable(strings.Join(parts[:k], ".")) {
				return parts[:k]
			}
		}
		return nil
	}
	for path, parts := range levels {
		if recordable(path) {
			v.records = append(v.records, path+" "+scope[path].Unit)
			continue
		}
		if outer(parts) != nil {
			continue
		}
		kind := trace.Instance
		if s := scope[path]; s != nil {
			kind = s.Kind
		}
		switch kind {
		case trace.Record:
			kind = trace.Block
		case trace.CaseGenerate:
			kind = trace.IfGenerate
		case trace.ForGenerate:
			m := forName.FindStringSubmatch(parts[len(parts)-1])
			if m == nil {
				kind = trace.Block
			} else if n, err := strconv.ParseInt(m[2], 10, 64); err != nil ||
				n < math.MinInt32 || n > math.MaxInt32 {
				kind = trace.Block
			}
		}
		v.scopes = append(v.scopes, path+" "+kind.String())
	}
	for i, s := range tr.Signals {
		dir := s.Dir
		if o := outer(trace.SplitPath(s.Path)); o != nil {
			for _, t := range tr.Signals {
				if prefixOf(o, trace.SplitPath(t.Path)) && t.Dir != s.Dir {
					dir = trace.Inout
				}
			}
		}
		v.signals = append(v.signals, vsig{path: s.Path,
			desc: sigDesc(s.Type, dir, tr.SignalType(i).Kind ==
				trace.Array, s.Left, s.Right, s.Downto)})
	}
	for _, p := range tr.Processes {
		v.procs = append(v.procs, p.Path)
	}
	slices.Sort(v.scopes)
	slices.Sort(v.records)
	slices.Sort(v.procs)
	return v
}

func diffLists(what string, got, want []string) error {
	if slices.Equal(got, want) {
		return nil
	}
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return fmt.Errorf("%s %d: got %q, want %q (%d vs %d)", what,
				i, g, w, len(got), len(want))
		}
	}
	return nil
}

// firstValue is the value a scalar of type ty shows before its first
// change, as the package documentation says.
func firstValue(ty *trace.Type) trace.Value {
	if ty.Kind == trace.Integer || ty.Kind == trace.Physical {
		return trace.Int(ty.Low)
	}
	return trace.Value{}
}

func sameValue(r rval, v trace.Value) bool {
	return r.i == v.Int &&
		math.Float64bits(r.f) == math.Float64bits(v.Real)
}

// roundTrip exports tr, reads the file back and compares types,
// hierarchy and values with the trace.
func roundTrip(tr *trace.Trace) (*ghwFile, []byte, error) {
	data, err := Exporter{}.Export(tr)
	if err != nil {
		return nil, nil, err
	}
	f, err := parseGHW(data)
	if err != nil {
		return nil, data, err
	}
	got, err := decode(f)
	if err != nil {
		return f, data, err
	}
	want := expect(tr)
	if err := diffLists("type", got.types, want.types); err != nil {
		return f, data, err
	}
	if err := diffLists("scope", got.scopes, want.scopes); err != nil {
		return f, data, err
	}
	if err := diffLists("record", got.records, want.records); err != nil {
		return f, data, err
	}
	if err := diffLists("process", got.procs, want.procs); err != nil {
		return f, data, err
	}
	var gs, ws []string
	for _, s := range got.signals {
		gs = append(gs, s.path+" "+s.desc)
	}
	for _, s := range want.signals {
		ws = append(ws, s.path+" "+s.desc)
	}
	if err := diffLists("signal", gs, ws); err != nil {
		return f, data, err
	}
	return f, data, checkValues(tr, f, got)
}

// checkValues replays the snapshot and cycles against trace.Settle.
func checkValues(tr *trace.Trace, f *ghwFile, v *view) error {
	type owner struct{ sig, elem int }
	own := make([]owner, f.nbasic+1)
	numsOf := make([][]int, len(tr.Signals))
	for _, s := range v.signals {
		i, ok := tr.Lookup(s.path)
		if !ok {
			return fmt.Errorf("no signal %s", s.path)
		}
		arr := tr.SignalType(i).Kind == trace.Array
		if !arr && len(s.nums) != 1 ||
			arr && len(s.nums) != tr.Signals[i].Len() {
			return fmt.Errorf("%s has %d scalars", s.path, len(s.nums))
		}
		numsOf[i] = s.nums
		for k, n := range s.nums {
			if !arr {
				k = -1
			}
			own[n] = owner{i, k}
		}
	}
	want := make([]trace.Value, f.nbasic+1)
	for n := 1; n <= f.nbasic; n++ {
		ty := tr.SignalType(own[n].sig)
		if own[n].elem >= 0 {
			ty = tr.ElemType(ty)
		}
		want[n] = firstValue(ty)
	}
	set := func(st trace.Step) []int {
		var touched []int
		for _, c := range st.Changes {
			for _, n := range numsOf[c.Signal] {
				x := c.Value
				if own[n].elem >= 0 {
					x = c.Value.Elems[own[n].elem]
				}
				if !want[n].Equal(x) {
					want[n] = x
					touched = append(touched, n)
				}
			}
		}
		slices.Sort(touched)
		return touched
	}
	steps := trace.Settle(tr)
	if len(steps) > 0 && steps[0].Time == 0 {
		set(steps[0])
		steps = steps[1:]
	}
	if f.snapTime != 0 {
		return fmt.Errorf("snapshot at %d", f.snapTime)
	}
	got := slices.Clone(f.snap)
	for n := 1; n <= f.nbasic; n++ {
		if !sameValue(got[n], want[n]) {
			return fmt.Errorf("snapshot #%d: got %v, want %v", n, got[n],
				want[n])
		}
	}
	times := []trace.Time{}
	for _, st := range steps {
		times = append(times, st.Time)
	}
	last := trace.Time(0)
	if len(times) > 0 {
		last = times[len(times)-1]
	}
	if tr.Header.End > last {
		times = append(times, tr.Header.End)
	}
	if len(f.cycles) != len(times) {
		return fmt.Errorf("%d cycles, want %d", len(f.cycles), len(times))
	}
	for j, c := range f.cycles {
		if trace.Time(c.time) != times[j] {
			return fmt.Errorf("cycle %d at %d, want %d", j, c.time,
				times[j])
		}
		var touched []int
		if j < len(steps) {
			touched = set(steps[j])
		}
		if !slices.Equal(c.nums, touched) {
			return fmt.Errorf("cycle at %d changes %v, want %v", c.time,
				c.nums, touched)
		}
		for k, n := range c.nums {
			if c.vals[k].i == got[n].i &&
				math.Float64bits(c.vals[k].f) == math.Float64bits(got[n].f) {
				return fmt.Errorf("cycle at %d restates #%d", c.time, n)
			}
			got[n] = c.vals[k]
			if !sameValue(got[n], want[n]) {
				return fmt.Errorf("cycle at %d: #%d is %v, want %v",
					c.time, n, got[n], want[n])
			}
		}
	}
	return nil
}

// check fails t unless tr survives the round trip.
func check(t *testing.T, tr *trace.Trace) (*ghwFile, []byte) {
	t.Helper()
	f, data, err := roundTrip(tr)
	if err != nil {
		t.Fatal(err)
	}
	return f, data
}
