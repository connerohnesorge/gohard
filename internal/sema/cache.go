package sema

// cache.go is the sema side of the on-disk analysis cache (#37): the
// syntax entries (one parse result per file text) and the semantic
// entries (one analyzed design unit each), their keys, and the codec
// mode that stores a unit's objects with references into the objects
// of other entries. The store itself (file format, versioning,
// recovery) is internal/cache; Analyze sees it as the Cache interface.
//
// A semantic entry is what one unit job produced (analyzeUnit): the
// unit's declarations, regions, references, typed expressions,
// statements and diagnostics. Everything the session computes
// (canonical indexes, levels, libraries, the checks across units) is
// recomputed on every run, so a restored unit goes through exactly the
// phases a freshly analyzed one does. The entry key is
//
//	K = H(node ID, conditional-analysis values, step limit, unit graph,
//	      the keys of the unit's dependencies)
//
// and the entry records the keys of the other units the job read (its
// readable() lookups and the owners of every object it points at): a
// hit needs each of them to be unchanged. Keys are hashes of hashes, so
// a unit's key covers everything below it. The unit's node ID is
// computed from its tokens (kind, flags, text), the trivia lengths and
// the line breaks, without the text of comments (#10): a comment edit
// that keeps every position keeps the key. An entry whose stored
// strings contain comment text (the text of a node with a comment
// inside) also records the text of every file it depends on, the text
// key, and is used only while those texts are unchanged. The unit
// graph is every file, unit and library of the session (names,
// levels, dependencies), so adding or removing a unit, which can make
// a name resolve differently anywhere, invalidates every entry.
//
// Soundness rests on the node ID and the keys of the unit's Deps
// closure (Deps is recomputed every run, so a unit that starts or stops
// being visible changes the Deps of the units that see it): the tests
// prove those parts (TestCacheSameLengthCode, TestCacheEdits). The unit
// graph and the recorded reads (extra) are defence in depth for a read
// outside the Deps closure; no known input needs them and no test
// proves them necessary, so do not rely on them alone.
//
// External references: an object of a cache entry that belongs to
// someone else is stored as (owner, number): the bundle's objects by
// their number in the prebuilt bundle, the session's files, units and
// libraries by their canonical index, and another unit's objects by
// that unit's index and the object's number in its entry. Red nodes
// and tokens are stored as paths from their file's root, so a restored
// unit points into the current tree, whose comment text may differ.

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/parser"
)

// Cache is the on-disk analysis cache as Analyze uses it: entries of a
// kind (CacheSyntax, CacheSema) by key. It must be safe for concurrent
// use; it only ever changes how fast a result comes, never the result.
type Cache interface {
	// Get returns the entry stored under key, if any.
	Get(kind string, key [32]byte) ([]byte, bool)
	// Put stores data under key. slot names what the entry is for (a
	// file path, a unit), so the store may drop the entry it last
	// wrote for that slot.
	Put(kind, slot string, key [32]byte, data []byte)
	// Discard throws the whole cache away: an entry passed the store's
	// checks but did not decode.
	Discard()
}

// The entry kinds.
const (
	CacheSyntax = "syntax"
	CacheSema   = "sema"
)

// cacheFormat is the version of the entry payloads written here. Bump
// it when the analysis rules change what an entry holds for the same
// source (2: the v1.1 Stage 1 rules, e.g. VHDL-2019 parameter classes
// and implicit operations of protected composites; 3: the v1.1 Stage 3
// rules: name-attribute prefixes, protected-type generics and the
// retired v1.1 unsupported/construct detectors; 4: PSL built-in calls
// keep their actuals, isunknown is one and a PSL operand is a PSL
// Boolean, #111; 5: a verification unit keeps its statements and
// `use vunit` binds, #66).
const cacheFormat = 5

var schemaOnce = sync.OnceValue(func() string {
	return codecSchema + "/" + sourcesHash() + "/" +
		string(rune('0'+cacheFormat))
})

// CacheSchema identifies the stored form of cache entries: the codec
// schema, the bundled sources and the entry format. It is part of the
// whole-cache key, so entries of another build are never read.
func CacheSchema() string { return schemaOnce() }

// syntaxKey is the key of a syntax entry: the file content and the
// parse options (standard, psl settings, conditional-analysis
// defines and tool version) under the kinds.yaml hash.
func syntaxKey(text []byte, o parser.Options) [32]byte {
	w := newKey()
	w.str("syntax")
	w.str(cst.KindsHash)
	w.int(int64(o.Standard))
	w.bool(o.PSLComments)
	w.bool(o.PSLStrictKeywords)
	w.str(o.ToolVersion)
	w.int(int64(len(o.Defines)))
	for _, d := range o.Defines {
		w.str(d.Name)
		w.str(d.Value)
	}
	w.int(int64(o.Budget))
	w.int(int64(o.MaxBytes))
	w.int(int64(o.MaxNodes))
	w.bytes(text)
	return w.sum()
}

