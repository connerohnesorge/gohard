package server

import (
	"errors"
	"math"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/span"
)

// Signature help and inlay hints (#56): the LSP form of the analyzer's
// results and the `gohard lsp signature-help` and `gohard lsp
// inlay-hints` actions.

// signatureHelp converts a signature help: parameter label offsets in
// the session's position encoding, null when there is no signature.
func (c conv) signatureHelp(h *SignatureHelp) any {
	if h == nil || len(h.Signatures) == 0 {
		return nil
	}
	type markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	type param struct {
		Label [2]int `json:"label"`
	}
	type info struct {
		Label           string  `json:"label"`
		Documentation   *markup `json:"documentation,omitempty"`
		Parameters      []param `json:"parameters"`
		ActiveParameter *int    `json:"activeParameter,omitempty"`
	}
	out := struct {
		Signatures      []info `json:"signatures"`
		ActiveSignature int    `json:"activeSignature"`
		ActiveParameter *int   `json:"activeParameter,omitempty"`
	}{Signatures: make([]info, 0, len(h.Signatures))}
	for _, sg := range h.Signatures {
		x := span.NewIndex([]byte(sg.Label))
		v := info{Label: sg.Label, Parameters: make([]param, 0,
			len(sg.Params))}
		if sg.Doc != "" {
			v.Documentation = &markup{"markdown", sg.Doc}
		}
		for _, p := range sg.Params {
			v.Parameters = append(v.Parameters, param{[2]int{
				x.Position(p.Start, c.enc).Col,
				x.Position(p.End, c.enc).Col}})
		}
		if sg.Active >= 0 && sg.Active < len(sg.Params) {
			a := sg.Active
			v.ActiveParameter = &a
		}
		out.Signatures = append(out.Signatures, v)
	}
	out.ActiveParameter = out.Signatures[0].ActiveParameter
	return out
}

func (c conv) inlayHints(uri string, hs []InlayHint) any {
	type hint struct {
		Position     Position `json:"position"`
		Label        string   `json:"label"`
		Kind         int      `json:"kind,omitempty"`
		PaddingLeft  bool     `json:"paddingLeft,omitempty"`
		PaddingRight bool     `json:"paddingRight,omitempty"`
	}
	out := make([]hint, 0, len(hs))
	for _, h := range hs {
		r := c.rng(uri, Span{h.Offset, h.Offset})
		out = append(out, hint{r.Start, h.Label, int(h.Kind),
			h.PadLeft, h.PadRight})
	}
	return out
}

func init() {
	RegisterAction(positional("signature-help",
		"textDocument/signatureHelp",
		"the signatures of the call or map at the position"))
	RegisterAction(Action{
		Name: "inlay-hints", Method: "textDocument/inlayHint",
		Summary: "inlay hints from --at to END (default: the end of " +
			"the file), of the kinds inlay_hints.* turns on",
		Args: "[END_LINE:END_COL]",
		Params: func(uri string, pos Position, args []string) (any,
			error,
		) {
			end := Position{Line: math.MaxInt32}
			switch len(args) {
			case 0:
			case 1:
				var ok bool
				if end, ok = parseLineCol(args[0]); !ok {
					return nil, errors.New("gohard lsp inlay-hints: " +
						"the range end must be LINE:COL with a line " +
						"and column of at least 1, not " +
						strconv.Quote(args[0]))
				}
			default:
				return nil, errors.New("gohard lsp inlay-hints takes " +
					"at most one argument, the range end LINE:COL")
			}
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Range Range `json:"range"`
			}
			p.TextDocument.URI = uri
			p.Range = Range{Start: pos, End: end}
			return p, nil
		},
	})
}
