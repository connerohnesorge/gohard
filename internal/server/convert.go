package server

import (
	"encoding/json"
	"slices"

	"github.com/connerohnesorge/gohard/internal/span"
)

// conv turns analyzer results (byte offsets) into LSP values in the
// session's position encoding, looking documents up in the snapshot
// the result was computed for.
type conv struct {
	snap *Snapshot
	enc  span.Encoding
}

func (c conv) rng(uri string, s Span) Range {
	d, ok := c.snap.lookup(uri)
	if !ok {
		return Range{}
	}
	return toRange(d.index, s, c.enc)
}

type lspLocation struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type textEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

func (c conv) hover(uri string, h *Hover) any {
	if h == nil {
		return nil
	}
	v := struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
		Range *Range `json:"range,omitempty"`
	}{}
	v.Contents.Kind, v.Contents.Value = "markdown", h.Markdown
	if h.Span != nil {
		r := c.rng(uri, *h.Span)
		v.Range = &r
	}
	return v
}

func (c conv) locations(ls []Location) any {
	out := make([]lspLocation, 0, len(ls))
	for _, l := range ls {
		r := c.rng(l.URI, l.Span)
		if l.Lines != nil {
			r = toRange(l.Lines, l.Span, c.enc)
		}
		out = append(out, lspLocation{l.URI, r})
	}
	return out
}

func (c conv) edits(uri string, es []Edit) any {
	out := make([]textEdit, 0, len(es))
	for _, e := range es {
		out = append(out, textEdit{c.rng(uri, e.Span), e.NewText})
	}
	return out
}

// failure is a feature result the server answers with RequestFailed.
type failure struct{ msg string }

func (c conv) renamed(r Renamed) any {
	if r.Refused != "" {
		return failure{r.Refused}
	}
	return c.workspaceEdit(r.Edits)
}

// workspaceEdit is the WorkspaceEdit of fs: changes by URI, or, when
// one of them creates its document, documentChanges in order, each
// created document's CreateFile before its edits (#57).
func (c conv) workspaceEdit(fs []FileEdit) any {
	if !slices.ContainsFunc(fs, func(f FileEdit) bool { return f.Create }) {
		changes := map[string]any{}
		for _, f := range fs {
			changes[f.URI] = c.edits(f.URI, f.Edits)
		}
		return struct {
			Changes map[string]any `json:"changes"`
		}{changes}
	}
	type create struct {
		Kind string `json:"kind"`
		URI  string `json:"uri"`
	}
	type doc struct {
		URI     string `json:"uri"`
		Version *int   `json:"version"`
	}
	type docEdit struct {
		TextDocument doc `json:"textDocument"`
		Edits        any `json:"edits"`
	}
	var changes []any
	for _, f := range fs {
		if f.Create {
			changes = append(changes, create{"create", f.URI})
		}
		changes = append(changes, docEdit{doc{URI: f.URI},
			c.edits(f.URI, f.Edits)})
	}
	return struct {
		DocumentChanges []any `json:"documentChanges"`
	}{changes}
}

func (c conv) completion(uri string, items []CompletionItem) any {
	type item struct {
		Label    string    `json:"label"`
		Kind     int       `json:"kind,omitempty"`
		Detail   string    `json:"detail,omitempty"`
		TextEdit *textEdit `json:"textEdit,omitempty"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		v := item{Label: it.Label, Kind: it.Kind, Detail: it.Detail}
		if it.Edit != nil {
			v.TextEdit = &textEdit{c.rng(uri, it.Edit.Span),
				it.Edit.NewText}
		}
		out = append(out, v)
	}
	return struct {
		IsIncomplete bool   `json:"isIncomplete"`
		Items        []item `json:"items"`
	}{false, out}
}

type docSymbol struct {
	Name           string      `json:"name"`
	Detail         string      `json:"detail,omitempty"`
	Kind           int         `json:"kind"`
	Range          Range       `json:"range"`
	SelectionRange Range       `json:"selectionRange"`
	Children       []docSymbol `json:"children,omitempty"`
}

func (c conv) docSymbols(ss []Symbol) any {
	var rec func([]Symbol) []docSymbol
	rec = func(ss []Symbol) []docSymbol {
		out := make([]docSymbol, 0, len(ss))
		for _, s := range ss {
			out = append(out, docSymbol{Name: s.Name, Detail: s.Detail,
				Kind: s.Kind, Range: c.rng(s.URI, s.Span),
				SelectionRange: c.rng(s.URI, s.Selection),
				Children:       rec(s.Children)})
		}
		return out
	}
	return rec(ss)
}

func (c conv) workspaceSymbols(ss []Symbol) any {
	type info struct {
		Name     string      `json:"name"`
		Kind     int         `json:"kind"`
		Location lspLocation `json:"location"`
	}
	out := make([]info, 0, len(ss))
	for _, s := range ss {
		out = append(out, info{s.Name, s.Kind,
			lspLocation{s.URI, c.rng(s.URI, s.Span)}})
	}
	return out
}

func (c conv) folds(uri string, fs []Fold) any {
	type fold struct {
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		Kind      string `json:"kind,omitempty"`
	}
	out := make([]fold, 0, len(fs))
	for _, f := range fs {
		r := c.rng(uri, f.Span)
		out = append(out, fold{r.Start.Line, r.End.Line, f.Kind})
	}
	return out
}

func (c conv) highlights(uri string, hs []Highlight) any {
	type hl struct {
		Range Range `json:"range"`
		Kind  int   `json:"kind,omitempty"`
	}
	out := make([]hl, 0, len(hs))
	for _, h := range hs {
		out = append(out, hl{c.rng(uri, h.Span), h.Kind})
	}
	return out
}

func (c conv) lenses(uri string, ls []Lens) any {
	type command struct {
		Title     string            `json:"title"`
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments,omitempty"`
	}
	type lens struct {
		Range   Range   `json:"range"`
		Command command `json:"command"`
	}
	out := make([]lens, 0, len(ls))
	for _, l := range ls {
		out = append(out, lens{c.rng(uri, l.Span),
			command{l.Title, l.Command, l.Args}})
	}
	return out
}

func (c conv) codeActions(as []CodeAction) any {
	type action struct {
		Title       string `json:"title"`
		Kind        string `json:"kind,omitempty"`
		IsPreferred bool   `json:"isPreferred,omitempty"`
		Edit        any    `json:"edit"`
	}
	out := make([]action, 0, len(as))
	for _, a := range as {
		out = append(out, action{a.Title, a.Kind, a.Preferred,
			c.workspaceEdit(a.Edits)})
	}
	return out
}
