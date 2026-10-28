// Package cst is GoHard's lossless concrete syntax tree (#10, #31).
//
// Green nodes are immutable values shared between trees: each has a kind,
// a width and children, and tokens carry their text and trivia. A green
// node's ID is a hash of its content, never of its address, so equal
// subtrees have equal IDs in every run (P1). A Builder deduplicates small
// green nodes by content while a tree is built.
//
// Red nodes (Node, Token) are cheap views on top: they add the parent and
// the absolute byte offset. Typed wrappers (EntityDecl, Expr, ...) are
// generated from kinds.yaml by tools/genkinds; they embed Node and give
// named field accessors.
//
// Concatenating the text and trivia of every token of a tree gives back
// the parsed bytes exactly, including invalid UTF-8 (the round-trip
// invariant). Source references use byte offsets or tree paths, never
// IDs, because identical code shares one ID.
package cst

//go:generate go run ../../tools/genkinds -root ../..

import (
	"slices"
	"strconv"

	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Kind is a CST node kind from kinds.yaml. The zero Kind is not valid.
type Kind uint16

type kindData struct {
	name string
	std  token.Standard
}

// String returns the kinds.yaml name of k.
func (k Kind) String() string {
	if k.Valid() {
		return kindInfo[k].name
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Valid reports whether k is a defined node kind.
func (k Kind) Valid() bool { return k > 0 && k < numKinds }

// Std returns the standard that introduces the construct (from
// kinds.yaml; documentation and test generation, not gating).
func (k Kind) Std() token.Standard {
	if k.Valid() {
		return kindInfo[k].std
	}
	return 0
}

// Kinds returns every node kind in number order.
func Kinds() []Kind {
	out := make([]Kind, 0, numKinds-1)
	for k := Kind(1); k < numKinds; k++ {
		out = append(out, k)
	}
	return out
}

// Groups returns the names of the kind groups in kinds.yaml order.
func Groups() []string { return slices.Clone(groupNames) }

// GroupKinds returns the members of the named group, or nil.
func GroupKinds(name string) []Kind {
	i := slices.Index(groupNames, name)
	if i < 0 {
		return nil
	}
	return slices.Clone(groupKinds[i])
}

// ID is a content-derived identity: equal green subtrees have equal IDs.
type ID uint64

// Flags mark tokens.
type Flags uint8

// Token flags.
const (
	// FlagMissing marks a zero-width token inserted by error recovery.
	FlagMissing Flags = 1 << iota
	// FlagInactive marks a token inside an inactive conditional-analysis
	// branch (#31).
	FlagInactive
	// FlagPSLComment marks a token scanned from a `-- psl` comment.
	FlagPSLComment
)

// Trivia is one piece of trivia: whitespace, a newline, a comment, a
// tool directive or encrypted data.
type Trivia struct {
	Kind token.Kind
	Text string
}

// Green is an immutable green node or token. Never modify one.
type Green struct {
	hash  uint64
	width uint32
	nlead uint32
	kind  uint16
	tok   bool
	flags Flags
	text  string
	triv  []Trivia
	kids  []*Green
}

// IsToken reports whether g is a token.
func (g *Green) IsToken() bool { return g.tok }

// Kind returns the node kind, or 0 for a token.
func (g *Green) Kind() Kind {
	if g.tok {
		return 0
	}
	return Kind(g.kind)
}

// TokenKind returns the token kind, or 0 for a node.
func (g *Green) TokenKind() token.Kind {
	if !g.tok {
		return 0
	}
	return token.Kind(g.kind)
}

// ID returns the content-derived identity of g.
func (g *Green) ID() ID { return ID(g.hash) }

// Width returns the length in bytes of g's text including trivia.
func (g *Green) Width() int { return int(g.width) }

// Flags returns the token flags (0 for nodes).
func (g *Green) Flags() Flags { return g.flags }

// Text returns a token's text without trivia ("" for nodes).
func (g *Green) Text() string { return g.text }

// Leading returns a token's leading trivia. Do not modify it.
func (g *Green) Leading() []Trivia { return slices.Clip(g.triv[:g.nlead]) }

// Trailing returns a token's trailing trivia. Do not modify it.
func (g *Green) Trailing() []Trivia { return slices.Clip(g.triv[g.nlead:]) }

// NumChildren returns the number of children of a node (0 for tokens).
func (g *Green) NumChildren() int { return len(g.kids) }

// Child returns child i of a node.
func (g *Green) Child(i int) *Green { return g.kids[i] }

// LeadWidth returns the byte length of a token's leading trivia, or of
// the leading trivia of a node's first token.
func (g *Green) LeadWidth() int {
	for !g.tok {
		if len(g.kids) == 0 {
			return 0
		}
		g = firstNonEmpty(g)
		if g == nil {
			return 0
		}
	}
	n := 0
	for _, t := range g.triv[:g.nlead] {
		n += len(t.Text)
	}
	return n
}

// TrailWidth returns the byte length of a token's trailing trivia, or of
// the trailing trivia of a node's last token.
func (g *Green) TrailWidth() int {
	for !g.tok {
		g = lastNonEmpty(g)
		if g == nil {
			return 0
		}
	}
	n := 0
	for _, t := range g.triv[g.nlead:] {
		n += len(t.Text)
	}
	return n
}

// firstNonEmpty returns the first child of g with a non-zero width, or
// the first child if all are empty, or nil.
func firstNonEmpty(g *Green) *Green {
	for _, k := range g.kids {
		if k.width > 0 {
			return k
		}
	}
	return nil
}

func lastNonEmpty(g *Green) *Green {
	for i := len(g.kids) - 1; i >= 0; i-- {
		if g.kids[i].width > 0 {
			return g.kids[i]
		}
	}
	return nil
}

// AppendText appends the full text of g (with trivia) to dst.
func (g *Green) AppendText(dst []byte) []byte {
	if g.tok {
		for _, t := range g.triv[:g.nlead] {
			dst = append(dst, t.Text...)
		}
		dst = append(dst, g.text...)
		for _, t := range g.triv[g.nlead:] {
			dst = append(dst, t.Text...)
		}
		return dst
	}
	for _, k := range g.kids {
		dst = k.AppendText(dst)
	}
	return dst
}

// String returns the full text of g.
func (g *Green) String() string {
	return string(g.AppendText(make([]byte, 0, g.width)))
}

// Equal reports whether a and b have the same content. It compares IDs
// first and walks the trees only when they match.
func Equal(a, b *Green) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.hash != b.hash || a.width != b.width ||
		a.kind != b.kind || a.tok != b.tok || a.flags != b.flags {
		return false
	}
	if a.tok {
		return a.text == b.text && a.nlead == b.nlead &&
			slices.Equal(a.triv, b.triv)
	}
	return slices.EqualFunc(a.kids, b.kids, Equal)
}

// Hashing: FNV-1a over bytes, combined with a 64-bit mixer. Fixed
// constants, so IDs are the same in every run and on every platform.
const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func hashString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}
	return h
}

func mix(h, v uint64) uint64 {
	h ^= v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
	h ^= h >> 31
	h *= 0xbf58476d1ce4e5b9
	return h
}

func tokenHash(k token.Kind, f Flags, text string, nlead int,
	triv []Trivia,
) uint64 {
	h := mix(fnvOffset, uint64(k)|uint64(f)<<16|1<<24)
	h = mix(h, hashString(fnvOffset, text))
	h = mix(h, uint64(nlead))
	for _, t := range triv {
		h = mix(h, uint64(t.Kind))
		h = mix(h, hashString(fnvOffset, t.Text))
	}
	return h
}

func nodeHash(k Kind, kids []*Green) uint64 {
	h := mix(fnvOffset, uint64(k)|2<<24)
	for _, c := range kids {
		h = mix(h, c.hash)
	}
	return h
}
