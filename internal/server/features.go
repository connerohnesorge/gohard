package server

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

type docParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position      *Position       `json:"position"`
	Range         *Range          `json:"range"`
	WorkDoneToken json.RawMessage `json:"workDoneToken"`
	// textDocument/references
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
	// textDocument/rename
	NewName string `json:"newName"`
	// workspace/symbol
	Query string `json:"query"`
	// workspace/executeCommand
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments"`
	// textDocument/semanticTokens/full/delta
	PreviousResultID string `json:"previousResultId"`
}

// methods maps every feature request to the feature that serves it.
var methods = map[string]Feature{
	"textDocument/hover":             FeatureHover,
	"textDocument/definition":        FeatureDefinition,
	"textDocument/references":        FeatureReferences,
	"textDocument/prepareRename":     FeatureRename,
	"textDocument/rename":            FeatureRename,
	"textDocument/completion":        FeatureCompletion,
	"textDocument/documentSymbol":    FeatureDocumentSymbols,
	"workspace/symbol":               FeatureWorkspaceSymbols,
	"textDocument/formatting":        FeatureFormatting,
	"textDocument/rangeFormatting":   FeatureRangeFormatting,
	"textDocument/foldingRange":      FeatureFolding,
	"textDocument/documentHighlight": FeatureHighlights,
	"textDocument/codeLens":          FeatureCodeLens,
	"workspace/executeCommand":       FeatureCommands,
	"textDocument/codeAction":        FeatureCodeActions,
	methodTokensFull:                 FeatureSemanticTokens,
	methodTokensDelta:                FeatureSemanticTokens,
	methodTokensRange:                FeatureSemanticTokens,
}

