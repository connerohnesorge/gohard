package jsonrpc2

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// gen draws test values from a seeded SplitMix64 (no math/rand, P1).
type gen struct{ r *env.SplitMix64 }

func newGen(seed uint64) *gen { return &gen{r: &env.SplitMix64{State: seed}} }

func (g *gen) intn(n int) int { return env.Intn(g.r, n) }

func (g *gen) bool() bool { return g.intn(2) == 0 }

// edge strings first: empty, quotes, control and non-ASCII runes.
var edgeStrings = []string{"", `"`, `\`, "\x00", "\n", "é", "日本",
	"\U0001F600", "a b", "</script>", " "}

func (g *gen) str() string {
	if g.intn(3) == 0 {
		return edgeStrings[g.intn(len(edgeStrings))]
	}
	var b strings.Builder
	for range g.intn(12) {
		b.WriteByte(byte('a' + g.intn(26)))
	}
	return b.String()
}

func (g *gen) id() ID {
	if g.bool() {
		edges := []int64{0, 1, -1, 1<<63 - 1, -1 << 63}
		if g.bool() {
			return IntID(edges[g.intn(len(edges))])
		}
		return IntID(int64(g.r.Uint64()))
	}
	return StringID(g.str())
}

// value returns a random JSON value of bounded depth, compacted.
func (g *gen) value(depth int) any {
	switch k := g.intn(7); {
	case depth <= 0 || k == 0:
		return g.str()
	case k == 1:
		return g.intn(1000) - 500
	case k == 2:
		return g.bool()
	case k == 3:
		return nil
	case k == 4:
		var a []any
		for range g.intn(4) {
			a = append(a, g.value(depth-1))
		}
		if a == nil {
			a = []any{}
		}
		return a
	default:
		return g.object(depth - 1)
	}
}

func (g *gen) object(depth int) map[string]any {
	m := map[string]any{}
	for i := range g.intn(4) {
		m[g.str()+strconv.Itoa(i)] = g.value(depth)
	}
	return m
}

func (g *gen) raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (g *gen) params() json.RawMessage {
	switch g.intn(3) {
	case 0:
		return nil
	case 1:
		a := []any{g.value(2)}
		return g.raw(a)
	}
	return g.raw(g.object(3))
}

func (g *gen) message() Message {
	switch g.intn(4) {
	case 0:
		return &Request{ID: g.id(), Method: g.str(), Params: g.params()}
	case 1:
		return &Notification{Method: g.str(), Params: g.params()}
	case 2:
		return &Response{ID: g.id(), Result: g.raw(g.value(3))}
	}
	e := &Error{Code: int64(g.intn(70000)) - 35000, Message: g.str()}
	if g.bool() {
		e.Data = g.raw(g.value(2))
	}
	id := g.id()
	if g.intn(4) == 0 {
		id = ID{}
	}
	return &Response{ID: id, Error: e}
}
