package elab

// gc.go is the garbage collector of the access heap (VHDL-2019, #27):
// a deterministic mark and sweep of the objects allocators made
// (world.heap) and of the protected objects (world.objs) nothing can
// reach any more.
//
// Determinism is by construction:
//   - a collection is triggered by allocation counters only, never by
//     Go memory or time: once the world has given out
//     max(DefaultGCMin, live after the last collection) handles since
//     the last collection (GCOptions.Every to force another period);
//   - it runs at a safe point only: before a statement of a process
//     when no function is running (machine.sync == 0), so no Go
//     variable holds a handle the roots do not show. Every other
//     process of the world is suspended (the kernel is single
//     threaded). A function that allocates in a loop without returning
//     therefore grows the heap until it returns;
//   - handles are never reused (world.next only grows), so a swept
//     handle can never be confused with a new object, and a dangling
//     access value DEALLOCATE left still reports "deallocated";
//   - the result of a mark is a set, the same whatever order the roots
//     are visited in; the roots are still visited in a fixed order (map
//     keys sorted), as detlint requires;
//   - a collection charges no step budget, so a run's steps, its
//     sim/budget-exceeded point and its trace are the same bytes with
//     the collector forced at every allocation, at the default period
//     or off.
//
// Only unreachable objects are swept, and freeing one is not
// observable: neither heap objects nor protected objects have a
// finalizer. Files are never swept (closing one writes it); they stay
// in world.files and are closed at the end of the run as before.
// Mirrors (std.reflection) are collected like any access value: a
// mirror object lives while a value designates it, and keeps the
// values of its node (the snapshot it reflects) alive. The caches that
// hand the same access values out again keep only what is still live:
// the faces whose access cells survive, and the links of the nodes of
// live mirrors (and of the nodes they lead to), so a live mirror still
// gives the same mirrors for the same element, subtype or bound.
//
// The roots are every value a process can still read: the slots of
// every frame of every machine of the world (process, procedure and
// method frames), a method frame's protected object, the places of
// pending out and inout parameters, function results in flight, the
// shared variables and runtime constants. Signals
// cannot hold access values (LRM 6.4.2.3), so drivers and the kernel
// hold none.

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/sim"
)

// DefaultGCMin is the smallest number of allocations between two
// collections of the default policy.
const DefaultGCMin = 4096

// GCOptions configure the garbage collector of the access heap.
type GCOptions struct {
	// Off disables collection: unreachable objects stay until the end
	// of the run, as in VHDL-2008.
	Off bool
	// Every, when positive, collects at the first safe point after
	// every Every allocations (1: after each one), instead of the
	// default period max(DefaultGCMin, live after the last collection).
	Every int64
	// Done, when set, receives the statistics of each world when its
	// run ends (sim.Closer), once per world.
	Done func(GCStats)
}

// GCStats are a world's heap statistics. They are deterministic: the
// same for every run of the same design, seed and options.
type GCStats struct {
	// Live is the number of heap and protected objects now.
	Live int64
	// Peak is the largest Live seen, at allocations and before each
	// collection.
	Peak int64
	// Collections is the number of collections run.
	Collections int64
	// Handles is the number of handles given out (heap and protected
	// objects, files, mirrors).
	Handles int64
}

// gcState is a world's collector state; fork copies it (machines are
// re-registered by the clones).
type gcState struct {
	// since counts the handles given out since the last collection;
	// last is Live after it.
	since, last int64
	peak        int64
	collections int64
	// machines are the world's processes by index, registered by
	// codeIn and machine.clone; nreg of them are set, want is the
	// count the world must reach before it may collect (a fork waits
	// for every clone of its pass).
	machines   []*machine
	nreg, want int
	reported   bool
	// holds memoizes holdsHandles by layout; lookup only.
	holds map[*eval.Type]bool
}

// register adds m to the roots of its world.
func (w *world) register(m *machine) {
	if m.idx < 0 {
		return
	}
	g := &w.gc
	if m.idx >= len(g.machines) {
		g.machines = append(g.machines,
			make([]*machine, m.idx+1-len(g.machines))...)
	}
	if g.machines[m.idx] == nil {
		g.nreg++
	}
	g.machines[m.idx] = m
}