// keyWriter writes length-prefixed parts to a hash.
type keyWriter struct {
	h   hash.Hash
	buf [binary.MaxVarintLen64]byte
}

func newKey() *keyWriter { return &keyWriter{h: sha256.New()} }

func (w *keyWriter) sum() (out [32]byte) {
	copy(out[:], w.h.Sum(nil))
	return out
}

func (w *keyWriter) int(v int64) {
	n := binary.PutVarint(w.buf[:], v)
	w.h.Write(w.buf[:n])
}

func (w *keyWriter) bool(b bool) {
	if b {
		w.int(1)
	} else {
		w.int(0)
	}
}

func (w *keyWriter) bytes(b []byte) {
	w.int(int64(len(b)))
	w.h.Write(b)
}

func (w *keyWriter) str(s string) { w.bytes([]byte(s)) }

func (w *keyWriter) key(k [32]byte) { w.h.Write(k[:]) }

// ParseCached returns parser.Parse(text, o), restored from the syntax
// entry of c when it has one and stored in c when not: the editor
// parses its documents itself and passes the trees to Analyze. path
// names the file the entry is for.
func ParseCached(c Cache, path string, text []byte, o parser.Options,
) *parser.Result {
	return parseCached(c, path, text, o)
}

// parseCached parses text or restores its syntax entry, storing a new
// one when the cache has none.
func parseCached(c Cache, path string, text []byte, o parser.Options,
) *parser.Result {
	k := syntaxKey(text, o)
	if b, ok := c.Get(CacheSyntax, k); ok {
		if p, ok := decodeSyntax(b, text); ok {
			return p
		}
		c.Discard()
	}
	p := parser.Parse(text, o)
	c.Put(CacheSyntax, path, k, encodeSyntax(p))
	return p
}

func encodeSyntax(p *parser.Result) []byte {
	e := newEncoder()
	e.s_parser_Result(p)
	drain(&e.queue, &e.later)
	return e.buf
}

// decodeSyntax restores syntax entry b of text. An entry that does not
// check out is rejected, never trusted: it must decode exactly, its
// tree must be consistent (cst.Green.Consistent) and spell text byte
// for byte, and every offset it holds must lie in text. A decoder
// panic on a damaged entry is a rejection too (#37: a corrupted cache
// is thrown away and rebuilt, silently).
func decodeSyntax(b, text []byte) (r *parser.Result, ok bool) {
	defer func() {
		if recover() != nil {
			r, ok = nil, false
		}
	}()
	d := &decoder{buf: b}
	var v parser.Result
	d.s_parser_Result(&v)
	drain(&d.queue, &d.later)
	if d.bad || d.off != len(d.buf) || v.Root == nil ||
		v.Root.Width() != len(text) || v.Root.IsToken() ||
		v.Root.Kind() != cst.KindDesignFile || !v.Root.Consistent() ||
		!bytes.Equal(v.Root.AppendText(make([]byte, 0, len(text))),
			text) {
		return nil, false
	}
	in := func(start, end int) bool {
		return 0 <= start && start <= end && end <= len(text)
	}
	for _, p := range v.Problems {
		if !in(p.Start, p.End) {
			return nil, false
		}
	}
	for _, x := range v.Inactive {
		if !in(x.Start, x.End) {
			return nil, false
		}
	}
	for _, x := range v.Directives {
		if !in(x.Start, x.End) {
			return nil, false
		}
	}
	return parser.Restore(&v, text), true
}

// bundleObjs are the bundle's objects in codec id order, set when the
// bundle is decoded from prebuilt.bin; bundleObjects computes them for
// an analyzed bundle.
var bundleObjs []any

var (
	bundleObjsOnce sync.Once
	bundleRegOnce  sync.Once
	bundleReg      map[any]uint64
)

func bundleObjects() []any {
	bundleObjsOnce.Do(func() {
		if b := Bundle(); bundleObjs == nil {
			_, bundleObjs = encodeBundleObjs(b)
		}
	})
	return bundleObjs
}

// bundleRegistry numbers the bundle's objects for encoding.
func bundleRegistry() map[any]uint64 {
	bundleRegOnce.Do(func() {
		objs := bundleObjects()
		bundleReg = make(map[any]uint64, len(objs))
		for i, o := range objs {
			bundleReg[o] = uint64(i)
		}
	})
	return bundleReg
}

// External reference owners.
const (
	ownerBundle = iota
	anchorFile
	anchorUnit
	anchorLib
	anchorLibDecl
	ownerUnit0 // + project unit index
)

// regRef is an object of a project unit's entry.
type regRef struct {
	owner int32
	seq   uint32
}

