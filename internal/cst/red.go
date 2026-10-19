package cst

import (
	"iter"

	"github.com/connerohnesorge/gohard/internal/token"
)

// Range is a half-open byte range [Start, End) in the parsed text.
type Range struct {
	Start int
	End   int
}

// Len returns End - Start.
func (r Range) Len() int { return r.End - r.Start }

// red is the shared part of a red node: its green node, parent and
// absolute offset (start of its full text).
type red struct {
	g      *Green
	parent *red
	off    int
	idx    int
}

// Node is a red node: a green node plus its parent and absolute offset.
// The zero Node is "no node"; every method on it returns zero values.
type Node struct{ r *red }

// NewRoot returns the red root of the green tree g (offset 0). g must
// be a node, not a token.
func NewRoot(g *Green) Node {
	if g == nil || g.tok {
		return Node{}
	}
	return Node{&red{g: g}}
}

// IsZero reports whether n is the zero Node.
func (n Node) IsZero() bool { return n.r == nil }

// Kind returns the node's kind (0 for the zero Node).
func (n Node) Kind() Kind {
	if n.r == nil {
		return 0
	}
	return Kind(n.r.g.kind)
}

// Green returns the node's green node.
func (n Node) Green() *Green {
	if n.r == nil {
		return nil
	}
	return n.r.g
}

// ID returns the content-derived identity of the node.
func (n Node) ID() ID {
	if n.r == nil {
		return 0
	}
	return n.r.g.ID()
}

// Equal reports whether n and m are the same node of the same tree
// position (same green node at the same offset and depth).
func (n Node) Equal(m Node) bool {
	if n.r == nil || m.r == nil {
		return n.r == m.r
	}
	return n.r.g == m.r.g && n.r.off == m.r.off && n.depth() == m.depth()
}

func (n Node) depth() int {
	d := 0
	for r := n.r; r != nil; r = r.parent {
		d++
	}
	return d
}

// Parent returns the parent node, or the zero Node for the root.
func (n Node) Parent() Node {
	if n.r == nil || n.r.parent == nil {
		return Node{}
	}
	return Node{n.r.parent}
}

// Ancestors yields the parent, its parent, and so on up to the root.
func (n Node) Ancestors() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		for p := n.Parent(); !p.IsZero(); p = p.Parent() {
			if !yield(p) {
				return
			}
		}
	}
}

// Index returns the node's position among its parent's children.
func (n Node) Index() int {
	if n.r == nil {
		return 0
	}
	return n.r.idx
}

// Offset returns the absolute offset of the start of the node's full
// text (including the leading trivia of its first token).
func (n Node) Offset() int {
	if n.r == nil {
		return 0
	}
	return n.r.off
}

// FullSpan returns the node's range including all trivia.
func (n Node) FullSpan() Range {
	if n.r == nil {
		return Range{}
	}
	return Range{n.r.off, n.r.off + int(n.r.g.width)}
}

// Span returns the node's range without the leading trivia of its first
// token and the trailing trivia of its last token. A node with no text
// gives an empty range at its offset.
func (n Node) Span() Range {
	if n.r == nil {
		return Range{}
	}
	g := n.r.g
	start := n.r.off + g.LeadWidth()
	end := n.r.off + int(g.width) - g.TrailWidth()
	if end < start {
		end = start
	}
	return Range{start, end}
}

// Text returns the node's full text, trivia included.
func (n Node) Text() string {
	if n.r == nil {
		return ""
	}
	return n.r.g.String()
}

// NumChildren returns the number of children (nodes and tokens).
func (n Node) NumChildren() int {
	if n.r == nil {
		return 0
	}
	return len(n.r.g.kids)
}

// Element is a child of a node: exactly one of Node and Token is
// non-zero.
type Element struct {
	Node  Node
	Token Token
}

// IsToken reports whether e is a token.
func (e Element) IsToken() bool { return !e.Token.IsZero() }

// FullSpan returns the element's range including trivia.
func (e Element) FullSpan() Range {
	if e.IsToken() {
		return e.Token.FullSpan()
	}
	return e.Node.FullSpan()
}

func (n Node) elem(i, off int) Element {
	g := n.r.g.kids[i]
	if g.tok {
		return Element{Token: Token{g: g, parent: n.r, off: off, idx: i}}
	}
	return Element{Node: Node{&red{g: g, parent: n.r, off: off, idx: i}}}
}

