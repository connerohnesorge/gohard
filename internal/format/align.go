package format

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Alignment (#32, format.align_ports).
//
// A line is alignable when it starts with an item that has an
// alignment token on the same line:
//
//   - an interface object declaration of a generic or port clause and
//     its `:`;
//   - a named association of a map (generic, port and parameter maps)
//     and its `=>`;
//   - a named element association of an aggregate and its `=>`;
//   - a simple or conditional signal, variable, force or release
//     assignment statement and its `<=` or `:=`.
//
// A run is a maximal sequence of adjacent alignable lines with the same
// key (the alignment token's kind and the list or statement part
// holding the items, so a run never leaves its block) and no blank line
// between them; comment-only lines and any other line end a run. In a
// run of two or more lines the gaps before the alignment tokens are
// widened so the tokens start in the same column.
//
// The padding depends only on the text before the alignment tokens,
// which alignment does not change, so alignment is idempotent. Alignment runs after wrapping and does not re-wrap:
// padding can push a line past format.max_line_length.
//
// With format.align_ports off, only `=>` in generic and port maps is
// aligned, and only in runs (maps written one association per line):
// that is the form the wrapper gives a long map, and the formatter
// cannot tell a wrapped map from one wrapped by hand, so both must
// format alike for formatting to be idempotent.

func init() { alignPass = alignLines }

// anchor is the alignment token of an alignable line.
type anchor struct {
	tok   token.Kind // Colon, Arrow, Le or VarAssign
	block cst.Node   // the node holding the line's item
	atom  int        // atom index of the alignment token
	col   int        // column where the alignment token's gap starts
}

func (a anchor) sameRun(b anchor) bool {
	return a.tok == b.tok && a.block.Equal(b.block)
}

// alignLines aligns every run of the doc.
func alignLines(d *doc) {
	var run []anchor
	flush := func() {
		if len(run) > 1 {
			to := 0
			for _, a := range run {
				to = max(to, a.col)
			}
			// Every token goes one space past the widest prefix. The
			// gap is set, not widened, so a gap the base layout left
			// empty (after a block comment) aligns the same way when
			// the output is formatted again.
			for _, a := range run {
				d.gap[a.atom] = strings.Repeat(" ", to-a.col+1)
			}
		}
		run = run[:0]
	}
	for li := range d.lines {
		a, ok := d.anchorOf(li)
		if !ok || (len(run) > 0 &&
			(d.lines[li].blank > 0 || !run[0].sameRun(a))) {
			flush()
		}
		if ok {
			run = append(run, a)
		}
	}
	flush()
}

// maxAlignDepth bounds the walk from a line's first token up to the
// item it starts: real items are a few nodes up (a selected-name target
// is about seven), and the bound keeps a line that starts a deeply
// nested expression (a 16k-operand concatenation) from costing a walk
// over the whole nest.
const maxAlignDepth = 16

// anchorOf returns the anchor of line li, if it is alignable.
func (d *doc) anchorOf(li int) (anchor, bool) {
	ln := d.lines[li]
	first := &d.atoms[ln.first]
	if !first.isToken() || first.psl {
		return anchor{}, false
	}
	t := first.tok
	n := t.Parent()
	for range maxAlignDepth {
		// n starts at t (an O(1) test; firstTok descends the tree).
		if n.IsZero() || n.Offset() != t.FullSpan().Start {
			break
		}
		tk, block, ok := d.alignItem(n)
		if !ok {
			n = n.Parent()
			continue
		}
		at := n.Token(tk, 0)
		if at.IsZero() || at.Text() == "" {
			return anchor{}, false
		}
		k, ok := d.atomOn(li, at.Span().Start)
		if !ok {
			return anchor{}, false
		}
		col, ok := d.column(li, k)
		if !ok {
			return anchor{}, false
		}
		return anchor{tok: tk, block: block, atom: k, col: col}, true
	}
	return anchor{}, false
}

// alignItem reports whether n is an item whose lines align, with its
// alignment token kind and the block that holds it. A node that is an
// item of another kind stops nothing: the caller walks on to the
// ancestors that start at the same token.
func (d *doc) alignItem(n cst.Node) (token.Kind, cst.Node, bool) {
	p := n.Parent()
	pp := p.Parent()
	all := d.opts.AlignPorts
	switch n.Kind() {
	case cst.KindInterfaceObjectDecl:
		if all && p.Kind() == cst.KindInterfaceList &&
			(pp.Kind() == cst.KindGenericClause ||
				pp.Kind() == cst.KindPortClause) {
			return token.Colon, p, true
		}
	case cst.KindAssociationElement:
		if p.Kind() != cst.KindAssociationList {
			break
		}
		switch pp.Kind() {
		case cst.KindGenericMapAspect, cst.KindPortMapAspect:
			return token.Arrow, p, true
		case cst.KindParameterMapAspect, cst.KindCallGenericMap:
			return token.Arrow, p, all
		}
	case cst.KindElementAssociation:
		if all && p.Kind() == cst.KindAggregate {
			return token.Arrow, p, true
		}
	case cst.KindConcurrentSimpleSignalAssign,
		cst.KindConcurrentConditionalSignalAssign,
		cst.KindSignalAssignStmt, cst.KindConditionalSignalAssignStmt,
		cst.KindForceAssignStmt, cst.KindReleaseAssignStmt:
		return token.Le, p, all
	case cst.KindVariableAssignStmt,
		cst.KindConditionalVariableAssignStmt:
		return token.VarAssign, p, all
	}
	return 0, cst.Node{}, false
}

// atomOn returns the index of the atom of line li that starts at off.
func (d *doc) atomOn(li, off int) (int, bool) {
	ln := d.lines[li]
	k := ln.first + sort.Search(ln.last-ln.first, func(i int) bool {
		return d.atoms[ln.first+i].start >= off
	})
	if k > ln.first && k < ln.last && d.atoms[k].start == off {
		return k, true
	}
	return 0, false
}

// column returns the column (in runes, indentation included) where the
// gap before atom k of line li starts: the end of atom k-1. It fails
// when an atom before k spans lines.
func (d *doc) column(li, k int) (int, bool) {
	ln := d.lines[li]
	w := max(ln.level, 0) * d.opts.Indent
	for i := ln.first; i < k; i++ {
		if i > ln.first {
			w += utf8.RuneCountInString(d.gap[i])
		}
		text := d.atoms[i].text
		if d.atoms[i].isChunk() || d.atoms[i].psl ||
			strings.ContainsAny(text, "\r\n") {
			return 0, false
		}
		w += utf8.RuneCountInString(text)
	}
	return w, true
}