// unitCache is the semantic cache state of one session. Slot i of each
// slice belongs to project unit i (s.units[i]): it is written only by
// that unit's job or between levels, and read only by units of higher
// levels, so the jobs share nothing mutable.
type unitCache struct {
	c     Cache
	s     *session
	off   int      // the bundle's unit count (Unit.Index offset)
	base  [32]byte // graph, step limit and conditional values
	k1    [][32]byte
	keys  [][32]byte // final keys
	objs  [][]any    // entry objects; nil: not available
	reads [][]*Unit  // readable() lookups of a unit being analyzed
	rec   []bool     // reads[i] is being recorded
	extra [][]int    // the other units an entry depends on
	hit   []bool
	psl   [][]pslEntry
	enc   []*encoder // a miss's entry being written
	xs    []*xenc
	level []int

	unitIdx map[*Unit]int
	fileIdx map[*File]int
	libIdx  map[*Library]int
	libDecl map[*Decl]int
	roots   map[*cst.Green]uint64

	reg       map[any]regRef
	regd      []bool
	failed    bool // a unit could not be stored: no entries above it
	failLevel int

	hashOnce []sync.Once
	hashes   [][32]byte

	// noStore is set when the session reuses an earlier result
	// (incr.go): its misses are analyzed but not stored, and their
	// extra units are their readable() reads.
	noStore bool
}

type pslEntry struct {
	at cst.Range
	e  *Expr
}

// newUnitCache prepares the semantic cache of s once its units are
// linked and levelized.
func newUnitCache(c Cache, s *session, popts parser.Options) *unitCache {
	n := len(s.units)
	uc := &unitCache{c: c, s: s, off: len(s.base.allUnits()),
		k1: make([][32]byte, n), keys: make([][32]byte, n),
		objs: make([][]any, n), reads: make([][]*Unit, n),
		rec: make([]bool, n), extra: make([][]int, n),
		hit: make([]bool, n), psl: make([][]pslEntry, n),
		enc: make([]*encoder, n), xs: make([]*xenc, n),
		level: make([]int, n), regd: make([]bool, n),
		unitIdx: make(map[*Unit]int, n), fileIdx: map[*File]int{},
		libIdx: map[*Library]int{}, libDecl: map[*Decl]int{},
		roots:    map[*cst.Green]uint64{},
		hashOnce: make([]sync.Once, len(s.files)),
		hashes:   make([][32]byte, len(s.files)),
	}
	for i, u := range s.units {
		uc.unitIdx[u] = i
		uc.level[i] = u.Level
	}
	for i, f := range s.files {
		uc.fileIdx[f] = i
		if f.Parse != nil && f.Parse.Root != nil {
			uc.roots[f.Parse.Root] = uint64(2 * i)
		}
	}
	for i, f := range s.base.Files {
		if f.Parse != nil && f.Parse.Root != nil {
			if _, ok := uc.roots[f.Parse.Root]; !ok {
				uc.roots[f.Parse.Root] = uint64(2*i + 1)
			}
		}
	}
	for i, l := range s.libOrder {
		uc.libIdx[l] = i
		if l.Decl != nil {
			if _, ok := uc.libDecl[l.Decl]; !ok {
				uc.libDecl[l.Decl] = i
			}
		}
	}
	w := newKey()
	w.str("sema-session")
	w.int(s.steps)
	w.int(int64(popts.Standard))
	w.str(popts.ToolVersion)
	w.int(int64(len(popts.Defines)))
	for _, d := range popts.Defines {
		w.str(d.Name)
		w.str(d.Value)
	}
	uc.graph(w)
	uc.base = w.sum()
	return uc
}

// unitID names a unit in the graph: its project index, or its bundle
// label.
func (uc *unitCache) unitID(w *keyWriter, u *Unit) {
	if u == nil {
		w.int(-2)
		return
	}
	if i, ok := uc.unitIdx[u]; ok {
		w.int(int64(i))
		return
	}
	w.int(-1)
	w.str(u.Label())
	if u.File != nil {
		w.str(u.File.Path)
	}
}

