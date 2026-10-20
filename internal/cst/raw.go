package cst

// Raw access for the codec of the prebuilt std/ieee bundle (#33): the
// bundle is analyzed when GoHard is built and its trees are stored and
// restored exactly, sharing and identities included. Nothing else uses
// these functions; trees are otherwise only made by a Builder.

// GreenRaw is every field of a green node or token.
type GreenRaw struct {
	Hash  uint64
	Width uint32
	NLead uint32
	Kind  uint16
	Tok   bool
	Flags Flags
	Text  string
	// Triv is the leading then the trailing trivia (NLead leading).
	Triv []Trivia
	Kids []*Green
}

// Raw returns the fields of g.
func (g *Green) Raw() GreenRaw {
	return GreenRaw{Hash: g.hash, Width: g.width, NLead: g.nlead,
		Kind: g.kind, Tok: g.tok, Flags: g.flags, Text: g.text,
		Triv: g.triv, Kids: g.kids}
}

// GreenFromRaw returns the green node with the fields r, as Raw
// returned them. The slices are kept, not copied.
func GreenFromRaw(r GreenRaw) *Green {
	return &Green{hash: r.Hash, width: r.Width, nlead: r.NLead,
		kind: r.Kind, tok: r.Tok, flags: r.Flags, text: r.Text,
		triv: r.Triv, kids: r.Kids}
}

// Raw returns the parts of n: its green node, parent, absolute offset
// and index in the parent. Two Nodes are the same red node exactly when
// they are == (Nodes are comparable).
func (n Node) Raw() (g *Green, parent Node, off, idx int) {
	if n.r == nil {
		return nil, Node{}, 0, 0
	}
	return n.r.g, Node{n.r.parent}, n.r.off, n.r.idx
}

// NodeFromRaw returns a new red node with the parts Raw returned.
func NodeFromRaw(g *Green, parent Node, off, idx int) Node {
	return Node{&red{g: g, parent: parent.r, off: off, idx: idx}}
}

// Raw returns the parts of t, as Node.Raw does.
func (t Token) Raw() (g *Green, parent Node, off, idx int) {
	return t.g, Node{t.parent}, t.off, t.idx
}

// TokenFromRaw returns the token with the parts Raw returned.
func TokenFromRaw(g *Green, parent Node, off, idx int) Token {
	return Token{g: g, parent: parent.r, off: off, idx: idx}
}
