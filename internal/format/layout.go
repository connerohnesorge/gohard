package format

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/token"
)

// line is one output line: atoms [first, last) of the doc.
type line struct {
	first, last int
	// level is the indentation level (ignored for a chunk line, whose
	// text carries its own indentation).
	level int
	// blank is the number of blank lines written before the line.
	blank int
	// pending marks a comment-led line whose level comes from the next
	// code line; pslLed marks a `-- psl` line (no closer bonus).
	pending, pslLed bool
}

// doc is the formatter's working model: the atoms, the in-line gaps
// and the lines. The layout passes (wrap.go, align.go) change only
// gaps, line breaks and levels.
type doc struct {
	src   []byte
	opts  Options
	atoms []atom
	// gap[i] is the text written between atom i-1 and atom i when atom
	// i is not the first atom of its line (spaces only, except inside
	// `-- psl` comment lines where it is the original whitespace).
	gap   []string
	lines []line
	// lineOf[i] is the line of atom i.
	lineOf []int
	// eol is the dominant line ending, "\n" or "\r\n".
	eol string

	// parens is the indenter's paren stack (innermost first) and pend
	// its comment-led lines waiting for the level of the next token.
	parens *paren
	pend   *pendLine
	// visits counts the atoms indentLine walked: the wrap pass's cost
	// is bounded by it, which the tests check without timing.
	visits int
	// closes caches closeOf for the `(` atoms (-1 until computed): the
	// wrap pass indents a line again after breaking it, and closeOf
	// walks the siblings of the `(`.
	closes []int
}

// paren is an open parenthesis on the paren stack.
type paren struct {
	open, close int // offsets of `(` and of its `)` (or its node's end)
	level       int // level of the line holding the `(`
	next        *paren
}

// pendLine is a line on the indenter's pending list.
type pendLine struct {
	li   int
	next *pendLine
}

// build runs the base layout: atoms, lines, levels, gaps and blank
// lines.
func build(res *parser.Result, opts Options) *doc {
	src := res.Root.AppendText(nil)
	atoms, verb := collect(res, src, opts)
	d := &doc{src: src, opts: opts, atoms: chunkify(src, atoms, verb),
		eol: dominantEOL(src)}
	d.splitLines()
	d.indent()
	d.spaceLines()
	d.blankLines()
	return d
}

// dominantEOL returns "\r\n" when CR LF line ends outnumber LF ones,
// else "\n".
func dominantEOL(src []byte) string {
	lf, crlf := 0, 0
	for i, c := range src {
		if c == '\n' {
			if i > 0 && src[i-1] == '\r' {
				crlf++
			} else {
				lf++
			}
		}
	}
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}

// newlines counts the line ends (LF, CR LF, CR) in a gap.
func newlines(s []byte) int {
	n := 0
	for i, c := range s {
		if c == '\n' || (c == '\r' && (i+1 == len(s) || s[i+1] != '\n')) {
			n++
		}
	}
	return n
}

// origGap returns the source text before atom i.
func (d *doc) origGap(i int) []byte {
	start := 0
	if i > 0 {
		start = d.atoms[i-1].end
	}
	return d.src[start:d.atoms[i].start]
}

// splitLines groups the atoms into lines at the source line breaks.
func (d *doc) splitLines() {
	d.gap = make([]string, len(d.atoms))
	d.lineOf = make([]int, len(d.atoms))
	for i := range d.atoms {
		nl := newlines(d.origGap(i))
		if i == 0 || nl > 0 {
			if n := len(d.lines); n > 0 {
				d.lines[n-1].last = i
			}
			d.lines = append(d.lines, line{first: i, last: i + 1,
				blank: max(nl-1, 0)})
		}
		d.lineOf[i] = len(d.lines) - 1
	}
	if n := len(d.lines); n > 0 {
		d.lines[n-1].last = len(d.atoms)
	}
}

// indent computes the level of every line in source order, keeping the
// paren stack in step with the tokens.
func (d *doc) indent() {
	d.parens, d.pend = nil, nil
	for li := range d.lines {
		d.indentLine(li)
	}
	d.flushPending()
}

// indentLine runs the indenter over the atoms of line li, which must
// follow the lines it ran over before. The state it reads and leaves
// is d.parens and d.pend: both are immutable lists, so a caller can
// save them before a line and restart there (the wrap pass does).
func (d *doc) indentLine(li int) {
	ln := &d.lines[li]
	d.visits += ln.last - ln.first
	for i := ln.first; i < ln.last; i++ {
		a := &d.atoms[i]
		start := ln.first == i
		if a.isChunk() {
			continue
		}
		if !a.isToken() {
			if start {
				ln.pending = true
				ln.pslLed = a.kind == token.PSLCommentMarker
				d.pend = &pendLine{li: li, next: d.pend}
			}
			continue
		}
		t := a.tok
		d.popStale(a.start)
		if start || ln.pending {
			lv := d.levelOf(t)
			for p := d.pend; p != nil; p = p.next {
				pl := &d.lines[p.li]
				pl.level = lv
				if p.li != li && !pl.pslLed && closer(t) {
					pl.level++
				}
				pl.pending = false
			}
			d.pend = nil
			ln.level = lv
			ln.pending = false
		}
		switch t.Kind() {
		case token.RParen:
			if top := d.parens; top != nil && top.close == a.start {
				d.parens = top.next
			}
		case token.LParen:
			d.parens = &paren{open: a.start, close: d.closeAt(i),
				level: ln.level, next: d.parens}
		}
	}
}