// Element returns child i.
func (n Node) Element(i int) Element {
	if n.r == nil || i < 0 || i >= len(n.r.g.kids) {
		return Element{}
	}
	off := n.r.off
	for _, k := range n.r.g.kids[:i] {
		off += int(k.width)
	}
	return n.elem(i, off)
}

// Elements yields every child in order.
func (n Node) Elements() iter.Seq[Element] {
	return func(yield func(Element) bool) {
		if n.r == nil {
			return
		}
		off := n.r.off
		for i, k := range n.r.g.kids {
			if !yield(n.elem(i, off)) {
				return
			}
			off += int(k.width)
		}
	}
}

// Nodes yields every child node in order.
func (n Node) Nodes() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		for e := range n.Elements() {
			if !e.IsToken() && !yield(e.Node) {
				return
			}
		}
	}
}

// Tokens yields every direct child token in order.
func (n Node) Tokens() iter.Seq[Token] {
	return func(yield func(Token) bool) {
		for e := range n.Elements() {
			if e.IsToken() && !yield(e.Token) {
				return
			}
		}
	}
}

// Token returns the nth (from 0) direct child token of kind k, or the
// zero Token.
func (n Node) Token(k token.Kind, nth int) Token {
	for t := range n.Tokens() {
		if t.Kind() == k {
			if nth == 0 {
				return t
			}
			nth--
		}
	}
	return Token{}
}

// TokenIn returns the nth (from 0) direct child token whose kind is in
// s, or the zero Token.
func (n Node) TokenIn(s token.Set, nth int) Token {
	for t := range n.Tokens() {
		if s.Has(t.Kind()) {
			if nth == 0 {
				return t
			}
			nth--
		}
	}
	return Token{}
}

// TokensIn yields every direct child token whose kind is in s.
func (n Node) TokensIn(s token.Set) iter.Seq[Token] {
	return func(yield func(Token) bool) {
		for t := range n.Tokens() {
			if s.Has(t.Kind()) && !yield(t) {
				return
			}
		}
	}
}

// View is implemented by the typed wrappers: a struct embedding Node
// that accepts some node kinds.
type View interface {
	~struct{ Node }
	Accepts(Kind) bool
}

// As returns n as the typed wrapper T if T accepts n's kind.
func As[T View](n Node) (T, bool) {
	var z T
	if n.r == nil || !z.Accepts(n.Kind()) {
		return z, false
	}
	return T{n}, true
}

// Descendants yields n and every node below it in preorder.
func (n Node) Descendants() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		if n.r != nil {
			n.walk(yield)
		}
	}
}

func (n Node) walk(yield func(Node) bool) bool {
	if !yield(n) {
		return false
	}
	for c := range n.Nodes() {
		if !c.walk(yield) {
			return false
		}
	}
	return true
}

// DescendantTokens yields every token below n in source order.
func (n Node) DescendantTokens() iter.Seq[Token] {
	return func(yield func(Token) bool) {
		if n.r != nil {
			n.walkTokens(yield)
		}
	}
}

func (n Node) walkTokens(yield func(Token) bool) bool {
	for e := range n.Elements() {
		if e.IsToken() {
			if !yield(e.Token) {
				return false
			}
		} else if !e.Node.walkTokens(yield) {
			return false
		}
	}
	return true
}

// FirstToken returns the first token below n (possibly a missing,
// zero-width token), or the zero Token.
func (n Node) FirstToken() Token {
	for t := range n.DescendantTokens() {
		return t
	}
	return Token{}
}

// LastToken returns the last token below n, or the zero Token.
func (n Node) LastToken() Token {
	if n.r == nil {
		return Token{}
	}
	for i := n.NumChildren() - 1; i >= 0; i-- {
		e := n.Element(i)
		if e.IsToken() {
			return e.Token
		}
		if t := e.Node.LastToken(); !t.IsZero() {
			return t
		}
	}
	return Token{}
}

