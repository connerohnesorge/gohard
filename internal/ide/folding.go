package ide

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/token"
)

func init() { provide(server.FeatureFolding) }

// Folding implements server.Analyzer (Analysis.Folding).
func (a *Analyzer) Folding(snap *server.Snapshot, uri string,
) server.Task[[]server.Fold] {
	return Query(a, snap, func(an *Analysis) []server.Fold {
		return an.Folding(uri)
	})
}

// folded are the node kinds that fold: design units, declarations
// and statements with a body, interface lists, maps and multi-line
// aggregates.
var folded = map[cst.Kind]bool{
	cst.KindEntityDecl: true, cst.KindArchitectureBody: true,
	cst.KindPackageDecl: true, cst.KindPackageBody: true,
	cst.KindConfigurationDecl: true, cst.KindContextDecl: true,
	cst.KindPSLVunit: true, cst.KindPSLVmode: true, cst.KindPSLVprop: true,
	cst.KindComponentDecl: true, cst.KindSubprogramBody: true,
	cst.KindRecordTypeDef: true, cst.KindProtectedTypeDef: true,
	cst.KindProtectedTypeBody: true, cst.KindGenericClause: true,
	cst.KindPortClause: true, cst.KindGenericMapAspect: true,
	cst.KindPortMapAspect: true, cst.KindIfStmt: true,
	cst.KindElsifBranch: true, cst.KindElseBranch: true,
	cst.KindCaseStmt: true, cst.KindCaseAlternative: true,
	cst.KindLoopStmt: true, cst.KindBlockStmt: true,
	cst.KindProcessStmt: true, cst.KindForGenerateStmt: true,
	cst.KindIfGenerateStmt: true, cst.KindGenerateBranch: true,
	cst.KindCaseGenerateStmt: true, cst.KindCaseGenerateAlternative: true,
	cst.KindComponentInstantiationStmt: true,
	cst.KindSequentialBlockStmt:        true, cst.KindAggregate: true,
	cst.KindBlockConfiguration: true, cst.KindComponentConfiguration: true,
}

// Folding returns the folding ranges of the document uri, sorted by
// start then end line: every multi-line construct of folded from its
// first line to the line before its closing `end` (to its last line
// when it has none), each context clause of two or more lines
// ("imports"), and every run of two or more whole-line comments and
// every multi-line block comment ("comment"). No two ranges are equal
// and every range spans at least two lines.
func (an *Analysis) Folding(uri string) []server.Fold {
	path, ok := an.Path(uri)
	if !ok || an.Result == nil {
		return nil
	}
	f, ok := an.Result.File(path)
	if !ok || f.Parse == nil {
		return nil
	}
	doc, _ := an.Snap.Document(uri)
	x := doc.Index()
	var out []server.Fold
	add := func(start, endLine int, kind string) {
		if endLine > x.Line(start) {
			out = append(out, server.Fold{Kind: kind,
				Span: server.Span{Start: start, End: x.LineEnd(endLine)}})
		}
	}
	root := f.Root()
	for n := range root.Descendants() {
		first, last := n.FirstToken(), n.LastToken()
		if first.IsZero() || last.IsZero() {
			continue
		}
		start := first.Span().Start
		switch {
		case n.Kind() == cst.KindContextClause:
			add(start, x.Line(last.Span().End), "imports")
		case folded[n.Kind()]:
			end := x.Line(last.Span().End)
			for t := range n.Tokens() {
				if t.Kind() == token.KwEnd && !t.Missing() {
					end = x.Line(t.Span().Start) - 1
				}
			}
			for c := range n.Nodes() {
				if c.Kind() == cst.KindEndClause {
					end = x.Line(c.FirstToken().Span().Start) - 1
				}
			}
			add(start, end, "")
		}
	}
	out = append(out, comments(root, doc.Text(), x)...)
	slices.SortFunc(out, func(a, b server.Fold) int {
		return cmp.Or(cmp.Compare(x.Line(a.Span.Start), x.Line(b.Span.Start)),
			cmp.Compare(x.Line(a.Span.End), x.Line(b.Span.End)),
			cmp.Compare(a.Kind, b.Kind))
	})
	return slices.CompactFunc(out, func(a, b server.Fold) bool {
		return x.Line(a.Span.Start) == x.Line(b.Span.Start) &&
			x.Line(a.Span.End) == x.Line(b.Span.End)
	})
}

// comments returns the comment folds of the tree rooted at root.
func comments(root cst.Node, text []byte, x *span.Index) []server.Fold {
	var out []server.Fold
	runStart, runEnd := -1, -1 // offset of the run's first comment, line
	flush := func() {
		if runStart >= 0 && runEnd > x.Line(runStart) {
			out = append(out, server.Fold{Kind: "comment",
				Span: server.Span{Start: runStart, End: x.LineEnd(runEnd)}})
		}
		runStart, runEnd = -1, -1
	}
	visit := func(off int, tv cst.Trivia) {
		switch tv.Kind {
		case token.BlockComment:
			flush()
			end := off + len(tv.Text)
			if x.Line(end) > x.Line(off) {
				out = append(out, server.Fold{Kind: "comment",
					Span: server.Span{Start: off, End: end}})
			}
		case token.LineComment:
			l := x.Line(off)
			if !wholeLine(text, x.LineStart(l), off) {
				flush()
				return
			}
			if runStart >= 0 && l == runEnd+1 {
				runEnd = l
				return
			}
			flush()
			runStart, runEnd = off, l
		}
	}
	for t := range root.DescendantTokens() {
		off := t.FullSpan().Start
		for _, tv := range t.Leading() {
			visit(off, tv)
			off += len(tv.Text)
		}
		off = t.Span().End
		for _, tv := range t.Trailing() {
			visit(off, tv)
			off += len(tv.Text)
		}
		if t.Kind() != token.EOF && t.Span().End > t.Span().Start {
			l := x.Line(t.Span().Start)
			if runStart >= 0 && l > runEnd {
				flush() // code after the run ends it
			}
		}
	}
	flush()
	return out
}

func wholeLine(text []byte, from, to int) bool {
	for _, c := range text[from:to] {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}
