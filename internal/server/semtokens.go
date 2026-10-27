package server

import (
	"errors"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/span"
)

// The semantic tokens requests (#55).
const (
	methodTokensFull  = "textDocument/semanticTokens/full"
	methodTokensDelta = "textDocument/semanticTokens/full/delta"
	methodTokensRange = "textDocument/semanticTokens/range"
)

// EncodeTokens encodes the semantic tokens ts of the document with
// line index x as LSP token data in the encoding enc: five integers
// per token (line delta, start delta, length, type, modifiers). A
// token that spans lines is split into one token per line, without
// the line terminators; empty pieces are left out. With r set, only
// the pieces that overlap r are kept, whole, so a range result is the
// slice of the full result that overlaps the range. ts must be sorted
// and not overlap; a piece that starts before the end of the previous
// one is dropped, so the data is always valid.
func EncodeTokens(x *span.Index, ts []SemToken, r *Span,
	enc span.Encoding,
) []uint32 {
	data := make([]uint32, 0, 5*len(ts))
	line, col, end := 0, 0, 0
	for _, t := range ts {
		a, b := max(t.Span.Start, 0), min(t.Span.End, x.Len())
		for l := x.Line(a); a < b; l++ {
			pa, pb := max(a, x.LineStart(l)), min(b, x.LineEnd(l))
			if l+1 < x.LineCount() {
				a = x.LineStart(l + 1)
			} else {
				a = b
			}
			if pb <= pa || pa < end ||
				r != nil && (pa >= r.End || pb <= r.Start) {
				continue
			}
			end = pb
			s, e := x.Position(pa, enc), x.Position(pb, enc)
			dl, dc := s.Line-line, s.Col
			if dl == 0 {
				dc = s.Col - col
			}
			data = append(data, uint32(dl), uint32(dc),
				uint32(e.Col-s.Col), uint32(t.Type), t.Mods)
			line, col = s.Line, s.Col
		}
	}
	return data
}

// tokenResult is the result of a semantic tokens request before the
// server answers it: the State assigns the result ID and keeps the data
// as the base of the next delta request.
type tokenResult struct {
	uri  string
	data []uint32
	// ranged is a range request; delta a delta request against prev.
	ranged, delta bool
	prev          string
}

// storedTokens is the last full or delta result of a document.
type storedTokens struct {
	id   string
	data []uint32
}

type semTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

type semEdit struct {
	Start       int      `json:"start"`
	DeleteCount int      `json:"deleteCount"`
	Data        []uint32 `json:"data"`
}

type semDelta struct {
	ResultID string    `json:"resultId"`
	Edits    []semEdit `json:"edits"`
}

func (c conv) tokens(uri string, ts []SemToken, r *Span,
	method, prev string,
) any {
	res := tokenResult{uri: uri, data: []uint32{},
		ranged: method == methodTokensRange,
		delta:  method == methodTokensDelta, prev: prev}
	if d, ok := c.snap.lookup(uri); ok {
		res.data = EncodeTokens(d.index, ts, r, c.enc)
	}
	return res
}

// answerTokens answers a semantic tokens request. A full or delta
// result gets the next result ID and becomes the document's delta
// base; a delta request whose previousResultId is that base gets the
// edits from it, any other gets the full result (LSP allows both).
func (s *State) answerTokens(o *out, id jsonrpc2.ID, r tokenResult) {
	if r.ranged {
		s.respond(o, id, semTokens{Data: r.data})
		return
	}
	key := docKey(r.uri)
	old, had := s.tokens[key]
	s.nextTokens++
	rid := strconv.FormatUint(s.nextTokens, 10)
	s.tokens = cloneSet(s.tokens, key, storedTokens{rid, r.data})
	if r.delta && had && old.id == r.prev {
		s.respond(o, id, semDelta{ResultID: rid,
			Edits: diffTokens(old.data, r.data)})
		return
	}
	s.respond(o, id, semTokens{ResultID: rid, Data: r.data})
}

// diffTokens returns the edits that turn the token data old into new:
// none when they are equal, else one edit replacing what lies between
// their common prefix and common suffix.
func diffTokens(old, new []uint32) []semEdit {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	if p == len(old) && p == len(new) {
		return []semEdit{}
	}
	q := 0
	for q < len(old)-p && q < len(new)-p &&
		old[len(old)-1-q] == new[len(new)-1-q] {
		q++
	}
	return []semEdit{{Start: p, DeleteCount: len(old) - p - q,
		Data: append([]uint32{}, new[p:len(new)-q]...)}}
}

// tokensCapability is the semanticTokensProvider capability.
func tokensCapability(f Features) map[string]any {
	nonNil := func(xs []string) []string {
		if xs == nil {
			return []string{}
		}
		return xs
	}
	return map[string]any{
		"legend": map[string]any{
			"tokenTypes":     nonNil(f.TokenTypes),
			"tokenModifiers": nonNil(f.TokenModifiers),
		},
		"range": true,
		"full":  map[string]any{"delta": true},
	}
}

func init() {
	usage := errors.New("gohard lsp semantic-tokens takes no " +
		"argument, full, or range START_LINE:START_COL-END_LINE:END_COL")
	RegisterAction(Action{
		Name: "semantic-tokens", Method: methodTokensFull,
		Summary: "semantic tokens of the file named by --at, or of " +
			"a range of it",
		Args: "[full|range LINE:COL-LINE:COL]",
		MethodOf: func(args []string) string {
			if len(args) > 0 && args[0] == "range" {
				return methodTokensRange
			}
			return methodTokensFull
		},
		Params: func(uri string, _ Position, args []string) (any,
			error,
		) {
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Range *Range `json:"range,omitempty"`
			}
			p.TextDocument.URI = uri
			switch {
			case len(args) == 0 || len(args) == 1 && args[0] == "full":
				return p, nil
			case len(args) != 2 || args[0] != "range":
				return nil, usage
			}
			a, b, ok := strings.Cut(args[1], "-")
			start, ok1 := parseLineCol(a)
			end, ok2 := parseLineCol(b)
			if !ok || !ok1 || !ok2 {
				return nil, errors.New("gohard lsp semantic-tokens: " +
					"the range must be LINE:COL-LINE:COL with lines " +
					"and columns of at least 1, not " +
					strconv.Quote(args[1]))
			}
			p.Range = &Range{Start: start, End: end}
			return p, nil
		},
	})
}
