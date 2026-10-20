package elab

import (
	"maps"
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// world is the design-wide state every process shares: shared and
// package variables, the heap of allocated objects (access values are
// handles into it, numbered in allocation order), protected objects
// and files. Its contents are values; fork copies them.
type world struct {
	d    *Design
	o    InterpOptions
	prog *progCache

	shared map[*sema.Decl]slot // lookup only
	heap   map[int64]eval.Value
	objs   map[int64]*protObj
	files  map[int64]*fileState
	// disk is what the design wrote, by external name: a file written
	// and closed is read back from here.
	disk map[string][]byte
	next int64 // the last handle given out

	// copy is the world the current Kernel.Clone pass copies into, and
	// copied the processes already cloned into it: each pass clones
	// every process once, in order.
	copy   *world
	copied map[int]bool
}

// protObj is one protected object: its variables.
type protObj struct {
	typ  *sema.Type
	vars map[*sema.Decl]slot
}

func newWorld(d *Design, o InterpOptions) *world {
	return &world{d: d, o: o, prog: newProgCache(d),
		shared: map[*sema.Decl]slot{}, heap: map[int64]eval.Value{},
		objs: map[int64]*protObj{}, files: map[int64]*fileState{},
		disk: map[string][]byte{}}
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
		w.copy, w.copied = &c, map[int]bool{}
	}
	w.copied[idx] = true
	return w.copy
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
			Msg: "dereference of a deallocated access value " +
				strconv.FormatInt(p.I, 10)}
	}
	return v, nil
}

func (w *world) free(p eval.Value) {
	delete(w.heap, p.I)
}
