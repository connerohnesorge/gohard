package sema

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/stdlib"
	"github.com/connerohnesorge/gohard/internal/token"
)

// The codec of the prebuilt bundle (#33). The analyzed std/ieee bundle
// is a graph: green trees shared by hash-consing, red nodes, and the
// sema model's declarations, regions, types and expressions pointing at
// each other in cycles. The codec stores the whole graph exactly:
// every pointer keeps its identity (two fields that share an object
// share it after decoding), nil stays nil, and an empty slice or map
// stays empty but non-nil.
//
// Format: the header (codecMagic, codecSchema, the sources hash) and
// then a stream of varints. Every pointer is 0 (nil), 1 (a new object,
// whose fields follow) or n+2 (the object with id n of its type). The
// sema objects are visited breadth first (a queue of objects whose
// fields are still to be written), so the depth of the graph does not
// reach the Go stack; green nodes and red nodes are written depth first
// (children and parents before the node: trees are shallow and their
// constructors need the parts). Maps are written in key order; maps
// keyed by a pointer are written after every other object (then every
// key has an id) in key id order. Strings and byte slices are
// interned. The shared objects of the sema and eval packages
// (UniversalInteger, eval.Integer, ...) are seeded into the tables at
// fixed ids, so they keep their identity in the decoded graph.
//
// The field-by-field code is generated from the type definitions
// (codec_gen.go, by tools/gencodec), so a new field is a stale file
// that the generated-file sync check (#30) fails. codecSchema is the
// hash of that code: a blob from a different schema is not decoded.

//go:generate go run ../../tools/gencodec -root ../..
//go:generate go run ../../tools/prebuild -root ../..

const codecMagic = "gohard-bundle\x00"

// errCodec reports a blob that is not a bundle of this build.
var errCodec = errors.New("sema: prebuilt bundle does not match")

// sourcesHash is the hash of the bundled sources the bundle is analyzed
// from (the vendored files in analysis order and the LRM contexts).
func sourcesHash() string {
	var b []byte
	for _, f := range stdlib.Files() {
		src, _ := stdlib.Source(f.Path)
		b = append(b, f.Path...)
		b = append(b, 0)
		b = append(b, stdlib.HashHex(src)...)
		b = append(b, 0)
	}
	for _, c := range lrmContexts {
		b = append(b, c.path...)
		b = append(b, 0)
		b = append(b, c.text...)
		b = append(b, 0)
	}
	return stdlib.HashHex(b)
}

// encTable assigns ids to the objects of one pointer type.
type encTable[T any] struct {
	ids  map[*T]uint64
	list []*T
}

func (t *encTable[T]) seed(ps ...*T) {
	for _, p := range ps {
		t.add(p)
	}
}

func (t *encTable[T]) add(p *T) {
	if t.ids == nil {
		t.ids = map[*T]uint64{}
	}
	t.ids[p] = uint64(len(t.list))
	t.list = append(t.list, p)
}

// ref writes p; a new object is queued for fill.
func (t *encTable[T]) ref(e *encoder, p *T, fill func(*T)) {
	if p == nil {
		e.uint(0)
		return
	}
	if id, ok := t.ids[p]; ok {
		e.uint(id + 2)
		return
	}
	t.add(p)
	e.uint(1)
	e.queue = append(e.queue, func() { fill(p) })
}

// decTable holds the decoded objects of one pointer type by id.
type decTable[T any] struct{ list []*T }

func (t *decTable[T]) seed(ps ...*T) { t.list = append(t.list, ps...) }

func (t *decTable[T]) ref(d *decoder, fill func(*T)) *T {
	n := d.uint()
	switch {
	case n == 0:
		return nil
	case n == 1:
		p := new(T)
		t.list = append(t.list, p)
		d.queue = append(d.queue, func() { fill(p) })
		return p
	case n-2 < uint64(len(t.list)):
		return t.list[n-2]
	}
	d.fail()
	return nil
}

type encoder struct {
	buf   []byte
	strs  map[string]uint64
	blobs map[string]uint64
	// queue holds the objects whose fields are still to be written,
	// later the maps keyed by pointers.
	queue, later []func()
	greens       map[*cst.Green]uint64
	nodes        map[cst.Node]uint64
	results      encTable[parser.Result]
	t            encTables
}

type decoder struct {
	buf          []byte
	off          int
	bad          bool
	strs         []string
	blobs        [][]byte
	queue, later []func()
	greens       []*cst.Green
	nodes        []cst.Node
	results      decTable[parser.Result]
	t            decTables
}

func (e *encoder) uint(v uint64) { e.buf = binary.AppendUvarint(e.buf, v) }
func (e *encoder) int(v int64)   { e.buf = binary.AppendVarint(e.buf, v) }
func (e *encoder) float(v float64) {
	e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v))
}

