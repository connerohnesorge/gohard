package cst

import (
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// maxInternKids is the largest child count of a node the Builder
// deduplicates; tokens are always deduplicated.
const maxInternKids = 4

// Builder builds a green tree bottom-up, in source order. It is used by
// one goroutine at a time and is not reusable after Finish.
type Builder struct {
	stack []frame
	kids  []*Green
	cache map[uint64]*Green
	nodes int

	greens []Green
	trivs  []Trivia
	slots  []*Green

	gsize, tsize, ssize int
}

type frame struct {
	kind  Kind
	start int
}

// Checkpoint marks a position among the children of the open node; see
// StartNodeAt.
type Checkpoint int

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{cache: make(map[uint64]*Green)}
}

// Nodes returns the number of nodes finished so far.
func (b *Builder) Nodes() int { return b.nodes }

// Depth returns the number of open nodes.
func (b *Builder) Depth() int { return len(b.stack) }

// Token appends a token to the open node. text, lead and trail are
// copied; the token's width is their total length.
func (b *Builder) Token(k token.Kind, text string, lead, trail []Trivia,
	f Flags,
) {
	nl := len(lead)
	n := nl + len(trail)
	// Hash without building the trivia slice.
	h := mix(fnvOffset, uint64(k)|uint64(f)<<16|1<<24)
	h = mix(h, hashString(fnvOffset, text))
	h = mix(h, uint64(nl))
	w := len(text)
	for _, t := range lead {
		h = mix(h, uint64(t.Kind))
		h = mix(h, hashString(fnvOffset, t.Text))
		w += len(t.Text)
	}
	for _, t := range trail {
		h = mix(h, uint64(t.Kind))
		h = mix(h, hashString(fnvOffset, t.Text))
		w += len(t.Text)
	}
	if g, ok := b.cache[h]; ok && g.tok && token.Kind(g.kind) == k &&
		g.flags == f && g.text == text && int(g.nlead) == nl &&
		len(g.triv) == n && sameTrivia(g.triv[:nl], lead) &&
		sameTrivia(g.triv[nl:], trail) {
		b.kids = append(b.kids, g)
		return
	}
	g := b.newGreen()
	g.hash, g.width, g.nlead = h, uint32(w), uint32(nl)
	g.kind, g.tok, g.flags, g.text = uint16(k), true, f, text
	if n > 0 {
		tv := b.newTrivia(n)
		copy(tv, lead)
		copy(tv[nl:], trail)
		g.triv = tv
	}
	if _, ok := b.cache[h]; !ok {
		b.cache[h] = g
	}
	b.kids = append(b.kids, g)
}

func sameTrivia(a, b []Trivia) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// StartNode opens a node of kind k; tokens and nodes appended until the
// matching FinishNode become its children.
func (b *Builder) StartNode(k Kind) {
	b.stack = append(b.stack, frame{kind: k, start: len(b.kids)})
}

// Checkpoint returns the current position among the open node's
// children.
func (b *Builder) Checkpoint() Checkpoint { return Checkpoint(len(b.kids)) }

// StartNodeAt opens a node of kind k that starts at checkpoint c: the
// children appended since c become its first children. It is how a
// parser wraps an already-built left operand (a binary expression, a
// name suffix). c must belong to the currently open node.
func (b *Builder) StartNodeAt(c Checkpoint, k Kind) {
	start := min(max(int(c), 0), len(b.kids))
	if n := len(b.stack); n > 0 && start < b.stack[n-1].start {
		start = b.stack[n-1].start
	}
	b.stack = append(b.stack, frame{kind: k, start: start})
}

// FinishNode closes the innermost open node. With no open node it does
// nothing.
func (b *Builder) FinishNode() {
	n := len(b.stack)
	if n == 0 {
		return
	}
	f := b.stack[n-1]
	b.stack = b.stack[:n-1]
	kids := b.kids[f.start:]
	g := b.node(f.kind, kids)
	b.kids = append(b.kids[:f.start], g)
}

func (b *Builder) node(k Kind, kids []*Green) *Green {
	b.nodes++
	h := nodeHash(k, kids)
	if len(kids) <= maxInternKids {
		if g, ok := b.cache[h]; ok && !g.tok && Kind(g.kind) == k &&
			samePtrs(g.kids, kids) {
			return g
		}
	}
	w := 0
	for _, c := range kids {
		w += int(c.width)
	}
	g := b.newGreen()
	g.hash, g.width, g.kind = h, uint32(w), uint16(k)
	if len(kids) > 0 {
		s := b.newSlots(len(kids))
		copy(s, kids)
		g.kids = s
	}
	if len(kids) <= maxInternKids {
		if _, ok := b.cache[h]; !ok {
			b.cache[h] = g
		}
	}
	return g
}

func samePtrs(a, b []*Green) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Finish closes every open node and returns the root. If the top level
// holds exactly one node, that node is the root; otherwise the elements
// are wrapped in a KindErrorNode, so Finish never loses text.
func (b *Builder) Finish() *Green {
	for len(b.stack) > 0 {
		b.FinishNode()
	}
	if len(b.kids) == 1 && !b.kids[0].tok {
		return b.kids[0]
	}
	return b.node(KindErrorNode, b.kids)
}

// Arena allocation: greens, trivia and child slots come from chunks so
// building a tree does not allocate per token. Chunks start small and
// double up to maxChunk, so small files stay cheap.
const (
	minChunk = 64
	maxChunk = 4096
)

func grow(prev int) int { return min(max(2*prev, minChunk), maxChunk) }

func (b *Builder) newGreen() *Green {
	if len(b.greens) == 0 {
		b.gsize = grow(b.gsize)
		b.greens = make([]Green, b.gsize)
	}
	g := &b.greens[0]
	b.greens = b.greens[1:]
	return g
}

func (b *Builder) newTrivia(n int) []Trivia {
	if n > minChunk/2 {
		return make([]Trivia, n)
	}
	if len(b.trivs) < n {
		b.tsize = grow(b.tsize)
		b.trivs = make([]Trivia, b.tsize)
	}
	s := b.trivs[:n:n]
	b.trivs = b.trivs[n:]
	return s
}

func (b *Builder) newSlots(n int) []*Green {
	if n > minChunk/2 {
		return make([]*Green, n)
	}
	if len(b.slots) < n {
		b.ssize = grow(b.ssize)
		b.slots = make([]*Green, b.ssize)
	}
	s := b.slots[:n:n]
	b.slots = b.slots[n:]
	return s
}