// graph writes the unit graph: everything the session knows before
// the unit jobs run that a job can observe.
func (uc *unitCache) graph(w *keyWriter) {
	s := uc.s
	w.int(int64(uc.off))
	w.int(int64(len(s.files)))
	for _, f := range s.files {
		w.str(f.Path)
		w.str(string(f.Library))
		w.bool(f.Bundled)
		w.int(int64(len(f.Units)))
	}
	w.int(int64(len(s.units)))
	for _, u := range s.units {
		w.int(int64(uc.fileIdx[u.File]))
		w.int(int64(u.Kind))
		w.str(string(u.Library))
		w.str(string(u.Name))
		w.str(string(u.Of))
		w.int(int64(u.Index))
		w.int(int64(u.Level))
		w.bool(u.Duplicate)
		w.bool(u.Opaque)
		w.bool(u.Unchecked)
		w.bool(u.Modeled)
		w.int(int64(len(u.Deps)))
		for _, d := range u.Deps {
			uc.unitID(w, d)
		}
	}
	w.int(int64(len(s.libOrder)))
	for _, l := range s.libOrder {
		w.str(string(l.Name))
		w.bool(l.Bundled)
		w.int(int64(len(l.Folders)))
		for _, f := range l.Folders {
			w.str(f)
		}
		_, own := uc.libDecl[l.Decl]
		w.bool(own)
		for _, k := range sortedKeys(l.units) {
			w.str(string(k))
			uc.unitID(w, l.units[k])
		}
		w.int(-3)
		for _, k := range sortedKeys(l.secondary) {
			w.str(string(k))
			for _, u := range l.secondary[k] {
				uc.unitID(w, u)
			}
		}
		w.int(-3)
		for _, u := range l.order {
			uc.unitID(w, u)
		}
		w.int(-3)
		for _, u := range l.order {
			if old, ok := l.replaced[u]; ok {
				uc.unitID(w, u)
				uc.unitID(w, old)
			}
		}
		w.int(-3)
	}
}

// nodeID writes the unit's semantic node ID: its tree (node kinds,
// token kinds, flags and text), the length and kind of every trivia
// piece but not its text, and the line breaks, so every position is
// covered. It is memoized on the file's parse result, which the editor
// reuses for an unchanged file on every keystroke (#53).
func nodeID(w *keyWriter, u *Unit) {
	if u.File.Parse == nil {
		w.key(nodeIDSum(u))
		return
	}
	_, _, off, _ := u.Node.Raw()
	k := nodeIDKey(off)
	if d, ok := u.File.Parse.Memo().Load(k); ok {
		w.key(d.([32]byte))
		return
	}
	d := nodeIDSum(u)
	u.File.Parse.Memo().Store(k, d)
	w.key(d)
}

// nodeIDKey keys a unit's node ID in its parse result's memo by the
// unit's offset.
type nodeIDKey int

func nodeIDSum(u *Unit) [32]byte {
	w := newKey()
	g, _, off, _ := u.Node.Raw()
	fs := u.Node.FullSpan()
	w.int(int64(off))
	if lines := u.File.Lines; lines != nil {
		l, c := lines.LineCol(fs.Start)
		w.int(int64(l))
		w.int(int64(c))
		el, ec := lines.LineCol(fs.End)
		w.int(int64(el))
		w.int(int64(ec))
		text := u.File.Text
		for i := fs.Start; i < fs.End && i < len(text); i++ {
			if text[i] == '\n' || text[i] == '\r' {
				w.int(int64(i))
				w.int(int64(text[i]))
			}
		}
	}
	var buf []byte
	buf = greenID(buf, g)
	w.bytes(buf)
	return w.sum()
}

func greenID(buf []byte, g *cst.Green) []byte {
	if g == nil {
		return append(buf, 0)
	}
	r := g.Raw()
	buf = binary.AppendUvarint(buf, uint64(r.Kind))
	buf = binary.AppendUvarint(buf, uint64(r.Flags))
	if r.Tok {
		buf = append(buf, 1)
		buf = binary.AppendUvarint(buf, uint64(len(r.Text)))
		buf = append(buf, r.Text...)
		buf = binary.AppendUvarint(buf, uint64(r.NLead))
		buf = binary.AppendUvarint(buf, uint64(len(r.Triv)))
		for _, t := range r.Triv {
			buf = binary.AppendUvarint(buf, uint64(t.Kind))
			buf = binary.AppendUvarint(buf, uint64(len(t.Text)))
		}
		return buf
	}
	buf = append(buf, 2)
	buf = binary.AppendUvarint(buf, uint64(len(r.Kids)))
	for _, k := range r.Kids {
		buf = greenID(buf, k)
	}
	return buf
}

// fileHash is the text key of project file i.
func (uc *unitCache) fileHash(i int) [32]byte {
	uc.hashOnce[i].Do(func() {
		uc.hashes[i] = sha256.Sum256(uc.s.files[i].Text)
	})
	return uc.hashes[i]
}

// record notes that the unit job of from read unit to (readable).
func (uc *unitCache) record(from, to *Unit) {
	if from == nil || to == nil || to.File == nil || to.File.Bundled {
		return
	}
	i, ok := uc.unitIdx[from]
	if !ok || !uc.rec[i] {
		return
	}
	uc.reads[i] = append(uc.reads[i], to)
}

// firstKey computes the entry key of unit i from its node ID and the
// keys of its dependencies.
func (uc *unitCache) firstKey(i int) [32]byte {
	u := uc.s.units[i]
	w := newKey()
	w.str("sema-unit")
	w.key(uc.base)
	w.int(int64(i))
	nodeID(w, u)
	for _, d := range u.Deps {
		j, ok := uc.unitIdx[d]
		if !ok {
			continue // bundled: covered by the whole-cache key
		}
		w.int(int64(j))
		if uc.level[j] < u.Level {
			w.key(uc.keys[j])
		} else {
			w.int(-1) // a cycle: never read
		}
	}
	return w.sum()
}