func (e *encoder) bool(v bool) {
	if v {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

// str writes s interned: 0 and the bytes the first time, id+1 after.
func (e *encoder) str(s string) {
	if id, ok := e.strs[s]; ok {
		e.uint(id + 1)
		return
	}
	e.strs[s] = uint64(len(e.strs))
	e.uint(0)
	e.uint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

// bytes writes b interned by content: 0 nil, 1 and the bytes the first
// time, id+2 after.
func (e *encoder) bytes(b []byte) {
	if b == nil {
		e.uint(0)
		return
	}
	if id, ok := e.blobs[string(b)]; ok {
		e.uint(id + 2)
		return
	}
	e.blobs[string(b)] = uint64(len(e.blobs))
	e.uint(1)
	e.uint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

func (d *decoder) fail() {
	d.bad = true
	d.off = len(d.buf)
}

func (d *decoder) uint() uint64 {
	v, n := binary.Uvarint(d.buf[d.off:])
	if n <= 0 {
		d.fail()
		return 0
	}
	d.off += n
	return v
}

func (d *decoder) int() int64 {
	v, n := binary.Varint(d.buf[d.off:])
	if n <= 0 {
		d.fail()
		return 0
	}
	d.off += n
	return v
}

func (d *decoder) float() float64 {
	if len(d.buf)-d.off < 8 {
		d.fail()
		return 0
	}
	v := binary.LittleEndian.Uint64(d.buf[d.off:])
	d.off += 8
	return math.Float64frombits(v)
}

func (d *decoder) bool() bool {
	if d.off >= len(d.buf) {
		d.fail()
		return false
	}
	v := d.buf[d.off]
	d.off++
	if v > 1 {
		d.fail() // the encoder writes 0 or 1
	}
	return v != 0
}

func (d *decoder) raw(n uint64) []byte {
	if uint64(len(d.buf)-d.off) < n {
		d.fail()
		return nil
	}
	b := d.buf[d.off : d.off+int(n)]
	d.off += int(n)
	return b
}

// count reads a length written as n+1 (0 is nil); ok is false for nil.
// A length beyond the remaining bytes fails the decoding.
func (d *decoder) count() (n int, ok bool) {
	v := d.uint()
	if v == 0 {
		return 0, false
	}
	if v-1 > uint64(len(d.buf)-d.off) {
		d.fail()
		return 0, false
	}
	return int(v - 1), true
}

// length reads a plain length, at most the remaining bytes.
func (d *decoder) length() int {
	v := d.uint()
	if v > uint64(len(d.buf)-d.off) {
		d.fail()
		return 0
	}
	return int(v)
}

func (d *decoder) str() string {
	id := d.uint()
	if id > 0 {
		if id-1 < uint64(len(d.strs)) {
			return d.strs[id-1]
		}
		d.fail()
		return ""
	}
	s := string(d.raw(uint64(d.length())))
	d.strs = append(d.strs, s)
	return s
}

func (d *decoder) bytes() []byte {
	switch id := d.uint(); {
	case id == 0:
		return nil
	case id == 1:
		b := bytes.Clone(d.raw(d.uint()))
		if b == nil {
			b = []byte{}
		}
		d.blobs = append(d.blobs, b)
		return b
	case id-2 < uint64(len(d.blobs)):
		return d.blobs[id-2]
	}
	d.fail()
	return nil
}

// green writes g depth first; its id is assigned after its children.
func (e *encoder) green(g *cst.Green) {
	if g == nil {
		e.uint(0)
		return
	}
	if id, ok := e.greens[g]; ok {
		e.uint(id + 2)
		return
	}
	e.uint(1)
	r := g.Raw()
	e.buf = binary.LittleEndian.AppendUint64(e.buf, r.Hash)
	e.uint(uint64(r.Width))
	e.uint(uint64(r.NLead))
	e.uint(uint64(r.Kind))
	e.bool(r.Tok)
	e.uint(uint64(r.Flags))
	e.str(r.Text)
	e.uint(uint64(len(r.Triv)))
	for _, t := range r.Triv {
		e.uint(uint64(t.Kind))
		e.str(t.Text)
	}
	e.uint(uint64(len(r.Kids)))
	for _, k := range r.Kids {
		e.green(k)
	}
	e.greens[g] = uint64(len(e.greens))
}

func (d *decoder) green() *cst.Green {
	switch id := d.uint(); {
	case id == 0:
		return nil
	case id >= 2:
		if id-2 < uint64(len(d.greens)) {
			return d.greens[id-2]
		}
		d.fail()
		return nil
	}
	var r cst.GreenRaw
	h := d.raw(8)
	if len(h) == 8 {
		r.Hash = binary.LittleEndian.Uint64(h)
	}
	r.Width = uint32(d.uint())
	r.NLead = uint32(d.uint())
	r.Kind = uint16(d.uint())
	r.Tok = d.bool()
	r.Flags = cst.Flags(d.uint())
	r.Text = d.str()
	if n := d.length(); n > 0 {
		r.Triv = make([]cst.Trivia, n)
		for i := range r.Triv {
			r.Triv[i].Kind = token.Kind(d.uint())
			r.Triv[i].Text = d.str()
		}
	}
	if n := d.length(); n > 0 {
		r.Kids = make([]*cst.Green, n)
		for i := range r.Kids {
			r.Kids[i] = d.green()
		}
	}
	if d.bad {
		return nil
	}
	g := cst.GreenFromRaw(r)
	d.greens = append(d.greens, g)
	return g
}

// node writes a red node: its parent chain first, its id after.
func (e *encoder) node(n cst.Node) {
	if n.IsZero() {
		e.uint(0)
		return
	}
	if id, ok := e.nodes[n]; ok {
		e.uint(id + 2)
		return
	}
	e.uint(1)
	g, parent, off, idx := n.Raw()
	e.green(g)
	e.node(parent)
	e.int(int64(off))
	e.int(int64(idx))
	e.nodes[n] = uint64(len(e.nodes))
}

func (d *decoder) node() cst.Node {
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
	g := d.green()
	parent := d.node()
	off, idx := int(d.int()), int(d.int())
	if d.bad {
		return cst.Node{}
	}
	n := cst.NodeFromRaw(g, parent, off, idx)
	d.nodes = append(d.nodes, n)
	return n
}

func (e *encoder) token(t cst.Token) {
	g, parent, off, idx := t.Raw()
	e.green(g)
	e.node(parent)
	e.int(int64(off))
	e.int(int64(idx))
}

func (d *decoder) token() cst.Token {
	g := d.green()
	parent := d.node()
	off, idx := int(d.int()), int(d.int())
	return cst.TokenFromRaw(g, parent, off, idx)
}

// lines writes whether a file has its line index; the index is rebuilt
// from the file's text, which it must index.
func (e *encoder) lines(x *span.Index, text []byte) {
	if x != nil && x.Len() != len(text) {
		panic("sema: codec: line index of another text")
	}
	e.bool(x != nil)
}

func (e *encoder) parseResult(p *parser.Result) {
	e.results.ref(e, p, func(r *parser.Result) {
		e.s_parser_Result(r)
		e.bytes(r.Source())
	})
}

func (d *decoder) parseResult() *parser.Result {
	return d.results.ref(d, func(r *parser.Result) {
		var v parser.Result
		d.s_parser_Result(&v)
		*r = *parser.Restore(v, d.bytes())
	})
}

// seedTables gives the shared objects of sema and eval their fixed ids.
func seedTables(seed func(ts []*Type, es []*eval.Type)) {
	seed([]*Type{UniversalInteger, UniversalReal, UnknownType, NullType,
		anyComposite},
		[]*eval.Type{eval.UniversalInteger, eval.UniversalReal,
			eval.Boolean, eval.Bit, eval.Character, eval.SeverityLevel,
			eval.Integer, eval.Real, eval.Time, eval.Natural,
			eval.Positive, eval.String, eval.BitVector})
}

// drain writes the queued objects, then the deferred maps, until both
// are empty.
func drain(queue, later *[]func()) {
	for {
		for i := 0; i < len(*queue); i++ {
			(*queue)[i]()
		}
		*queue = (*queue)[:0]
		if len(*later) == 0 {
			return
		}
		f := (*later)[0]
		*later = (*later)[1:]
		f()
	}
}

// encodeBundle returns the stored form of the bundle r.
func encodeBundle(r *Result) []byte {
	e := &encoder{strs: map[string]uint64{}, blobs: map[string]uint64{},
		greens: map[*cst.Green]uint64{}, nodes: map[cst.Node]uint64{}}
	seedTables(func(ts []*Type, es []*eval.Type) {
		e.t.p_Type.seed(ts...)
		e.t.p_eval_Type.seed(es...)
	})
	e.buf = append(e.buf, codecMagic...)
	e.str(codecSchema)
	e.str(sourcesHash())
	e.p_Result(r)
	drain(&e.queue, &e.later)
	return e.buf
}

// decodeBundle restores a bundle encodeBundle stored. A blob of another
// schema or other sources, or a damaged one, is errCodec.
func decodeBundle(b []byte) (*Result, error) {
	if !bytes.HasPrefix(b, []byte(codecMagic)) {
		return nil, errCodec
	}
	d := &decoder{buf: b, off: len(codecMagic)}
	if d.str() != codecSchema || d.str() != sourcesHash() {
		return nil, errCodec
	}
	seedTables(func(ts []*Type, es []*eval.Type) {
		d.t.p_Type.seed(ts...)
		d.t.p_eval_Type.seed(es...)
	})
	r := d.p_Result()
	drain(&d.queue, &d.later)
	if d.bad || d.off != len(d.buf) || r == nil {
		return nil, errCodec
	}
	return r, nil
}
