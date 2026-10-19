package format

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Wrapping (#32, format.max_line_length), in golines style.
//
// A line is too long when its code (the line without its trailing
// comments, which never cause a wrap and stay at the end of their
// line) is wider than format.max_line_length. Lines holding verbatim
// chunks or `-- psl` comment text are never wrapped. A too-long line
// is shortened by the first of these that adds a line break:
//
//  1. Map and clause breaks: a generic or port map, or a generic or
//     port clause, that starts inside the line moves to a line of its
//     own, so an instantiation becomes `u : entity work.e`,
//     `generic map (...)`, `port map (...)`.
//  2. The enclosing list: when the line holds a `,` or `;` of the
//     parenthesized list that contains its start (a list already
//     wrapped by hand), that list is exploded.
//  3. A list opened on the line: of the parenthesized groups opened on
//     the line with content after the `(` on the line, the outermost
//     ones are candidates; one whose line up to the `(` then fits is
//     preferred, then a list with separators, then the widest, then
//     the leftmost. A group without separators is only exploded when
//     each of its three lines then fits: the line up to the `(`, its
//     content and the `)` with the rest of the line.
//
// Exploding a group puts each of its elements on a line of its own,
// one level deeper, and the `)` on a line of its own at the level of
// the line holding the `(`:
//
//	port map (
//	  a => b,
//	  c => d
//	);
//
// A line nothing applies to is unbreakable and stays as it is. The
// pieces of a wrapped line are wrapped in turn.
//
// The pass is one walk over the lines in source order that runs the
// indenter (doc.indentLine) as it goes, so every line is checked with
// the level the base layout gives it when the output is formatted
// again: a line's level depends only on the lines before it. A line
// that gets broken is checked again from its start, and when a break
// lands on an earlier line (exploding a list wrapped by hand) the walk
// restarts at that line with the indenter state saved there. Each
// final line was checked with its final level and needed no break,
// which makes wrapping idempotent; and the cost is that of the lines
// checked, not a pass over the file per wrapping depth.

func init() { wrapPass = wrapLines }

// wrapper holds the paren structure of the atoms, which wrapping never
// changes, and the line starts.
type wrapper struct {
	d *doc
	// match[i] is the atom index of the paren matching the paren atom
	// i, or -1.
	match []int
	// encl[i] is the atom index of the innermost `(` enclosing atom i
	// (for a paren, the one around it), or -1.
	encl []int
	// seps[o] lists, in order, the atom indexes of the separators of
	// the group opened at atom o (see sepOf).
	seps [][]int
	// bad[i] counts the verbatim chunks and `-- psl` atoms before atom
	// i, so a range of atoms is checked for them in O(1).
	bad []int
	// start[i] reports whether atom i starts a line: a source line or
	// a break of the pass.
	start []bool
	// blank[i] is the blank-line count of the source line starting at
	// atom i.
	blank []int
	// first is the first atom the pass broke before since it was reset,
	// or -1.
	first int
}

// wrapLines runs the pass. d.lines is rebuilt as the walk goes: it
// holds the finished lines and, last, the line being checked.
func wrapLines(d *doc) {
	w := newWrapper(d)
	n := len(d.atoms)
	type state struct {
		parens *paren
		pend   *pendLine
	}
	saved := make([]state, 0, len(d.lines))
	d.lines = d.lines[:0]
	d.parens, d.pend = nil, nil
	for p := 0; p < n; {
		e := p + 1
		for e < n && !w.start[e] {
			e++
		}
		li := len(d.lines)
		saved = append(saved, state{d.parens, d.pend})
		d.lines = append(d.lines, line{first: p, last: e,
			blank: w.blank[p]})
		for i := p; i < e; i++ {
			d.lineOf[i] = li
		}
		d.indentLine(li)
		w.first = -1
		if !w.wrapLine(li) {
			p = e
			continue
		}
		// Restart at the line of the first break.
		at := li
		if w.first < p {
			at = d.lineOf[w.first]
		}
		p = d.lines[at].first
		d.parens, d.pend = saved[at].parens, saved[at].pend
		d.lines, saved = d.lines[:at], saved[:at]
	}
	d.flushPending()
}

// brk starts a line at atom i, which must not start one.
func (w *wrapper) brk(i int) {
	w.start[i] = true
	w.d.gap[i] = ""
	if w.first < 0 || i < w.first {
		w.first = i
	}
}

// onLine reports whether atom i is on line li.
func (w *wrapper) onLine(i, li int) bool {
	ln := w.d.lines[li]
	return i >= ln.first && i < ln.last
}

func newWrapper(d *doc) *wrapper {
	n := len(d.atoms)
	w := &wrapper{d: d, match: make([]int, n), encl: make([]int, n),
		seps: make([][]int, n), bad: make([]int, n+1),
		start: make([]bool, n), blank: make([]int, n)}
	for _, ln := range d.lines {
		w.start[ln.first] = true
		w.blank[ln.first] = ln.blank
	}
	var stack []int
	for i := range d.atoms {
		w.match[i] = -1
		w.encl[i] = -1
		if n := len(stack); n > 0 {
			w.encl[i] = stack[n-1]
		}
		a := &d.atoms[i]
		w.bad[i+1] = w.bad[i]
		if a.isChunk() || a.psl {
			w.bad[i+1]++
		}
		if o := w.encl[i]; o >= 0 && w.sepOf(i, o) {
			w.seps[o] = append(w.seps[o], i)
		}
		if !a.isToken() {
			continue
		}
		switch a.kind {
		case token.LParen:
			stack = append(stack, i)
		case token.RParen:
			if n := len(stack); n > 0 {
				o := stack[n-1]
				stack = stack[:n-1]
				w.match[o], w.match[i] = i, o
			}
		}
	}
	return w
}

// wrapLine shortens line li if it is too long and can be shortened. It
// reports whether it added a line break.
func (w *wrapper) wrapLine(li int) bool {
	d := w.d
	ln := d.lines[li]
	if w.bad[ln.last] != w.bad[ln.first] {
		return false
	}
	if d.codeWidth(li) <= d.opts.MaxLineLength {
		return false
	}
	if w.breakAspects(li) {
		return true
	}
	if o := w.encl[ln.first]; o >= 0 && w.hasSep(li, o) {
		if w.explode(o) {
			return true
		}
	}
	if o := w.pickGroup(li); o >= 0 && w.explode(o) {
		return true
	}
	return false
}

// codeWidth returns the width of line li without its trailing
// comments.
func (d *doc) codeWidth(li int) int {
	ln := d.lines[li]
	end := ln.last
	for end > ln.first+1 && d.atoms[end-1].isComment() {
		end--
	}
	w := max(ln.level, 0) * d.opts.Indent
	for i := ln.first; i < end; i++ {
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

// breakAspects moves every generic or port map and generic or port
// clause that starts inside line li to a line of its own.
func (w *wrapper) breakAspects(li int) bool {
	d := w.d
	ln := d.lines[li]
	broke := false
	for i := ln.first + 1; i < ln.last; i++ {
		if a := &d.atoms[i]; a.isToken() && aspectStart(a.tok) {
			w.brk(i)
			broke = true
		}
	}
	return broke
}

// aspectStart reports whether t is the first token of a generic or
// port map or clause.
func aspectStart(t cst.Token) bool {
	if t.Kind() != token.KwGeneric && t.Kind() != token.KwPort {
		return false
	}
	p := t.Parent()
	switch p.Kind() {
	case cst.KindGenericMapAspect, cst.KindPortMapAspect,
		cst.KindGenericClause, cst.KindPortClause:
		return sameTok(firstTok(p), t)
	}
	return false
}

// sepOf reports whether atom i separates the elements of the list
// opened at atom o: a `,` or `;` of the same node as the `(`, so the
// commas of an identifier list inside an interface list do not count.
// It walks the tree, so newWrapper calls it once per atom and records
// the answers in seps.
func (w *wrapper) sepOf(i, o int) bool {
	a := &w.d.atoms[i]
	return w.encl[i] == o && a.isToken() &&
		(a.kind == token.Comma || a.kind == token.Semicolon) &&
		a.tok.Parent().Equal(w.d.atoms[o].tok.Parent())
}

// hasSep reports whether line li holds a separator of the group opened
// at atom o.
func (w *wrapper) hasSep(li, o int) bool {
	ln := w.d.lines[li]
	s := w.seps[o]
	k, _ := slices.BinarySearch(s, ln.first)
	return k < len(s) && s[k] < ln.last
}

// explodable reports whether the group opened at atom o can be
// exploded: it is closed, holds a token and no verbatim chunk.
func (w *wrapper) explodable(o int) bool {
	c := w.match[o]
	return c >= 0 && w.nextTok(o) < c && w.bad[c] == w.bad[o+1]
}

// nextTok returns the index of the first token atom after atom i.
func (w *wrapper) nextTok(i int) int {
	for i++; i < len(w.d.atoms) && !w.d.atoms[i].isToken(); i++ {
	}
	return i
}

// pickGroup returns the group opened on line li to explode, or -1.
func (w *wrapper) pickGroup(li int) int {
	d := w.d
	ln := d.lines[li]
	type cand struct {
		o, depth, width int
		head, sep       bool
	}
	best := cand{o: -1}
	m := w.measure(li)
	var open []int // parens opened on the line and still open
	for o := ln.first; o < ln.last; o++ {
		a := &d.atoms[o]
		if !a.isToken() {
			continue
		}
		if a.kind == token.RParen {
			if k := len(open); k > 0 && open[k-1] == w.match[o] {
				open = open[:k-1]
			}
			continue
		}
		if a.kind != token.LParen {
			continue
		}
		depth := len(open)
		open = append(open, o)
		if !w.explodable(o) || !w.onLine(w.nextTok(o), li) {
			continue
		}
		c := cand{o: o, depth: depth, sep: len(w.seps[o]) > 0,
			width: m.contentWidth(o)}
		col, ok := m.column(o + 1)
		c.head = ok && col <= d.opts.MaxLineLength
		if !c.sep && !w.singleFits(m, o, c.width, c.head) {
			continue // exploding it would not help
		}
		if best.o < 0 || better(c.depth, best.depth, c.head, best.head,
			c.sep, best.sep, c.width, best.width) {
			best = c
		}
	}
	return best.o
}

// lineMeasure holds prefix widths of one line, so the columns and
// widths pickGroup asks for cost O(1) each instead of a walk over the
// line: on a line with many groups the walks made wrapping cubic.
type lineMeasure struct {
	w  *wrapper
	li int
	// pre[k] is the width of the atoms first..first+k-1 with the gaps
	// between them, each multi-line atom counted whole.
	pre []int
	// flat is the index of the first atom holding a line break, or
	// the line's end: column is defined only up to it.
	flat int
}

func (w *wrapper) measure(li int) *lineMeasure {
	d := w.d
	ln := d.lines[li]
	m := &lineMeasure{w: w, li: li, pre: make([]int, ln.last-ln.first+1),
		flat: ln.last}
	for i := ln.first; i < ln.last; i++ {
		n := utf8.RuneCountInString(d.atoms[i].text)
		if i > ln.first {
			n += utf8.RuneCountInString(d.gap[i])
		}
		m.pre[i-ln.first+1] = m.pre[i-ln.first] + n
		if m.flat == ln.last && strings.ContainsAny(d.atoms[i].text,
			"\r\n") {
			m.flat = i
		}
	}
	return m
}

// column is d.column(li, k) for a line without chunks or psl text.
func (m *lineMeasure) column(k int) (int, bool) {
	d := m.w.d
	ln := d.lines[m.li]
	if k > m.flat {
		return 0, false
	}
	return max(ln.level, 0)*d.opts.Indent + m.pre[k-ln.first], true
}

// contentWidth returns the width, on the line, of the content of the
// group opened at atom o: from the token after the `(` to the `)` or
// the end of the line's code.
func (m *lineMeasure) contentWidth(o int) int {
	d := m.w.d
	ln := d.lines[m.li]
	end := min(m.w.match[o], ln.last)
	for end > o+1 && d.atoms[end-1].isComment() {
		end--
	}
	start := m.w.nextTok(o)
	if start >= end {
		return 0
	}
	return m.pre[end-ln.first] - m.pre[start-ln.first] -
		utf8.RuneCountInString(d.gap[start])
}

// singleFits reports whether exploding the group without separators
// opened at atom o on the measured line makes all three of its lines
// fit: the line up to the `(` (head), the content one level deeper
// (width), and the `)` with the rest of the line when it closes on the
// line. Splitting `rising_edge(clk)` off a line whose rest is still
// too long gains nothing, so such a line stays as it is.
func (w *wrapper) singleFits(m *lineMeasure, o, width int,
	head bool) bool {
	d := w.d
	ln := d.lines[m.li]
	lvl := max(ln.level, 0) * d.opts.Indent
	if !head || lvl+d.opts.Indent+width > d.opts.MaxLineLength {
		return false
	}
	c := w.match[o]
	if !w.onLine(c, m.li) {
		return true
	}
	col, ok := m.column(c)
	if !ok {
		return false
	}
	tail := d.codeWidth(m.li) - col - utf8.RuneCountInString(d.gap[c])
	return lvl+tail <= d.opts.MaxLineLength
}

// better ranks two candidate groups: the outermost first, then one
// whose line up to the `(` fits, then a list with separators, then the
// widest content. On a tie the earlier (leftmost) one stays.
func better(depth, bDepth int, head, bHead, sep, bSep bool, width,
	bWidth int) bool {
	switch {
	case depth != bDepth:
		return depth < bDepth
	case head != bHead:
		return head
	case sep != bSep:
		return sep
	}
	return width > bWidth
}

// groupHasSep reports whether the group opened at atom o has a
// separator.
func (w *wrapper) groupHasSep(o int) bool { return len(w.seps[o]) > 0 }

// explode puts each element of the group opened at atom o on a line of
// its own and its `)` on a line of its own. It reports whether it
// added a line break.
func (w *wrapper) explode(o int) bool {
	if !w.explodable(o) {
		return false
	}
	c := w.match[o]
	broke := false
	add := func(i int) {
		if i <= c && !w.start[i] {
			w.brk(i)
			broke = true
		}
	}
	add(w.nextTok(o))
	for _, i := range w.seps[o] {
		add(w.nextTok(i))
	}
	add(c)
	return broke
}