// finalKey is unit i's key: its entry key and the keys of the other
// units its entry depends on.
func (uc *unitCache) finalKey(i int) [32]byte {
	w := newKey()
	w.key(uc.k1[i])
	for _, j := range uc.extra[i] {
		w.int(int64(j))
		w.key(uc.keys[j])
	}
	return w.sum()
}

// lookup computes unit i's entry key and restores the unit from its
// entry, if the cache has a usable one.
func (uc *unitCache) lookup(i int) {
	uc.k1[i] = uc.firstKey(i)
	b, ok := uc.c.Get(CacheSema, uc.k1[i])
	if !ok {
		return
	}
	switch uc.safeRestore(i, b) {
	case restoreOK:
		uc.hit[i] = true
	case restoreBad:
		uc.c.Discard()
	}
}

type restoreResult int

const (
	restoreOK restoreResult = iota
	restoreStale
	restoreBad
)

// safeRestore is restore with a decoder panic on a damaged entry
// reported as restoreBad: the entry is thrown away, never trusted.
// restore changes the unit only after the entry decoded completely.
func (uc *unitCache) safeRestore(i int, b []byte) (r restoreResult) {
	defer func() {
		if recover() != nil {
			r = restoreBad
		}
	}()
	return uc.restore(i, b)
}

// restore decodes entry b of unit i into the unit.
func (uc *unitCache) restore(i int, b []byte) restoreResult {
	u := uc.s.units[i]
	d := &decoder{buf: b}
	if d.uint() != cacheFormat {
		return restoreBad
	}
	n := d.length()
	extra := make([]int, 0, n)
	stale := false
	for range n {
		j := int(d.uint())
		k := d.raw(32)
		if d.bad || j < 0 || j >= len(uc.s.units) || j == i {
			return restoreBad
		}
		if uc.level[j] >= u.Level || !bytes.Equal(k, uc.keys[j][:]) {
			stale = true
		}
		extra = append(extra, j)
	}
	if d.bool() {
		n := d.length()
		for range n {
			j := int(d.uint())
			k := d.raw(32)
			if d.bad || j < 0 || j >= len(uc.s.files) {
				return restoreBad
			}
			if h := uc.fileHash(j); !bytes.Equal(k, h[:]) {
				stale = true
			}
		}
	}
	if d.bad {
		return restoreBad
	}
	if stale {
		return restoreStale
	}
	x := &xdec{uc: uc, self: i}
	d.x = x
	seedTables(func(ts []*Type, es []*eval.Type) {
		d.t.p_Type.seed(ts...)
		d.t.p_eval_Type.seed(es...)
	})
	var tmp Unit
	d.s_Unit(&tmp)
	drain(&d.queue, &d.later)
	var psl []pslEntry
	if np, ok := d.count(); ok {
		psl = make([]pslEntry, np)
		for k := range psl {
			d.s_cst_Range(&psl[k].at)
			psl[k].e = d.p_Expr()
		}
	}
	drain(&d.queue, &d.later)
	if x.missing {
		return restoreStale
	}
	if d.bad || d.off != len(d.buf) || !sameUnit(&tmp, u) {
		return restoreBad
	}
	u.Decl, u.Context, u.Region = tmp.Decl, tmp.Context, tmp.Region
	u.Decls, u.Refs, u.Exprs, u.Body = tmp.Decls, tmp.Refs, tmp.Exprs,
		tmp.Body
	u.Opaque, u.Unchecked, u.Modeled = tmp.Opaque, tmp.Unchecked,
		tmp.Modeled
	u.diags, u.missing, u.late = tmp.diags, tmp.missing, tmp.late
	u.unitAttrs, u.othersAttrs = tmp.unitAttrs, tmp.othersAttrs
	uc.objs[i] = d.objs
	uc.extra[i] = extra
	uc.psl[i] = psl
	return restoreOK
}

// sameUnit reports whether a decoded unit is the session's unit u (the
// fields the session sets before the unit job).
func sameUnit(t, u *Unit) bool {
	return t.Kind == u.Kind && t.Library == u.Library &&
		t.Name == u.Name && t.Of == u.Of && t.File == u.File &&
		t.Index == u.Index && t.Level == u.Level &&
		t.Duplicate == u.Duplicate && slices.Equal(t.Deps, u.Deps) &&
		t.Node.Equal(u.Node) && t.Lib.Equal(u.Lib)
}