// forkGC is the collector state of c, a fork of w: the same counters,
// no machines until the clones register.
func (w *world) forkGC() gcState {
	g := w.gc
	g.machines = make([]*machine, len(w.gc.machines))
	g.nreg, g.want = 0, w.gc.nreg
	g.reported = false
	g.holds = nil // a cache; each world fills its own
	return g
}

// live is the number of heap and protected objects.
func (w *world) live() int64 { return int64(len(w.heap) + len(w.objs)) }

// notePeak records the current live count in the peak.
func (w *world) notePeak() {
	w.gc.peak = max(w.gc.peak, w.live())
}

// stats returns the world's statistics.
func (w *world) stats() GCStats {
	return GCStats{Live: w.live(), Peak: max(w.gc.peak, w.live()),
		Collections: w.gc.collections, Handles: w.next}
}

// GCStatsOf returns the heap statistics of the world process code c
// runs in; ok is false for code the interpreter did not make.
func GCStatsOf(c sim.Code) (GCStats, bool) {
	x, ok := c.(*code)
	if !ok || x.m == nil || x.m.w == nil {
		return GCStats{}, false
	}
	return x.m.w.stats(), true
}

// due reports whether a collection is due.
func (w *world) due() bool {
	g := &w.gc
	if w.o.GC.Off || g.since == 0 || g.nreg < g.want {
		return false
	}
	period := w.o.GC.Every
	if period <= 0 {
		period = max(DefaultGCMin, g.last)
	}
	return g.since >= period
}

// safePoint collects when a collection is due and m is at a safe
// point (no function running).
func (m *machine) safePoint() {
	if m.sync == 0 && m.w.due() {
		m.w.collect()
	}
}

// marker is one mark phase.
type marker struct {
	w      *world
	marked map[int64]bool // lookup only
	// todo are the reached values not walked yet.
	todo []eval.Value
}

// collect runs one collection.
func (w *world) collect() {
	w.notePeak()
	k := &marker{w: w, marked: map[int64]bool{}}
	k.roots()
	k.drain()
	marked := k.marked
	for h := range w.heap {
		if !marked[h] {
			delete(w.heap, h)
		}
	}
	for h := range w.objs {
		if !marked[h] {
			delete(w.objs, h)
		}
	}
	k.sweepMirrors()
	w.gc.collections++
	w.gc.since, w.gc.last = 0, w.live()
}

// roots marks from every root of the world.
func (k *marker) roots() {
	w := k.w
	for _, m := range w.gc.machines {
		if m == nil {
			continue
		}
		for i := range m.frames {
			f := &m.frames[i]
			k.object(f.obj)
			k.slots(f.vars)
			k.value(f.ret)
			for _, o := range f.outs {
				k.place(o.dst)
			}
		}
		k.value(m.lastRet)
	}
	for _, sk := range slices.SortedFunc(maps.Keys(w.shared),
		compareShared) {
		k.value(w.shared[sk].v)
	}
}

// mirror marks mirror object h (std.reflection) and the values of its
// node, when h is one.
func (k *marker) mirror(h int64) {
	if h == 0 || k.marked[h] {
		return
	}
	f, ok := k.w.mirrors.objs[h]
	if !ok {
		return
	}
	k.marked[h] = true
	k.node(f.n)
}

// sweepMirrors drops the mirror objects and faces the mark did not
// reach, and the links of nodes no live mirror leads to.
func (k *marker) sweepMirrors() {
	ms := &k.w.mirrors
	for h := range ms.objs {
		if !k.marked[h] {
			delete(ms.objs, h)
		}
	}
	for fk, v := range ms.faces {
		if !k.marked[v.I] {
			delete(ms.faces, fk)
		}
	}
	// The nodes of live mirrors, and every node a link of a live node
	// leads to (a breadth-first walk of the links by parent).
	kids := map[*mnode][]*mnode{} // lookup only
	for _, l := range slices.SortedFunc(maps.Keys(ms.links),
		compareLink) {
		kids[l.n] = append(kids[l.n], ms.links[l])
	}
	live := map[*mnode]bool{} // lookup only
	var todo []*mnode
	for _, h := range slices.Sorted(maps.Keys(ms.objs)) {
		if n := ms.objs[h].n; !live[n] {
			live[n] = true
			todo = append(todo, n)
		}
	}
	for len(todo) > 0 {
		n := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, c := range kids[n] {
			if !live[c] {
				live[c] = true
				todo = append(todo, c)
			}
		}
	}
	for l := range ms.links {
		if !live[l.n] {
			delete(ms.links, l)
		}
	}
}