// TokenAt returns the token whose full span (with trivia) contains off:
// the last non-empty token starting at or before off. Offsets past the
// end give the last non-empty token.
func (n Node) TokenAt(off int) Token {
	cur := n
	for !cur.IsZero() {
		var pick Element
		found := false
		for e := range cur.Elements() {
			sp := e.FullSpan()
			if sp.Start > off {
				break
			}
			if sp.Len() > 0 {
				pick, found = e, true
			}
		}
		if !found {
			pick = cur.Element(0)
		}
		if pick.IsToken() {
			return pick.Token
		}
		cur = pick.Node
	}
	return Token{}
}

// Covering returns the smallest node below or at n whose Span contains
// [start, end).
func (n Node) Covering(start, end int) Node {
	cur := n
	for {
		var found Node
		for c := range cur.Nodes() {
			sp := c.Span()
			if sp.Start <= start && end <= sp.End && sp.Len() > 0 {
				found = c
				break
			}
		}
		if found.IsZero() {
			return cur
		}
		cur = found
	}
}

// Inactive reports whether n is inside (or is) an inactive
// conditional-analysis branch.
func (n Node) Inactive() bool {
	for r := n.r; r != nil; r = r.parent {
		if Kind(r.g.kind) == KindInactiveBranch {
			return true
		}
	}
	return false
}

// Token is a red token: a green token plus its parent and offset.
type Token struct {
	g      *Green
	parent *red
	off    int // start of the full text (leading trivia)
	idx    int
}

// IsZero reports whether t is the zero Token.
func (t Token) IsZero() bool { return t.g == nil }

// Kind returns the token kind (0 for the zero Token).
func (t Token) Kind() token.Kind {
	if t.g == nil {
		return 0
	}
	return token.Kind(t.g.kind)
}

// Green returns the green token.
func (t Token) Green() *Green { return t.g }

// Text returns the token's text without trivia.
func (t Token) Text() string {
	if t.g == nil {
		return ""
	}
	return t.g.text
}

// Flags returns the token's flags.
func (t Token) Flags() Flags {
	if t.g == nil {
		return 0
	}
	return t.g.flags
}

// Missing reports whether the token was inserted by error recovery.
func (t Token) Missing() bool { return t.Flags()&FlagMissing != 0 }

// Inactive reports whether the token is in an inactive branch.
func (t Token) Inactive() bool { return t.Flags()&FlagInactive != 0 }

// Leading returns the leading trivia.
func (t Token) Leading() []Trivia {
	if t.g == nil {
		return nil
	}
	return t.g.Leading()
}

// Trailing returns the trailing trivia.
func (t Token) Trailing() []Trivia {
	if t.g == nil {
		return nil
	}
	return t.g.Trailing()
}

// Span returns the range of the token's text without trivia.
func (t Token) Span() Range {
	if t.g == nil {
		return Range{}
	}
	start := t.off
	for _, tr := range t.g.triv[:t.g.nlead] {
		start += len(tr.Text)
	}
	return Range{start, start + len(t.g.text)}
}

// FullSpan returns the token's range including its trivia.
func (t Token) FullSpan() Range {
	if t.g == nil {
		return Range{}
	}
	return Range{t.off, t.off + int(t.g.width)}
}

// Parent returns the node that contains the token.
func (t Token) Parent() Node {
	if t.parent == nil {
		return Node{}
	}
	return Node{t.parent}
}

// Index returns the token's position among its parent's children.
func (t Token) Index() int { return t.idx }

// Next returns the next token in source order, or the zero Token at the
// end of the tree.
func (t Token) Next() Token {
	if t.g == nil {
		return Token{}
	}
	p, i := t.Parent(), t.idx
	for !p.IsZero() {
		for j := i + 1; j < p.NumChildren(); j++ {
			e := p.Element(j)
			if e.IsToken() {
				return e.Token
			}
			if f := e.Node.FirstToken(); !f.IsZero() {
				return f
			}
		}
		i = p.Index()
		p = p.Parent()
	}
	return Token{}
}

// Prev returns the previous token in source order, or the zero Token.
func (t Token) Prev() Token {
	if t.g == nil {
		return Token{}
	}
	p, i := t.Parent(), t.idx
	for !p.IsZero() {
		for j := i - 1; j >= 0; j-- {
			e := p.Element(j)
			if e.IsToken() {
				return e.Token
			}
			if l := e.Node.LastToken(); !l.IsZero() {
				return l
			}
		}
		i = p.Index()
		p = p.Parent()
	}
	return Token{}
}
