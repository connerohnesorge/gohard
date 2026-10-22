package elab

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// interp_names.go: the name attributes 'SIMPLE_NAME, 'PATH_NAME and
// 'INSTANCE_NAME (LRM 16.2.5) in simulation. hier.Namer builds the
// strings (hier/names.go, NVC 1.23's forms); the interpreter supplies
// the instance the running code belongs to and, inside a protected
// type (VHDL-2019), the protected object's own path:
//
//	:tb:sv:m[integer]:     method m of shared variable sv
//	:tb:sv:in1:z           z of the private object in1 of sv
//	:tb:p:v:z              z of the object of variable v of process p
//	:tb:_p0::z             z of an allocated object: the empty name in
//	                       the running process (unlabeled ones are
//	                       _p0, _p1, ... as NVC names them)
//
// An object's path is found from where its handle is stored, which is
// one place, so no string depends on a handle or on the schedule.

// nameAttr reports whether e is a name attribute, whose value only the
// interpreter knows (it depends on the instance and the object).
func nameAttr(e *sema.Expr) bool { return hier.NameAttr(e) }

// lowerName lowers the name attribute e to its string.
func (m *machine) lowerName(ev *sema.Evaluator, e *sema.Expr,
) (eval.Expr, bool, error) {
	if !nameAttr(e) {
		return nil, false, nil
	}
	s, err := namer{m: m, ev: ev}.namer().Value(e)
	if err != nil {
		return nil, true, err
	}
	return &eval.Const{V: eval.Str(s)}, true, nil
}

// namer is the naming context of process m. obj, when not 0, is the
// protected object a declaration inside a protected type belongs to
// (else the innermost method frame of that type decides); in, when
// set, is the instance of the design regions (for an object found in
// the world rather than in the process).
type namer struct {
	m *machine
	// ev is the evaluator e is lowered in: the static evaluator's
	// frame of a pure call knows the subprogram instance it runs.
	ev    *sema.Evaluator
	obj   int64
	in    *hier.Instance
	depth int
}

// maxNameDepth bounds the nesting of protected objects a name walks.
const maxNameDepth = 64

func (n namer) namer() hier.Namer {
	return hier.Namer{Find: n.find, Protected: n.protected,
		Instance: n.instance}
}

// instance returns the subprogram instance whose call runs the body of
// the generic subprogram g: the innermost of the static evaluator's
// calls, else of the process's frames.
func (n namer) instance(g *sema.Decl) *sema.Decl {
	if n.ev != nil {
		if x := n.ev.RunningInstance(g); x != nil {
			return x
		}
	}
	for i := len(n.m.frames) - 1; i >= 0; i-- {
		f := n.m.frames[i]
		if f.sub == nil || f.call == nil || f.call.Decl == nil ||
			!sema.SameSubprogram(f.sub, g) {
			continue
		}
		x := f.call.Decl.Denotes()
		if sema.InstanceOf(x) != nil {
			return x
		}
		return nil
	}
	return nil
}

// find returns the instance that elaborates design region r: n.in or
// one of its parents, else the process's instance (the object's
// instance while a shared object is elaborated) or one of its parents.
func (n namer) find(r *sema.Region) *hier.Instance {
	start := n.in
	if start == nil {
		start = n.m.inst()
		if n.m.elab.d != nil {
			start = n.m.elab.in
		}
	}
	for in := start; in != nil; in = n.m.w.d.Parent(in) {
		if declares(in, r) {
			return in
		}
	}
	return nil
}

// protected returns the names of the protected object whose type
// declares (or whose body is) r: n.obj, else the object of the
// innermost method frame of that type.
func (n namer) protected(r *sema.Region) (hier.Names, error) {
	w := n.m.w
	of := func(h int64) bool {
		o := w.objs[h]
		if o == nil {
			return false
		}
		if r.Kind == sema.RegionProtected {
			return o.typ.Base().Members == r
		}
		return w.protectedBody(o.typ) == r
	}
	h := n.obj
	if h == 0 || !of(h) {
		h = 0
		for i := len(n.m.frames) - 1; i >= 0; i-- {
			if f := n.m.frames[i]; f.obj != 0 && of(f.obj) {
				h = f.obj
				break
			}
		}
	}
	if h == 0 {
		return hier.Names{}, unnamedErr("a protected type member " +
			"outside a call of its object's methods")
	}
	return n.object(h)
}