// flushPending puts the comment lines left at the end of the file at
// level 0.
func (d *doc) flushPending() {
	for p := d.pend; p != nil; p = p.next {
		d.lines[p.li].level = 0
		d.lines[p.li].pending = false
	}
	d.pend = nil
}

// closeAt returns closeOf for the `(` atom i, computing it once.
func (d *doc) closeAt(i int) int {
	if d.closes == nil {
		d.closes = make([]int, len(d.atoms))
		for k := range d.closes {
			d.closes[k] = -1
		}
	}
	if d.closes[i] < 0 {
		d.closes[i] = closeOf(d.atoms[i].tok)
	}
	return d.closes[i]
}

// popStale drops the parens closed before off (unbalanced input).
func (d *doc) popStale(off int) {
	for d.parens != nil && d.parens.close < off {
		d.parens = d.parens.next
	}
}

// closeOf returns the offset of the `)` matching the `(` t among its
// siblings, or the end of the parent node when there is none.
func closeOf(t cst.Token) int {
	p := t.Parent()
	depth := 0
	i := 0
	for e := range p.Elements() {
		if i <= t.Index() || !e.IsToken() {
			i++
			continue
		}
		i++
		switch e.Token.Kind() {
		case token.LParen:
			depth++
		case token.RParen:
			if depth == 0 {
				return e.Token.Span().Start
			}
			depth--
		}
	}
	return p.Span().End
}

// levelOf returns the level of a line that starts with token t, given
// the paren stack before t.
func (d *doc) levelOf(t cst.Token) int {
	top := d.parens
	start := t.Span().Start
	if t.Kind() == token.RParen && top != nil && top.close == start {
		return top.level
	}
	it := item(t.Parent())
	if it.IsZero() {
		if top != nil {
			return top.level + 1
		}
		return 0
	}
	f := firstTok(it)
	if sameTok(f, t) {
		return d.itemStart(it, top)
	}
	if top != nil && top.open >= f.Span().Start {
		return top.level + 1
	}
	if structural(t.Kind()) {
		return d.lineLevelAt(f.Span().Start)
	}
	return d.lineLevelAt(f.Span().Start) + 1
}

// itemStart returns the level of a line that starts with the first
// token of item it.
func (d *doc) itemStart(it cst.Node, top *paren) int {
	for {
		p := item(it.Parent())
		if p.IsZero() {
			if top != nil {
				return top.level + 1
			}
			return 0
		}
		fp := firstTok(p)
		if sameTok(fp, firstTok(it)) {
			it = p
			continue
		}
		if top != nil && top.open >= fp.Span().Start {
			return top.level + 1
		}
		base := d.lineLevelAt(fp.Span().Start)
		if sibling(it.Kind()) {
			return base
		}
		return base + 1
	}
}

// lineLevelAt returns the level of the line holding offset off. For a
// verbatim chunk it is the original indentation of that source line.
func (d *doc) lineLevelAt(off int) int {
	li := sort.Search(len(d.lines), func(i int) bool {
		return d.atoms[d.lines[i].first].start > off
	}) - 1
	if li < 0 {
		return 0
	}
	ln := d.lines[li]
	if d.atoms[ln.first].isChunk() {
		return d.origLevel(off)
	}
	return max(ln.level, 0)
}

// origLevel returns the indentation level of the source line holding
// off: its leading columns (a tab counts as one indent) over the
// indent.
func (d *doc) origLevel(off int) int {
	s := min(off, len(d.src))
	for s > 0 && d.src[s-1] != '\n' && d.src[s-1] != '\r' {
		s--
	}
	w := 0
	for ; s < len(d.src); s++ {
		switch d.src[s] {
		case ' ':
			w++
		case '\t':
			w += d.opts.Indent
		default:
			return w / d.opts.Indent
		}
	}
	return w / d.opts.Indent
}

func sameTok(a, b cst.Token) bool {
	return a.Green() == b.Green() && a.FullSpan() == b.FullSpan()
}

// firstTok returns the first active token of n (it may be a missing
// token), or the zero Token.
func firstTok(n cst.Node) cst.Token {
	for t := range n.DescendantTokens() {
		if !t.Inactive() {
			return t
		}
	}
	return cst.Token{}
}

