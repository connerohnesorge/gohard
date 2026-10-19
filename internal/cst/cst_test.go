package cst

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/token"
)

// genTree builds a random tree from r: the reference model is the flat
// list of (token kind, text, trivia) in source order, which the tree
// must reproduce exactly.
type tok struct {
	kind        token.Kind
	text        string
	lead, trail []Trivia
}

var texts = []string{"a", "b", ";", "entity", "", "\xff", "é", "(", ")"}

var trivs = []Trivia{
	{token.Whitespace, " "}, {token.Newline, "\n"},
	{token.LineComment, "-- c"}, {token.BlockComment, "/* b */"},
}

func randTok(r *env.SplitMix64) tok {
	t := tok{
		kind: token.Kind(1 + r.Uint64()%40),
		text: texts[r.Uint64()%uint64(len(texts))],
	}
	for range r.Uint64() % 3 {
		t.lead = append(t.lead, trivs[r.Uint64()%uint64(len(trivs))])
	}
	for range r.Uint64() % 2 {
		t.trail = append(t.trail, trivs[r.Uint64()%uint64(len(trivs))])
	}
	return t
}

// build emits a random tree into b, appending its tokens to flat.
func build(b *Builder, r *env.SplitMix64, depth int, flat *[]tok) {
	n := int(r.Uint64() % 5)
	for range n {
		if depth < 4 && r.Uint64()%3 == 0 {
			b.StartNode(Kind(1 + r.Uint64()%10))
			build(b, r, depth+1, flat)
			b.FinishNode()
			continue
		}
		t := randTok(r)
		b.Token(t.kind, t.text, t.lead, t.trail, 0)
		*flat = append(*flat, t)
	}
}

func randomTree(seed uint64) (*Green, []tok) {
	r := &env.SplitMix64{State: seed}
	b := NewBuilder()
	b.StartNode(KindDesignFile)
	var flat []tok
	build(b, r, 0, &flat)
	b.FinishNode()
	return b.Finish(), flat
}

func flatText(flat []tok) string {
	var s strings.Builder
	for _, t := range flat {
		for _, tr := range t.lead {
			s.WriteString(tr.Text)
		}
		s.WriteString(t.text)
		for _, tr := range t.trail {
			s.WriteString(tr.Text)
		}
	}
	return s.String()
}

// TestTreeModel: on random trees the text, token order, red offsets and
// parent links agree with the flat reference model.
func TestTreeModel(t *testing.T) {
	for seed := range uint64(500) {
		g, flat := randomTree(seed)
		want := flatText(flat)
		if g.String() != want || g.Width() != len(want) {
			t.Fatalf("seed %d: text %q, want %q", seed, g.String(), want)
		}
		root := NewRoot(g)
		i, off := 0, 0
		for tk := range root.DescendantTokens() {
			ft := flat[i]
			if tk.Kind() != ft.kind || tk.Text() != ft.text {
				t.Fatalf("seed %d: token %d mismatch", seed, i)
			}
			if tk.FullSpan().Start != off {
				t.Fatalf("seed %d: token %d at %d, want %d", seed, i,
					tk.FullSpan().Start, off)
			}
			sp := tk.Span()
			if want[sp.Start:sp.End] != ft.text {
				t.Fatalf("seed %d: span text", seed)
			}
			p := tk.Parent()
			if e := p.Element(tk.Index()); e.Token.Green() != tk.Green() {
				t.Fatalf("seed %d: parent/index", seed)
			}
			if i > 0 && tk.Prev().FullSpan().End != off {
				t.Fatalf("seed %d: Prev", seed)
			}
			if n := tk.Next(); !n.IsZero() &&
				n.FullSpan().Start != tk.FullSpan().End {
				t.Fatalf("seed %d: Next", seed)
			}
			off += tk.Green().Width()
			i++
		}
		if i != len(flat) {
			t.Fatalf("seed %d: %d tokens, want %d", seed, i, len(flat))
		}
		for n := range root.Descendants() {
			sp, full := n.Span(), n.FullSpan()
			if sp.Start < full.Start || sp.End > full.End ||
				sp.Start > sp.End {
				t.Fatalf("seed %d: span %v outside %v", seed, sp, full)
			}
			if n.Text() != want[full.Start:full.End] {
				t.Fatalf("seed %d: node text", seed)
			}
			for c := range n.Nodes() {
				if !c.Parent().Equal(n) {
					t.Fatalf("seed %d: parent", seed)
				}
			}
		}
		for o := 0; o < len(want); o++ {
			tk := root.TokenAt(o)
			if fs := tk.FullSpan(); o < fs.Start || o >= fs.End {
				t.Fatalf("seed %d: TokenAt(%d) = %v", seed, o, fs)
			}
		}
	}
}

