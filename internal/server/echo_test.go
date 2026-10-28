package server_test

import (
	"fmt"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// echo is the analyzer of the fixtures: it makes the server's view of
// the text visible. Diagnostics put one hint at 1:1 of every open
// document holding its version and text; hover names the word at the
// position and its byte offset; definition finds `entity <word>` in
// any document of the snapshot (a cross-file query).
type echo struct{ server.Nop }

func (echo) Features() server.Features {
	return server.Features{Set: server.FeatureDiagnostics |
		server.FeatureHover | server.FeatureDefinition}
}

func (echo) Diagnostics(snap *server.Snapshot,
) server.Task[[]server.FileDiagnostics] {
	var out []server.FileDiagnostics
	for _, d := range snap.Documents() {
		if !d.Open {
			continue
		}
		out = append(out, server.FileDiagnostics{URI: d.URI,
			Diagnostics: []diag.Diagnostic{{
				Path: d.Rel, Line: 1, Col: 1, Severity: diag.Hint,
				Code: "lsp/echo",
				Message: fmt.Sprintf("v%d %q", d.Version,
					d.Text()),
			}}})
	}
	return server.Done(out)
}

// word returns the identifier around off.
func word(text []byte, off int) server.Span {
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' ||
			c|0x20 >= 'a' && c|0x20 <= 'z' || c >= 0x80
	}
	a, b := off, off
	for a > 0 && isWord(text[a-1]) {
		a--
	}
	for b < len(text) && isWord(text[b]) {
		b++
	}
	return server.Span{Start: a, End: b}
}

func (echo) Hover(q server.Query) server.Task[*server.Hover] {
	d, ok := q.Snap.Document(q.URI)
	if !ok {
		return server.Done[*server.Hover](nil)
	}
	w := word(d.Text(), q.Offset)
	return server.Done(&server.Hover{
		Markdown: fmt.Sprintf("%q at byte %d", d.Text()[w.Start:w.End],
			q.Offset),
		Span: &w})
}

func (echo) Definition(q server.Query) server.Task[[]server.Location] {
	d, ok := q.Snap.Document(q.URI)
	if !ok {
		return server.Done[[]server.Location](nil)
	}
	w := word(d.Text(), q.Offset)
	name := strings.ToLower(string(d.Text()[w.Start:w.End]))
	for _, doc := range q.Snap.Documents() {
		low := strings.ToLower(string(doc.Text()))
		if i := strings.Index(low, "entity "+name); i >= 0 && name != "" {
			at := i + len("entity ")
			return server.Done([]server.Location{{URI: doc.URI,
				Span: server.Span{Start: at, End: at + len(name)}}})
		}
	}
	return server.Done[[]server.Location](nil)
}

// words is the analyzer of the semantic tokens fixtures (#55): every
// word is a variable token, readonly when it has no lower-case letter,
// and every -- and /* */ comment a comment token (a block comment may
// span lines).
type words struct{ server.Nop }

func (words) Features() server.Features {
	return server.Features{Set: server.FeatureSemanticTokens,
		TokenTypes:     []string{"variable", "comment"},
		TokenModifiers: []string{"readonly"}}
}

func (words) SemanticTokens(snap *server.Snapshot, uri string,
) server.Task[[]server.SemToken] {
	d, ok := snap.Document(uri)
	if !ok {
		return server.Done[[]server.SemToken](nil)
	}
	text := d.Text()
	var out []server.SemToken
	for i := 0; i < len(text); {
		rest := string(text[i:])
		var end int
		switch {
		case strings.HasPrefix(rest, "--"):
			end = i + len(rest)
			if j := strings.IndexByte(rest, '\n'); j >= 0 {
				end = i + j
			}
			out = append(out, server.SemToken{
				Span: server.Span{Start: i, End: end}, Type: 1})
		case strings.HasPrefix(rest, "/*"):
			end = i + len(rest)
			if j := strings.Index(rest[2:], "*/"); j >= 0 {
				end = i + 2 + j + 2
			}
			out = append(out, server.SemToken{
				Span: server.Span{Start: i, End: end}, Type: 1})
		default:
			w := word(text, i)
			if w.Start != i || w.End == i {
				i++
				continue
			}
			end = w.End
			s := string(text[w.Start:w.End])
			var mods uint32
			if strings.ToUpper(s) == s && strings.ToLower(s) != s {
				mods = 1
			}
			out = append(out, server.SemToken{Span: w, Mods: mods})
		}
		i = end
	}
	return server.Done(out)
}
