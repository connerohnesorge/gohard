package cst

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// nodes returns every green node of g, depth first.
func nodes(g *Green, out []*Green) []*Green {
	out = append(out, g)
	for _, k := range g.kids {
		out = nodes(k, out)
	}
	return out
}

// TestConsistent: every tree a Builder makes is consistent, and a tree
// with any one field of any one node changed is not (#37: a restored
// tree that does not check out is never used).
func TestConsistent(t *testing.T) {
	for seed := range uint64(300) {
		g, _ := randomTree(seed)
		if !g.Consistent() {
			t.Fatalf("seed %d: a built tree is inconsistent", seed)
		}
		all := nodes(g, nil)
		r := &env.SplitMix64{State: seed}
		victim := all[int(r.Uint64()%uint64(len(all)))]
		raw := victim.Raw()
		muts := map[string]func(*GreenRaw){
			"hash":  func(x *GreenRaw) { x.Hash ^= 1 },
			"width": func(x *GreenRaw) { x.Width++ },
			"kind":  func(x *GreenRaw) { x.Kind ^= 1 },
			"flags": func(x *GreenRaw) { x.Flags ^= FlagMissing },
			"nlead": func(x *GreenRaw) { x.NLead++ },
			"text":  func(x *GreenRaw) { x.Text += "x" },
		}
		if raw.Tok && raw.Text != "" {
			muts["same-width-text"] = func(x *GreenRaw) {
				b := []byte(x.Text)
				b[0] ^= 0x20
				x.Text = string(b)
			}
		}
		for _, name := range []string{"hash", "width", "kind", "flags",
			"nlead", "text", "same-width-text"} {
			m, ok := muts[name]
			if !ok {
				continue
			}
			bad := raw
			m(&bad)
			repl := GreenFromRaw(bad)
			tree := replace(g, victim, repl)
			if tree.Consistent() {
				t.Errorf("seed %d: %s of a %s changed, still consistent",
					seed, name, map[bool]string{true: "token",
						false: "node"}[raw.Tok])
			}
		}
	}
}

// replace returns g with every occurrence of old replaced by repl,
// the nodes above it copied with their stored fields unchanged.
func replace(g, old, repl *Green) *Green {
	if g == old {
		return repl
	}
	if g.tok {
		return g
	}
	r := g.Raw()
	r.Kids = make([]*Green, len(g.kids))
	for i, k := range g.kids {
		r.Kids[i] = replace(k, old, repl)
	}
	return GreenFromRaw(r)
}