func unnamedErr(what string) error {
	return &nativeError{name: "'PATH_NAME and 'INSTANCE_NAME of " + what}
}

// object returns the names of protected object h, from the one place
// its handle is stored: a shared variable (or a variable port's
// actual) or design-region object, a variable of another protected
// object, a variable of the process's frames (outermost first), or a
// heap cell (an allocated object). An element of a composite adds its
// index or field.
func (n namer) object(h int64) (hier.Names, error) {
	if n.depth >= maxNameDepth {
		return hier.Names{}, unnamedErr("protected objects nested " +
			strconv.Itoa(maxNameDepth) + " deep")
	}
	w, m := n.m.w, n.m
	type hit struct {
		ns  hier.Names
		err error
	}
	var hits []hit
	try := func(sub namer, d *sema.Decl, v eval.Value) {
		suffix, ok := holds(v, h)
		if !ok {
			return
		}
		sub.m, sub.depth = m, n.depth+1
		ns, err := sub.namer().Entity(d)
		hits = append(hits, hit{ns.Add(suffix), err})
	}
	for _, k := range slices.SortedFunc(maps.Keys(w.shared),
		compareShared) {
		if k.d.Kind != sema.DeclFile {
			try(namer{in: k.in}, k.d, w.shared[k].v)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(w.objs)) {
		vars := w.objs[p].vars
		for _, d := range slices.SortedFunc(maps.Keys(vars),
			compareDecl) {
			try(namer{obj: p}, d, vars[d].v)
		}
	}
	for _, f := range m.frames {
		if len(hits) > 0 {
			break
		}
		for _, d := range slices.SortedFunc(maps.Keys(f.vars),
			compareDecl) {
			// A formal names its actual's object, found before it.
			if d.Kind == sema.DeclVariable ||
				d.Kind == sema.DeclConstant {
				try(namer{obj: f.obj}, d, f.vars[d].v)
			}
		}
	}
	if len(hits) == 0 {
		for _, k := range slices.Sorted(maps.Keys(w.heap)) {
			if _, ok := holds(w.heap[k], h); ok {
				return m.process().Add(":"), nil
			}
		}
		return hier.Names{}, unnamedErr("a protected object no " +
			"variable holds")
	}
	// One place holds a handle; should a world hold it twice, the least
	// name wins, never the map order.
	slices.SortFunc(hits, func(a, b hit) int {
		return strings.Compare(a.ns.Path+"\x00"+a.ns.Inst,
			b.ns.Path+"\x00"+b.ns.Inst)
	})
	return hits[0].ns, hits[0].err
}

// process returns the names of the running process with its
// elaborated name (unlabeled ones are _p0, _p1, ...).
func (m *machine) process() hier.Names {
	p := m.prog.path
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		p = p[i+1:]
	}
	p = ":" + hier.Lower(p)
	if in := m.inst(); in != nil {
		return in.Names().Add(p)
	}
	return hier.Names{Path: p, Inst: p}
}

// holds returns where value v holds protected object h: "" for v
// itself, "(i)" for an array element ("(i,j)" in more dimensions),
// ".f" for a record field, nested.
func holds(v eval.Value, h int64) (string, bool) {
	if v.T == nil {
		return "", false
	}
	switch v.Kind() {
	case eval.KindProtected:
		return "", v.I == h
	case eval.KindArray:
		for i, e := range v.E {
			if s, ok := holds(e, h); ok {
				return "(" + indexImage(v.B, int64(i)) + ")" + s, true
			}
		}
	case eval.KindRecord:
		fs := v.T.BaseType().Fields
		for i, e := range v.E {
			s, ok := holds(e, h)
			if !ok {
				continue
			}
			f := strconv.Itoa(i)
			if i < len(fs) {
				f = hier.Lower(fs[i].Name)
			}
			return "." + f + s, true
		}
	}
	return "", false
}

// indexImage is the index of flat element i of an array with bounds
// bs, row-major, as "i" or "i,j".
func indexImage(bs []eval.Range, i int64) string {
	if len(bs) == 0 {
		return strconv.FormatInt(i, 10)
	}
	ix := make([]string, len(bs))
	for d := len(bs) - 1; d >= 0; d-- {
		l := max(bs[d].Len(), 1)
		ix[d] = strconv.FormatInt(bs[d].At(i%l), 10)
		i /= l
	}
	return strings.Join(ix, ",")
}