// slots marks from the values of a frame or protected object.
func (k *marker) slots(vars map[*sema.Decl]slot) {
	for _, d := range slices.SortedFunc(maps.Keys(vars), compareDecl) {
		k.value(vars[d].v)
	}
}

// place marks the object a pending out parameter writes into.
func (k *marker) place(p place) {
	switch p.kind {
	case placeHeap:
		k.handle(p.obj)
	case placeObj:
		k.object(p.obj)
	}
}

// node marks from a mirror node's snapshots.
func (k *marker) node(n *mnode) {
	if n == nil {
		return
	}
	k.value(n.v)
	if n.deref != nil {
		k.value(*n.deref)
	}
}

// value marks the objects value v designates and holds.
func (k *marker) value(v eval.Value) {
	if v.T == nil {
		// An untyped value: any handle it may be is kept (keeping an
		// object is always safe).
		k.handle(v.I)
		k.object(v.I)
		k.mirror(v.I)
	} else {
		switch v.T.Kind {
		case eval.KindAccess:
			k.handle(v.I)
			return
		case eval.KindProtected:
			k.object(v.I)
			k.mirror(v.I)
			return
		case eval.KindArray, eval.KindRecord:
			if !k.w.holdsHandles(v.T) {
				return
			}
		default:
			return
		}
	}
	for _, e := range v.E {
		k.value(e)
	}
}

// handle marks heap object h; its value is walked by drain.
func (k *marker) handle(h int64) {
	if h == 0 || k.marked[h] {
		return
	}
	v, ok := k.w.heap[h]
	if !ok {
		return
	}
	k.marked[h] = true
	k.todo = append(k.todo, v)
}

// object marks protected object h and its variables.
func (k *marker) object(h int64) {
	if h == 0 || k.marked[h] {
		return
	}
	o, ok := k.w.objs[h]
	if !ok {
		return
	}
	k.marked[h] = true
	k.slots(o.vars)
}

// drain walks the reached heap values until none is left (a worklist,
// so a long linked list does not recurse deeply).
func (k *marker) drain() {
	for len(k.todo) > 0 {
		v := k.todo[len(k.todo)-1]
		k.todo = k.todo[:len(k.todo)-1]
		k.value(v)
	}
}

// holdsHandles reports whether a value of layout t can hold an access
// or protected value: a composite whose elements or fields can.
func (w *world) holdsHandles(t *eval.Type) bool {
	if t == nil {
		return true
	}
	if v, ok := w.gc.holds[t]; ok {
		return v
	}
	var v bool
	switch t.Kind {
	case eval.KindAccess, eval.KindProtected:
		v = true
	case eval.KindArray:
		v = t.Elem == nil || w.holdsHandles(t.Elem)
	case eval.KindRecord:
		for _, f := range t.Fields {
			if w.holdsHandles(f.Type) {
				v = true
				break
			}
		}
	}
	if w.gc.holds == nil {
		w.gc.holds = map[*eval.Type]bool{}
	}
	w.gc.holds[t] = v
	return v
}

// compareDecl orders declarations by file, position and name. The
// order only makes the visit fixed: marking gives the same set in any
// order.
func compareDecl(a, b *sema.Decl) int {
	return cmp.Or(strings.Compare(declPath(a), declPath(b)),
		cmp.Compare(a.Span.Start, b.Span.Start),
		strings.Compare(string(a.Name), string(b.Name)))
}

func declPath(d *sema.Decl) string {
	if d == nil || d.File == nil {
		return ""
	}
	return d.File.Path
}

func compareShared(a, b sharedKey) int {
	pa, pb := "", ""
	if a.in != nil {
		pa = a.in.Path
	}
	if b.in != nil {
		pb = b.in.Path
	}
	return cmp.Or(compareDecl(a.d, b.d), strings.Compare(pa, pb))
}

func compareLink(a, b mlink) int {
	return cmp.Or(cmp.Compare(a.what, b.what), cmp.Compare(a.i, b.i))
}