// encode writes the entry body of freshly analyzed unit i (in its
// job); finish completes it between levels.
func (uc *unitCache) encode(i int) {
	if uc.failed {
		return
	}
	u := uc.s.units[i]
	e := newEncoder()
	x := &xenc{uc: uc, self: i, owners: map[int]bool{}}
	e.x = x
	seedTables(func(ts []*Type, es []*eval.Type) {
		e.t.p_Type.seed(ts...)
		e.t.p_eval_Type.seed(es...)
	})
	if !catchCodec(func() {
		e.s_Unit(u)
		drain(&e.queue, &e.later)
	}) || x.bad {
		return
	}
	uc.enc[i], uc.xs[i] = e, x
}

// catchCodec runs f, reporting false if the codec found a graph it
// cannot store (a pointer-keyed map whose key is unreachable).
func catchCodec(f func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			s, isStr := r.(string)
			if !isStr || !strings.HasPrefix(s, "sema: codec:") {
				panic(r)
			}
			ok = false
		}
	}()
	f()
	return true
}

// finish completes the entry of unit i (sequentially, after its
// level): the PSL expressions of its file, the dependency header, and
// the store. It returns false when the unit could not be stored.
func (uc *unitCache) finish(i int) bool {
	u := uc.s.units[i]
	e, x := uc.enc[i], uc.xs[i]
	uc.enc[i], uc.xs[i] = nil, nil
	reads := uc.reads[i]
	uc.reads[i] = nil
	extra := map[int]bool{}
	for _, d := range reads {
		if j, ok := uc.unitIdx[d]; ok && j != i {
			extra[j] = true
		}
	}
	if x != nil {
		for j := range x.owners {
			extra[j] = true
		}
	}
	for _, d := range u.Deps {
		if j, ok := uc.unitIdx[d]; ok {
			delete(extra, j)
		}
	}
	for j := range extra {
		if uc.level[j] >= u.Level {
			e = nil // an object of a unit not finished before it
		}
	}
	uc.extra[i] = slices.Sorted(maps.Keys(extra))
	if e == nil {
		return false
	}
	var psl []pslEntry
	if f := u.File; f.PSL != nil {
		fs := u.Node.FullSpan()
		var ats []cst.Range
		for at := range f.PSL {
			if at.Start >= fs.Start && at.End <= fs.End {
				ats = append(ats, at)
			}
		}
		slices.SortFunc(ats, func(a, b cst.Range) int {
			return cmp.Or(cmp.Compare(a.Start, b.Start),
				cmp.Compare(a.End, b.End))
		})
		for _, at := range ats {
			psl = append(psl, pslEntry{at, f.PSL[at]})
		}
	}
	ok := catchCodec(func() {
		if psl == nil {
			e.uint(0)
		} else {
			e.uint(uint64(len(psl)) + 1)
			for k := range psl {
				e.s_cst_Range(&psl[k].at)
				e.p_Expr(psl[k].e)
			}
		}
		drain(&e.queue, &e.later)
	})
	if !ok || x.bad {
		return false
	}
	for j := range x.owners {
		if uc.level[j] >= u.Level || uc.objs[j] == nil {
			return false
		}
	}
	h := &encoder{}
	h.uint(cacheFormat)
	h.uint(uint64(len(uc.extra[i])))
	for _, j := range uc.extra[i] {
		h.uint(uint64(j))
		h.buf = append(h.buf, uc.keys[j][:]...)
	}
	h.bool(x.textSens)
	if x.textSens {
		files := uc.closureFiles(i)
		h.uint(uint64(len(files)))
		for _, j := range files {
			k := uc.fileHash(j)
			h.uint(uint64(j))
			h.buf = append(h.buf, k[:]...)
		}
	}
	uc.objs[i] = e.objs
	uc.c.Put(CacheSema, u.File.Path+"#"+u.Label(), uc.k1[i],
		append(h.buf, e.buf...))
	return true
}

// closureFiles returns the project files of unit i and of every unit
// its key covers, sorted.
func (uc *unitCache) closureFiles(i int) []int {
	seen := map[int]bool{}
	files := map[int]bool{}
	var visit func(j int)
	visit = func(j int) {
		if seen[j] {
			return
		}
		seen[j] = true
		u := uc.s.units[j]
		files[uc.fileIdx[u.File]] = true
		for _, d := range u.Deps {
			if k, ok := uc.unitIdx[d]; ok && uc.level[k] < u.Level {
				visit(k)
			}
		}
		for _, k := range uc.extra[j] {
			visit(k)
		}
	}
	visit(i)
	return slices.Sorted(maps.Keys(files))
}

// register adds the objects of the given units to the registry of
// objects other entries may point at.
func (uc *unitCache) register(us []*Unit) {
	if uc.reg == nil {
		uc.reg = map[any]regRef{}
	}
	for _, u := range us {
		i := uc.unitIdx[u]
		if uc.regd[i] {
			continue
		}
		uc.regd[i] = true
		for k, o := range uc.objs[i] {
			uc.reg[o] = regRef{owner: int32(i), seq: uint32(k)}
		}
	}
}