// TestContentIDs: IDs are a function of content (equal trees, equal
// IDs; Equal agrees with ID equality on these trees), identical small
// subtrees are shared, and IDs are fixed across runs.
func TestContentIDs(t *testing.T) {
	for seed := range uint64(200) {
		a, _ := randomTree(seed)
		b, _ := randomTree(seed)
		if a == b || a.ID() != b.ID() || !Equal(a, b) {
			t.Fatalf("seed %d: equal content, different IDs", seed)
		}
		c, _ := randomTree(seed + 1000)
		if Equal(a, c) != (a.String() == c.String() && a.ID() == c.ID()) {
			t.Fatalf("seed %d: Equal disagrees", seed)
		}
	}
	b := NewBuilder()
	b.StartNode(KindDesignFile)
	for range 2 {
		b.StartNode(KindSimpleName)
		b.Token(token.Identifier, "x", nil, []Trivia{{token.Whitespace,
			" "}}, 0)
		b.FinishNode()
	}
	b.FinishNode()
	g := b.Finish()
	if g.Child(0) != g.Child(1) {
		t.Errorf("identical small nodes are not shared")
	}
	// A fixed tree has a fixed ID in every run and on every platform
	// (FNV-1a and the mixer use constants, never addresses).
	if got := g.ID(); got != pinnedID {
		t.Errorf("ID = %#x, want %#x", uint64(got), uint64(pinnedID))
	}
	if want := tokenHash(token.Identifier, 0, "x", 0, []Trivia{
		{token.Whitespace, " "}}); g.Child(0).Child(0).ID() != ID(want) {
		t.Errorf("token ID differs from tokenHash")
	}
	if g.ID() == g.Child(0).ID() {
		t.Errorf("parent and child share an ID")
	}
}

const pinnedID ID = 0x19b8d12f1f7e4b40

func TestStartNodeAt(t *testing.T) {
	b := NewBuilder()
	b.StartNode(KindDesignFile)
	m := b.Checkpoint()
	b.Token(token.Identifier, "a", nil, nil, 0)
	b.StartNodeAt(m, KindBinaryExpr)
	b.Token(token.Plus, "+", nil, nil, 0)
	b.Token(token.Identifier, "b", nil, nil, 0)
	b.FinishNode()
	b.FinishNode()
	root := NewRoot(b.Finish())
	got := Dump(root)
	want := "DesignFile 0..3\n  BinaryExpr 0..3\n" +
		"    Identifier 0..1 \"a\"\n    Plus 1..2 \"+\"\n" +
		"    Identifier 2..3 \"b\"\n"
	if got != want {
		t.Errorf("dump:\n%s\nwant:\n%s", got, want)
	}
}

func TestFinishWrapsLooseElements(t *testing.T) {
	b := NewBuilder()
	b.Token(token.Identifier, "a", nil, nil, 0)
	b.StartNode(KindSimpleName) // never finished
	b.Token(token.Identifier, "b", nil, nil, 0)
	g := b.Finish()
	if g.String() != "ab" || g.Kind() != KindErrorNode {
		t.Errorf("got %v %q", g.Kind(), g.String())
	}
}

func TestDumpFlagsAndTrivia(t *testing.T) {
	b := NewBuilder()
	b.StartNode(KindDesignFile)
	b.Token(token.Identifier, "x", []Trivia{{token.LineComment, "-- \xff"},
		{token.Newline, "\n"}}, []Trivia{{token.Whitespace, " "}},
		FlagInactive|FlagPSLComment)
	b.Token(token.Semicolon, "", nil, nil, FlagMissing)
	b.FinishNode()
	got := Dump(NewRoot(b.Finish()))
	want := "DesignFile 0..7\n" +
		"    lead LineComment \"-- \\xff\"\n" +
		"    lead Newline \"\\n\"\n" +
		"  Identifier 5..6 \"x\" inactive psl-comment\n" +
		"    trail Whitespace \" \"\n" +
		"  Semicolon 7..7 \"\" missing\n"
	if got != want {
		t.Errorf("dump:\n%s\nwant:\n%s", got, want)
	}
}

func leadOf(parts ...string) []Trivia {
	var out []Trivia
	for _, p := range parts {
		k := token.Whitespace
		switch {
		case p == "\n":
			k = token.Newline
		case strings.HasPrefix(p, "--"):
			k = token.LineComment
		case strings.HasPrefix(p, "/*"):
			k = token.BlockComment
		case strings.HasPrefix(p, "`"):
			k = token.Directive
		}
		out = append(out, Trivia{k, p})
	}
	return out
}

