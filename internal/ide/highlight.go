package ide

import (
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

func init() { provide(server.FeatureHighlights) }

// LSP DocumentHighlightKind values.
const (
	highlightText  = 1
	highlightRead  = 2
	highlightWrite = 3
)

// Highlights implements server.Analyzer: the in-file highlights of the
// entity at q (Analysis.Highlights).
func (a *Analyzer) Highlights(q server.Query,
) server.Task[[]server.Highlight] {
	return Query(a, q.Snap, func(an *Analysis) []server.Highlight {
		return an.Highlights(q)
	})
}

// Highlights returns the references of the entity at q that lie in
// q's document, declarations included, in position order: assignment
// targets are writes, other uses reads, declarations text.
// They are exactly the in-file part of References.
func (an *Analysis) Highlights(q server.Query) []server.Highlight {
	path, _, ok := an.query(q)
	if !ok {
		return nil
	}
	var out []server.Highlight
	for _, l := range an.References(q, true) {
		if l.URI != q.URI {
			continue
		}
		kind := highlightText
		ref, isRef := an.Result.RefAt(path, l.Span.Start)
		switch {
		case isRef && Span(ref.Span) == l.Span &&
			ref.Kind == sema.RefWrite:
			kind = highlightWrite
		case isRef && Span(ref.Span) == l.Span:
			kind = highlightRead
		}
		out = append(out, server.Highlight{Span: l.Span, Kind: kind})
	}
	return out
}