// item returns n or its nearest ancestor that is an item: a construct
// that starts its own line (a unit, declaration, statement, interface
// element, association, branch or end clause).
func item(n cst.Node) cst.Node {
	for ; !n.IsZero(); n = n.Parent() {
		if itemKinds[n.Kind()] {
			return n
		}
	}
	return cst.Node{}
}

var itemKinds = func() map[cst.Kind]bool {
	m := map[cst.Kind]bool{}
	for _, g := range []string{"LibraryUnit", "ContextItem", "Decl",
		"ConcStmt", "SeqStmt", "InterfaceDecl", "ConfigItem",
		"PSLUnitItem"} {
		for _, k := range cst.GroupKinds(g) {
			m[k] = true
		}
	}
	for _, k := range []cst.Kind{cst.KindEndClause, cst.KindElsifBranch,
		cst.KindElseBranch, cst.KindCaseAlternative,
		cst.KindCaseGenerateAlternative, cst.KindGenerateBranch,
		cst.KindWhenBranch, cst.KindSelectBranch,
		cst.KindAssociationElement, cst.KindElementAssociation,
		cst.KindElementDecl, cst.KindPrimaryUnitDecl,
		cst.KindSecondaryUnitDecl, cst.KindGenericClause,
		cst.KindPortClause, cst.KindGenericMapAspect,
		cst.KindPortMapAspect, cst.KindModeViewElementDef,
		cst.KindPSLFormalParameter} {
		m[k] = true
	}
	delete(m, cst.KindErrorNode)
	delete(m, cst.KindInactiveBranch)
	return m
}()

// sibling reports whether an item of kind k that starts a line stays
// at the level of the construct it belongs to.
func sibling(k cst.Kind) bool {
	switch k {
	case cst.KindEndClause, cst.KindElsifBranch, cst.KindElseBranch,
		cst.KindGenerateBranch:
		return true
	}
	return false
}

// structural reports whether a token that starts a line inside an item
// (not as its first token) stays at the item's level.
func structural(k token.Kind) bool {
	switch k {
	case token.KwBegin, token.KwEnd, token.RBrace:
		return true
	}
	return false
}

// closer reports whether t closes a region, so comment lines just
// above it belong to the region and are one level deeper.
func closer(t cst.Token) bool {
	switch t.Kind() {
	case token.KwEnd, token.KwBegin, token.RParen, token.RBrace:
		return true
	case token.KwElse, token.KwElsif:
		switch t.Parent().Kind() {
		case cst.KindElseBranch, cst.KindElsifBranch,
			cst.KindGenerateBranch:
			return true
		}
	}
	return false
}

// opener reports whether a line ending with t opens a region, so blank
// lines just below it are dropped.
func opener(t cst.Token) bool {
	switch t.Kind() {
	case token.KwIs, token.KwBegin, token.KwThen, token.KwElse,
		token.KwGenerate, token.KwLoop, token.LParen, token.KwRecord,
		token.KwUnits, token.KwProtected, token.KwBody, token.LBrace:
		return true
	case token.Arrow:
		switch t.Parent().Kind() {
		case cst.KindCaseAlternative, cst.KindCaseGenerateAlternative:
			return true
		}
	}
	return false
}

// blankLines applies the blank-line rules: at most one blank line, none
// at the start of the file and none at the start or end of a region.
func (d *doc) blankLines() {
	for li := range d.lines {
		ln := &d.lines[li]
		ln.blank = min(ln.blank, 1)
		if li == 0 {
			ln.blank = 0
			continue
		}
		if ln.blank == 0 {
			continue
		}
		if a := &d.atoms[ln.first]; a.isToken() && closer(a.tok) {
			ln.blank = 0
			continue
		}
		prev := d.lines[li-1]
		for i := prev.last - 1; i >= prev.first; i-- {
			a := &d.atoms[i]
			if a.isComment() {
				continue
			}
			if a.isToken() && !a.psl && opener(a.tok) {
				ln.blank = 0
			}
			break
		}
	}
}

// Helpers for the layout passes.

// width returns the width of line li as written, in characters (runes),
// indentation included; a multi-line atom counts up to its first line
// break, and the line ends there.
func (d *doc) width(li int) int {
	ln := d.lines[li]
	w := 0
	if !d.atoms[ln.first].isChunk() {
		w = max(ln.level, 0) * d.opts.Indent
	}
	for i := ln.first; i < ln.last; i++ {
		if i > ln.first {
			w += utf8.RuneCountInString(d.gap[i])
		}
		text := d.atoms[i].text
		if k := strings.IndexAny(text, "\r\n"); k >= 0 {
			return w + utf8.RuneCountInString(text[:k])
		}
		w += utf8.RuneCountInString(text)
	}
	return w
}

// commentOnly reports whether line li holds only comments.
func (d *doc) commentOnly(li int) bool {
	ln := d.lines[li]
	for i := ln.first; i < ln.last; i++ {
		if !d.atoms[i].isComment() {
			return false
		}
	}
	return true
}
