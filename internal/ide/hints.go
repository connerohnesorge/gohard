package ide

import (
	"github.com/connerohnesorge/gohard/v2/internal/resolve"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// Signature help and inlay hints (#56), from resolve over the
// snapshot's sema result.

func init() {
	provide(server.FeatureSignatureHelp | server.FeatureInlayHints)
}

// SignatureTriggers are the characters that open signature help: the
// parenthesis of a call or map, and the comma between associations.
var SignatureTriggers = []string{"(", ","}

// SignatureHelpAt is the signature help at q (resolve.SignatureHelp),
// nil when q is in no call or map.
func (an *Analysis) SignatureHelpAt(q server.Query) *server.SignatureHelp {
	path, off, ok := an.query(q)
	if !ok {
		return nil
	}
	sigs, ok := resolve.SignatureHelp(an.Result, path, off)
	if !ok {
		return nil
	}
	out := &server.SignatureHelp{}
	for _, s := range sigs {
		sg := server.Signature{Label: s.Label, Doc: s.Doc,
			Active: s.Active}
		for _, p := range s.Params {
			sg.Params = append(sg.Params, Span(p))
		}
		out.Signatures = append(out.Signatures, sg)
	}
	return out
}

// SignatureHelp implements server.Analyzer.
func (a *Analyzer) SignatureHelp(q server.Query,
) server.Task[*server.SignatureHelp] {
	return Query(a, q.Snap, func(an *Analysis) *server.SignatureHelp {
		return an.SignatureHelpAt(q)
	})
}

// HintKinds returns the inlay hint kinds the snapshot's configuration
// turns on (inlay_hints.*).
func HintKinds(snap *server.Snapshot) resolve.HintKinds {
	h := snap.Config.InlayHints
	return resolve.HintKinds{ParameterNames: h.ParameterNames,
		Types: h.Types, Values: h.Values}
}

// InlayHintsIn returns the inlay hints of the kinds k in the document
// uri whose position lies in r (resolve.Hints): a parameter name is
// followed by a space, a value preceded by one.
func (an *Analysis) InlayHintsIn(uri string, r server.Span,
	k resolve.HintKinds,
) []server.InlayHint {
	path, _, ok := an.query(server.Query{Snap: an.Snap, URI: uri})
	if !ok {
		return nil
	}
	var out []server.InlayHint
	for _, h := range resolve.Hints(an.Result, path, r.Start, r.End, k) {
		ih := server.InlayHint{Offset: h.Off, Label: h.Label}
		switch h.Kind {
		case resolve.HintParameter:
			ih.Kind, ih.PadRight = server.HintParameter, true
		case resolve.HintType:
			ih.Kind = server.HintType
		case resolve.HintValue:
			ih.PadLeft = true // LSP has no value kind
		}
		out = append(out, ih)
	}
	return out
}

// InlayHints implements server.Analyzer.
func (a *Analyzer) InlayHints(snap *server.Snapshot, uri string,
	r server.Span,
) server.Task[[]server.InlayHint] {
	k := HintKinds(snap)
	if k == (resolve.HintKinds{}) {
		return server.Done[[]server.InlayHint](nil)
	}
	return Query(a, snap, func(an *Analysis) []server.InlayHint {
		return an.InlayHintsIn(uri, r, k)
	})
}