func TestDocComment(t *testing.T) {
	cases := []struct {
		lead []string
		want []string
	}{
		{[]string{"\n", "-- a", "\n", "  "}, []string{"-- a"}},
		{[]string{"-- a", "\n", "-- b", "\n"}, []string{"-- a", "-- b"}},
		{[]string{"-- a", "\n", "\n", "-- b", "\n"}, []string{"-- b"}},
		{[]string{"-- floats", "\n", "\n"}, nil},
		{[]string{"\n"}, nil},
		{[]string{"/* x */", " "}, []string{"/* x */"}},
		{[]string{"-- a", "\n", "`if X = \"1\" then", "\n"}, nil},
		{[]string{"`end if", "\n", "-- a", "\n"}, []string{"-- a"}},
	}
	for _, c := range cases {
		b := NewBuilder()
		b.StartNode(KindDesignFile)
		b.Token(token.KwSignal, "signal", leadOf(c.lead...), nil, 0)
		b.FinishNode()
		root := NewRoot(b.Finish())
		var got []string
		for _, tr := range DocComment(root.FirstToken()) {
			got = append(got, tr.Text)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.lead, got, c.want)
		}
	}
}

// TestKindsTable: names are unique and round-trip through String; every
// group member is a valid kind.
func TestKindsTable(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Kinds() {
		n := k.String()
		if n == "" || seen[n] || !k.Valid() || k.Std() == 0 {
			t.Errorf("kind %d: %q", k, n)
		}
		seen[n] = true
	}
	for _, g := range Groups() {
		ks := GroupKinds(g)
		if len(ks) == 0 {
			t.Errorf("group %s empty", g)
		}
		for _, k := range ks {
			if !k.Valid() {
				t.Errorf("group %s: kind %d", g, k)
			}
		}
	}
	if Kind(0).Valid() || Kind(numKinds).Valid() {
		t.Error("out-of-range kinds are valid")
	}
}

// TestWrappers: typed views accept exactly their kinds and field
// accessors find the right children.
func TestWrappers(t *testing.T) {
	b := NewBuilder()
	b.StartNode(KindEntityDecl)
	b.Token(token.KwEntity, "entity", nil, nil, 0)
	b.Token(token.Identifier, "e", nil, nil, 0)
	b.Token(token.KwIs, "is", nil, nil, 0)
	b.StartNode(KindDeclarativePart)
	b.FinishNode()
	b.StartNode(KindEndClause)
	b.Token(token.KwEnd, "end", nil, nil, 0)
	b.Token(token.Identifier, "e", nil, nil, 0)
	b.Token(token.Semicolon, ";", nil, nil, 0)
	b.FinishNode()
	b.FinishNode()
	root := NewRoot(b.Finish())
	e, ok := As[EntityDecl](root)
	if !ok {
		t.Fatal("As[EntityDecl] failed")
	}
	if _, ok := As[ArchitectureBody](root); ok {
		t.Error("As[ArchitectureBody] accepted an entity")
	}
	if _, ok := As[LibraryUnit](root); !ok {
		t.Error("LibraryUnit group rejects EntityDecl")
	}
	if e.Name().Text() != "e" || e.End().Name().Text() != "e" ||
		e.Decls().IsZero() || !e.Ports().IsZero() {
		t.Errorf("accessors: %q %q", e.Name().Text(),
			e.End().Name().Text())
	}
	n := 0
	for range e.Node.Children[Decl]() {
		n++
	}
	if n != 0 {
		t.Errorf("Children[Decl] = %d", n)
	}
}

// TestTokenIDIsContentHash: Builder.Token's inline hash equals tokenHash
// over the token's kind, flags, text and every leading and trailing
// trivia piece, for random tokens; and tokens that differ only in the
// text of a leading or trailing comment get different IDs (content IDs,
// #31).
func TestTokenIDIsContentHash(t *testing.T) {
	r := &env.SplitMix64{State: 7}
	for range 2000 {
		tk := randTok(r)
		f := Flags(r.Uint64() % 4)
		b := NewBuilder()
		b.StartNode(KindDesignFile)
		b.Token(tk.kind, tk.text, tk.lead, tk.trail, f)
		b.FinishNode()
		g := b.Finish().Child(0)
		triv := append(slices.Clone(tk.lead), tk.trail...)
		want := ID(tokenHash(tk.kind, f, tk.text, len(tk.lead), triv))
		if g.ID() != want {
			t.Fatalf("%+v: ID %#x, want tokenHash %#x", tk, uint64(g.ID()),
				uint64(want))
		}
	}
	id := func(lead, trail []Trivia) ID {
		b := NewBuilder()
		b.StartNode(KindDesignFile)
		b.Token(token.Identifier, "x", lead, trail, 0)
		b.FinishNode()
		return b.Finish().Child(0).ID()
	}
	c1 := []Trivia{{token.LineComment, "-- one"}}
	c2 := []Trivia{{token.LineComment, "-- two"}}
	if id(c1, nil) == id(c2, nil) {
		t.Errorf("leading comment text does not change the ID")
	}
	if id(nil, c1) == id(nil, c2) {
		t.Errorf("trailing comment text does not change the ID")
	}
	if id(c1, nil) == id(nil, c1) {
		t.Errorf("leading and trailing trivia share an ID")
	}
}
