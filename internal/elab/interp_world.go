package elab

import (
	"maps"
	"slices"

	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// world is the design-wide state every process shares: shared and
// package variables, the heap of allocated objects (access values are
// handles into it, numbered in allocation order), protected objects
// and files. Its contents are values; fork copies them.
//
// Handles are opaque: they are never printed, never reused (the
// counter only grows) and never compared across worlds. The shared
// variables are created before any process runs, in the canonical
// order of sharedOrder, so the handles of their protected objects do
// not depend on which process touches them first (#18).
type world struct {
	d    *Design
	o    InterpOptions
	prog *progCache

	// shared are the shared variables and package or architecture
	// files, one per declaring instance (nil for packages); lookup
	// only.
	shared map[sharedKey]slot
	// eager is set once the shared variables were created
	// (machine.elabShared); order is sharedOrder, computed once.
	eager bool
	order []sharedKey
	// prot memoizes holdsProtected by sema type; lookup only.
	prot map[*sema.Type]bool
	// rtConst memoizes runtimeConst by declaration; lookup only.
	rtConst map[*sema.Decl]bool
	heap    map[int64]eval.Value
	objs    map[int64]*protObj
	files   map[int64]*fileState
	// disk is what the design wrote, by external name: a file written
	// and closed is read back from here.
	disk map[string][]byte
	next int64 // the last handle given out
	// mirrors are the std.reflection mirror objects (interp_reflect.go).
	mirrors mirrorState

	// copy is the world the current Kernel.Clone pass copies into, and
	// copied the processes already cloned into it: each pass clones
	// every process once, in order.
	copy   *world
	copied map[int]bool
}

// sharedKey names one shared object: the declaration and the
// instance that elaborated it (nil for a package object), so an
// architecture instantiated twice has two objects.
type sharedKey struct {
	in *hier.Instance
	d  *sema.Decl
}

// protObj is one protected object: its variables.
type protObj struct {
	typ  *sema.Type
	vars map[*sema.Decl]slot
}

func newWorld(d *Design, o InterpOptions) *world {
	return &world{d: d, o: o, prog: newProgCache(d),
		shared: map[sharedKey]slot{}, heap: map[int64]eval.Value{},
		prot: map[*sema.Type]bool{}, rtConst: map[*sema.Decl]bool{},
		objs: map[int64]*protObj{}, files: map[int64]*fileState{},
		disk: map[string][]byte{}, mirrors: newMirrorState()}
}

// fork returns the world process idx's clone uses: a copy shared by
// every process cloned in the same pass.
func (w *world) fork(idx int) *world {
	if w.copy == nil || w.copied[idx] {
		c := *w
		c.copy, c.copied = nil, nil
		c.shared = maps.Clone(w.shared)
		c.heap = maps.Clone(w.heap)
		c.objs = make(map[int64]*protObj, len(w.objs))
		for h, o := range w.objs {
			c.objs[h] = &protObj{typ: o.typ, vars: maps.Clone(o.vars)}
		}
		c.files = make(map[int64]*fileState, len(w.files))
		for h, f := range w.files {
			cf := *f
			cf.data = slices.Clone(f.data)
			c.files[h] = &cf
		}
		c.disk = maps.Clone(w.disk)
		c.mirrors = w.mirrors.clone()
		w.copy, w.copied = &c, map[int]bool{}
	}
	w.copied[idx] = true
	return w.copy
}

// sharedOrder returns every shared variable of the design in the
// canonical order they are created: those of the packages the design
// depends on (by dependency level, then unit order; package bodies
// after their package), then those of each instance in hierarchy
// preorder, in declaration order. Generic packages and package
// instances are left to their first use.
func (w *world) sharedOrder() []sharedKey {
	if w.order != nil || w.d.Top == nil {
		return w.order
	}
	var insts []*hier.Instance
	var walk func(in *hier.Instance)
	walk = func(in *hier.Instance) {
		insts = append(insts, in)
		for _, c := range in.Children {
			walk(c)
		}
	}
	walk(w.d.Top)
	seen := map[*sema.Unit]bool{} // lookup only
	var units []*sema.Unit
	var add func(u *sema.Unit)
	add = func(u *sema.Unit) {
		if u == nil || seen[u] {
			return
		}
		seen[u] = true
		units = append(units, u)
		for _, x := range u.Deps {
			add(x)
		}
		if u.Kind != sema.UnitPackage {
			return
		}
		if l, ok := w.d.r.Library(u.Library); ok {
			for _, b := range l.Secondary(u.Name) {
				add(b)
			}
		}
	}
	for _, in := range insts {
		if in.Kind == hier.KindEntity {
			if in.Entity != nil {
				add(in.Entity.Unit)
			}
			add(in.Arch)
		}
	}
	slices.SortStableFunc(units, func(a, b *sema.Unit) int {
		if a.Level != b.Level {
			return a.Level - b.Level
		}
		return a.Index - b.Index
	})
	order := []sharedKey{}
	for _, u := range units {
		if (u.Kind == sema.UnitPackage || u.Kind == sema.UnitPackageBody) &&
			!w.genericPackage(u) {
			order = w.appendShared(order, nil, u.Region)
		}
	}
	for _, in := range insts {
		for _, r := range instRegions(in) {
			order = w.appendShared(order, in, r)
		}
	}
	w.order = order
	return order
}

// appendShared appends the shared variables declared directly in r,
// and the constants whose value the interpreter computes
// (runtimeConst), in declaration order.
func (w *world) appendShared(ks []sharedKey, in *hier.Instance,
	r *sema.Region,
) []sharedKey {
	if r == nil {
		return ks
	}
	ev := w.d.Top.Evaluator()
	if in != nil {
		ev = in.Evaluator()
	}
	for _, d := range r.Decls {
		if d.Kind == sema.DeclSharedVariable || w.runtimeConst(ev, d) {
			ks = append(ks, sharedKey{in, d})
		}
	}
	return ks
}

// genericPackage reports whether u is a generic package or the body
// of one.
func (w *world) genericPackage(u *sema.Unit) bool {
	if u.Kind == sema.UnitPackageBody {
		l, ok := w.d.r.Library(u.Library)
		if !ok {
			return false
		}
		p, ok := l.Unit(u.Of)
		if !ok {
			return false
		}
		u = p
	}
	if u.Region == nil {
		return false
	}
	for _, d := range u.Region.Decls {
		if d.Kind == sema.DeclGeneric {
			return true
		}
	}
	return false
}

// handle returns a new heap, object or file handle.
func (w *world) handle() int64 {
	w.next++
	return w.next
}

// alloc stores v on the heap and returns its access value of type t.
func (w *world) alloc(t *eval.Type, v eval.Value) eval.Value {
	h := w.handle()
	w.heap[h] = v
	return eval.Value{T: t, I: h}
}

// deref returns the object access value p designates.
func (w *world) deref(p eval.Value) (eval.Value, error) {
	if p.I == 0 {
		return eval.Value{}, &eval.Error{Code: eval.ErrCodeNull,
			Msg: "dereference of a null access value"}
	}
	v, ok := w.heap[p.I]
	if !ok {
		return eval.Value{}, &eval.Error{Code: eval.ErrCodeNull,
			Msg: "dereference of a deallocated access value"}
	}
	return v, nil
}

// free deallocates the object access value p designates, and the
// protected objects it holds (LRM 5.4.3): a handle into a freed object
// no longer resolves. Objects designated by access values inside it
// are other heap objects and stay.
func (w *world) free(p eval.Value) {
	v, ok := w.heap[p.I]
	if !ok {
		return
	}
	delete(w.heap, p.I)
	w.freeObjects(v)
}

// freeObjects frees the protected objects value v holds (its protected
// leaves) and, recursively, the objects their variables hold.
func (w *world) freeObjects(v eval.Value) {
	if v.T != nil && v.T.Kind == eval.KindProtected {
		o, ok := w.objs[v.I]
		if !ok {
			return
		}
		delete(w.objs, v.I)
		// The variables in declaration order: private variables of
		// the declaration (2019), then the body's.
		for _, r := range []*sema.Region{o.typ.Base().Members,
			w.protectedBody(o.typ)} {
			if r == nil {
				continue
			}
			for _, d := range r.Decls {
				if s, ok := o.vars[d]; ok {
					w.freeObjects(s.v)
				}
			}
		}
		return
	}
	for _, e := range v.E {
		w.freeObjects(e)
	}
}