// Methods returns the feature request methods, sorted.
func Methods() []string {
	out := make([]string, 0, len(methods))
	for m := range methods {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

// feature starts the task for a feature request on the latest
// snapshot. Until the project files are loaded a request waits
// (parked, with no slice queued): cross-file answers need every file.
// A parked request can be cancelled, and it gets ContentModified if a
// document changes before it starts.
func (s *State) feature(o *out, m call) {
	feat, ok := methods[m.method]
	if !ok || !s.features().Has(feat) {
		s.fail(o, m.id, jsonrpc2.CodeMethodNotFound,
			"method not found: "+m.method)
		return
	}
	if !s.loaded || s.rereading {
		s.nextWork++
		w := work{id: s.nextWork, isReq: true, reqID: m.id,
			method: m.method, parked: &m, edits: s.edits, since: s.now}
		s.setWork(w)
		s.byRequest = cloneSet(s.byRequest, m.id.String(), w.id)
		return
	}
	s.begin(o, m, 0)
}

// begin validates a request and queues its first slice; id reuses the
// work ID of a parked request.
func (s *State) begin(o *out, m call, id uint64) {
	var p docParams
	if len(m.params) > 0 && json.Unmarshal(m.params, &p) != nil {
		s.dropWork(id)
		s.fail(o, m.id, jsonrpc2.CodeInvalidParams, "invalid params")
		return
	}
	snap := s.snap
	uri := p.TextDocument.URI
	doc, hasDoc := snap.Document(uri)
	needsDoc := strings.HasPrefix(m.method, "textDocument/")
	if needsDoc && !hasDoc {
		s.dropWork(id)
		s.fail(o, m.id, jsonrpc2.CodeInvalidParams,
			"unknown document "+uri)
		return
	}
	if needsDoc && p.Position == nil && takesPosition(m.method) {
		s.dropWork(id)
		s.fail(o, m.id, jsonrpc2.CodeInvalidParams, "missing position")
		return
	}
	if m.method == methodTokensRange && p.Range == nil {
		s.dropWork(id)
		s.fail(o, m.id, jsonrpc2.CodeInvalidParams, "missing range")
		return
	}
	q := Query{Snap: snap, URI: uri}
	if hasDoc && p.Position != nil {
		q.Offset = offset(doc.index, *p.Position, s.enc)
	}
	c := conv{snap: snap, enc: s.enc}
	run := s.runner(m.method, q, p, c)
	var job *simJob
	if m.method == "workspace/executeCommand" {
		if r, j, ok := s.simCommand(p.Command, p.Arguments); ok {
			run, job = r, j
		}
	}
	run = s.decorate(m.method, q, run)
	s.startWork(work{id: id, isReq: true, reqID: m.id, method: m.method,
		run: run, token: p.WorkDoneToken, sim: job})
	if job != nil && job.tb != "" {
		s.refreshLenses(o)
	}
}

// unpark starts every parked request, in arrival order, once the
// project files are loaded.
func (s *State) unpark(o *out) {
	for _, id := range sortedKeys(s.work) {
		w := s.work[id]
		if w.parked == nil {
			continue
		}
		if w.edits != s.edits {
			s.finish(o, w)
			s.fail(o, w.reqID, jsonrpc2.CodeContentModified,
				"the content changed before "+w.method+" started")
			continue
		}
		s.begin(o, *w.parked, w.id)
	}
}

func takesPosition(method string) bool {
	switch method {
	case "textDocument/documentSymbol", "textDocument/formatting",
		"textDocument/rangeFormatting", "textDocument/foldingRange",
		"textDocument/codeLens", "textDocument/codeAction",
		methodTokensFull, methodTokensDelta, methodTokensRange:
		return false
	}
	return true
}

func (s *State) runner(method string, q Query, p docParams,
	c conv,
) runner {
	an, snap, uri := s.an, q.Snap, q.URI
	switch method {
	case "textDocument/hover":
		return runTask(an.Hover(q), func(h *Hover) any {
			return c.hover(uri, h)
		})
	case "textDocument/definition":
		return runTask(an.Definition(q), c.locations)
	case "textDocument/references":
		return runTask(an.References(q, p.Context.IncludeDeclaration),
			c.locations)
	case "textDocument/prepareRename":
		return runTask(an.PrepareRename(q), func(sp *Span) any {
			if sp == nil {
				return nil
			}
			return c.rng(uri, *sp)
		})
	case "textDocument/rename":
		return runTask(an.Rename(q, p.NewName), c.renamed)
	case "textDocument/completion":
		return runTask(an.Completion(q), func(items []CompletionItem,
		) any {
			return c.completion(uri, items)
		})
	case "textDocument/documentSymbol":
		return runTask(an.DocumentSymbols(snap, uri), c.docSymbols)
	case "workspace/symbol":
		return runTask(an.WorkspaceSymbols(snap, p.Query),
			c.workspaceSymbols)
	case "textDocument/formatting", "textDocument/rangeFormatting":
		var r *Span
		if d, ok := snap.Document(uri); ok && p.Range != nil &&
			method == "textDocument/rangeFormatting" {
			sp := toSpan(d.index, *p.Range, s.enc)
			r = &sp
		}
		return runTask(an.Format(snap, uri, r), func(es []Edit) any {
			return c.edits(uri, es)
		})
	case "textDocument/foldingRange":
		return runTask(an.Folding(snap, uri), func(fs []Fold) any {
			return c.folds(uri, fs)
		})
	case "textDocument/documentHighlight":
		return runTask(an.Highlights(q), func(hs []Highlight) any {
			return c.highlights(uri, hs)
		})
	case "textDocument/codeAction":
		var r Span
		if d, ok := snap.Document(uri); ok && p.Range != nil {
			r = toSpan(d.index, *p.Range, s.enc)
		}
		q.Offset = r.Start
		return runTask(an.CodeActions(q, r), c.codeActions)
	case "textDocument/codeLens":
		return runTask(an.CodeLens(snap, uri), func(ls []Lens) any {
			return c.lenses(uri, ls)
		})
	case methodTokensFull, methodTokensDelta, methodTokensRange:
		var r *Span
		if d, ok := snap.Document(uri); ok && p.Range != nil &&
			method == methodTokensRange {
			sp := toSpan(d.index, *p.Range, s.enc)
			r = &sp
		}
		return runTask(an.SemanticTokens(snap, uri), func(ts []SemToken,
		) any {
			return c.tokens(uri, ts, r, method, p.PreviousResultID)
		})
	default: // workspace/executeCommand
		return runTask(an.Command(snap, p.Command, p.Arguments),
			func(r json.RawMessage) any { return r })
	}
}