// xenc is the cache-entry state of an encoder.
type xenc struct {
	uc       *unitCache
	self     int
	owners   map[int]bool
	textSens bool
	bad      bool
	// paths writes every red node as its full path, so the encoding
	// does not depend on which red nodes are the same object (no code
	// compares red nodes but with Equal; the tests compare graphs).
	paths bool
}

// find returns the owner and number of an object some other entry or
// the session holds.
func (x *xenc) find(p any) (owner, seq uint64, ok bool) {
	uc := x.uc
	switch v := p.(type) {
	case *Unit:
		if i, ok := uc.unitIdx[v]; ok {
			return anchorUnit, uint64(i), true
		}
	case *File:
		if i, ok := uc.fileIdx[v]; ok {
			return anchorFile, uint64(i), true
		}
	case *Library:
		if i, ok := uc.libIdx[v]; ok {
			return anchorLib, uint64(i), true
		}
	case *Decl:
		if i, ok := uc.libDecl[v]; ok {
			return anchorLibDecl, uint64(i), true
		}
	case *Result:
		return ownerBundle, 0, true // use marks the entry bad
	}
	if n, ok := bundleRegistry()[p]; ok {
		return ownerBundle, n, true
	}
	if r, ok := uc.reg[p]; ok {
		return uint64(ownerUnit0 + int(r.owner)), uint64(r.seq), true
	}
	return 0, 0, false
}

// use records that the entry refers to p of owner.
func (x *xenc) use(p any, owner uint64) {
	if _, ok := p.(*Result); ok {
		x.bad = true // a whole result is never part of an entry
	}
	if owner >= ownerUnit0 {
		x.owners[int(owner-ownerUnit0)] = true
	}
}

// scan calls f with the objects of other entries, in a fixed order:
// those of the units the entry refers to so far (by index), the
// bundle's, then those of every other unit; it stops when f returns
// false.
func (x *xenc) scan(f func(o any) bool) {
	uc := x.uc
	visit := func(objs []any) bool {
		for _, o := range objs {
			if !f(o) {
				return false
			}
		}
		return true
	}
	owners := slices.Sorted(maps.Keys(x.owners))
	for _, k := range owners {
		if !visit(uc.objs[k]) {
			return
		}
	}
	if !visit(bundleObjects()) {
		return
	}
	for k := range uc.objs {
		if !x.owners[k] && uc.regd[k] && !visit(uc.objs[k]) {
			return
		}
	}
}

// text notes a stored string: one with comment text in it makes the
// entry depend on the text of its files.
func (x *xenc) text(s string) {
	if !x.textSens && (strings.Contains(s, "--") ||
		strings.Contains(s, "/*")) {
		x.textSens = true
	}
}

// xdec is the cache-entry state of a decoder.
type xdec struct {
	uc      *unitCache
	self    int
	missing bool // an owner's objects are not available
}

func (x *xdec) resolve(owner, seq uint64) any {
	uc := x.uc
	s := uc.s
	switch owner {
	case ownerBundle:
		if objs := bundleObjects(); seq < uint64(len(objs)) {
			return objs[seq]
		}
	case anchorFile:
		if seq < uint64(len(s.files)) {
			return s.files[seq]
		}
	case anchorUnit:
		if seq < uint64(len(s.units)) {
			return s.units[seq]
		}
	case anchorLib:
		if seq < uint64(len(s.libOrder)) {
			return s.libOrder[seq]
		}
	case anchorLibDecl:
		if seq < uint64(len(s.libOrder)) {
			return s.libOrder[seq].Decl
		}
	default:
		k := owner - ownerUnit0
		if k >= uint64(len(s.units)) ||
			uc.level[k] >= uc.level[x.self] {
			return nil
		}
		objs := uc.objs[k]
		if objs == nil {
			x.missing = true
			return nil
		}
		if seq < uint64(len(objs)) {
			return objs[seq]
		}
	}
	return nil
}

// xnode writes a red node as a path from its file's root.
func (e *encoder) xnode(n cst.Node) {
	if n.IsZero() {
		e.uint(0)
		return
	}
	if id, ok := e.nodes[n]; ok && !e.x.paths {
		e.uint(id + 2)
		return
	}
	e.uint(1)
	g, parent, off, idx := n.Raw()
	if parent.IsZero() {
		code, ok := e.x.uc.roots[g]
		if !ok {
			e.x.bad = true
		}
		e.uint(0)
		e.uint(code)
	} else {
		e.uint(1)
		e.node(parent)
		e.uint(uint64(idx))
		e.int(int64(off))
	}
	e.nodes[n] = uint64(len(e.nodes))
}

func (d *decoder) xnode() cst.Node {
	switch id := d.uint(); {
	case id == 0:
		return cst.Node{}
	case id >= 2:
		if id-2 < uint64(len(d.nodes)) {
			return d.nodes[id-2]
		}
		d.fail()
		return cst.Node{}
	}
	var n cst.Node
	if d.uint() == 0 {
		g := d.x.root(d.uint())
		if g == nil {
			d.fail()
			return cst.Node{}
		}
		n = cst.NewRoot(g)
	} else {
		parent := d.node()
		idx, off := d.uint(), int(d.int())
		g := d.x.child(parent, idx, false)
		if g == nil {
			d.fail()
			return cst.Node{}
		}
		n = cst.NodeFromRaw(g, parent, off, int(idx))
	}
	if d.bad {
		return cst.Node{}
	}
	d.nodes = append(d.nodes, n)
	return n
}

func (e *encoder) xtoken(t cst.Token) {
	g, parent, off, idx := t.Raw()
	if g == nil {
		e.uint(0)
		return
	}
	e.uint(1)
	e.node(parent)
	e.uint(uint64(idx))
	e.int(int64(off))
}

func (d *decoder) xtoken() cst.Token {
	if d.uint() == 0 {
		return cst.Token{}
	}
	parent := d.node()
	idx, off := d.uint(), int(d.int())
	g := d.x.child(parent, idx, true)
	if g == nil {
		d.fail()
		return cst.Token{}
	}
	return cst.TokenFromRaw(g, parent, off, int(idx))
}

// root returns the green root of the file with code c.
func (x *xdec) root(c uint64) *cst.Green {
	var fs []*File
	if c%2 == 0 {
		fs = x.uc.s.files
	} else {
		fs = x.uc.s.base.Files
	}
	if i := c / 2; i < uint64(len(fs)) && fs[i].Parse != nil {
		return fs[i].Parse.Root
	}
	return nil
}

// child returns child idx of parent's green node when it is a token
// (tok) or a node.
func (x *xdec) child(parent cst.Node, idx uint64, tok bool) *cst.Green {
	pg := parent.Green()
	if pg == nil || idx >= uint64(pg.NumChildren()) {
		return nil
	}
	g := pg.Child(int(idx))
	if g.IsToken() != tok {
		return nil
	}
	return g
}

// analyzeCached runs the unit jobs of one level with the cache: units
// with a usable entry are restored, the others analyzed and stored.
func (s *session) analyzeCached(us []*Unit) {
	uc := s.uc
	idx := make([]int, len(us))
	for k, u := range us {
		idx[k] = uc.unitIdx[u]
	}
	s.jobs.Run(s.limit, len(us), func(k int) { uc.lookup(idx[k]) })
	var miss []int
	for _, i := range idx {
		if !uc.hit[i] {
			miss = append(miss, i)
		}
	}
	level := us[0].Level
	if uc.noStore {
		s.jobs.Run(s.limit, len(miss), func(k int) {
			s.analyzeUnit(s.units[miss[k]])
		})
		for _, i := range idx {
			if uc.hit[i] {
				uc.restorePSL(i)
			} else {
				uc.extra[i] = s.readsOutsideDeps(i)
			}
			uc.keys[i] = uc.finalKey(i)
		}
		return
	}
	if uc.failed && level > uc.failLevel {
		// A lower unit could not be stored: its objects have no
		// numbers, so no unit above it is stored either.
		for _, i := range miss {
			uc.rec[i] = true
		}
	} else if len(miss) > 0 {
		var lower []*Unit
		for i, u := range s.units {
			if uc.level[i] < level && !uc.regd[i] {
				lower = append(lower, u)
			}
		}
		uc.register(lower)
		for _, i := range miss {
			uc.rec[i] = true
		}
	}
	s.jobs.Run(s.limit, len(miss), func(k int) {
		i := miss[k]
		s.analyzeUnit(s.units[i])
		if !uc.failed || level <= uc.failLevel {
			uc.encode(i)
		}
	})
	for _, i := range idx {
		uc.rec[i] = false
		if uc.hit[i] {
			uc.restorePSL(i)
		} else if !uc.finish(i) && !uc.failed {
			uc.failed, uc.failLevel = true, level
		}
		uc.keys[i] = uc.finalKey(i)
	}
}

// restorePSL adds the PSL expressions of restored unit i to its file.
func (uc *unitCache) restorePSL(i int) {
	f := uc.s.units[i].File
	for _, p := range uc.psl[i] {
		if f.PSL == nil {
			f.PSL = map[cst.Range]*Expr{}
		}
		f.PSL[p.at] = p.e
	}
	uc.psl[i] = nil
}

// readsOutsideDeps returns the project units unit i's job read that
// are not among its Deps, sorted.
func (s *session) readsOutsideDeps(i int) []int {
	var out []int
	for _, j := range s.reads[i] {
		if j != i && !slices.Contains(s.units[i].Deps, s.units[j]) {
			out = append(out, j)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
