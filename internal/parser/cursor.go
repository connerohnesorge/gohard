package parser

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Parser is the state of one parse. Grammar code (units.go, lists.go
// and the builders' files decl.go, types.go, seq.go, conc.go, expr.go,
// psl.go, v2019.go) uses only the methods in this file to read tokens
// and build the tree.
//
// Conventions for grammar functions:
//
//   - A parseX function that may not apply returns false WITHOUT
//     consuming anything when the current token cannot start X; it
//     returns true once it has consumed at least one token.
//   - Open the node first (open), then consume, then close. Wrap an
//     already-parsed left part with mark/openAt (binary operators, name
//     suffixes).
//   - Use expect/expectKw for required tokens: on a mismatch they report
//     syntax/error and insert a zero-width missing token, consuming
//     nothing. Use recover to skip tokens into an ErrorNode.
//   - Identifiers go through ident (or identOr), which applies the PSL
//     reservation rules; keywords through atKw/bumpKw/expectKw, which
//     accept a later standard's keyword lexed as an identifier and
//     report syntax/standard.
//   - Every loop must stop at end of input: at returns false and
//     atEOF true when the input (or the inactive branch being parsed) is
//     exhausted, or when the budget ran out.
//   - List loops call p.inactive(item) at the head of every iteration so
//     an inactive conditional-analysis branch becomes a structured
//     InactiveBranch subtree.
//   - Recursion goes through enter/leave so deeply nested input cannot
//     exhaust the stack.
type Parser struct {
	src  []byte
	text string
	opts Options
	lx   *lexer.Lexer
	b    *cst.Builder

	buf  []raw // raw tokens; buf[pos] is the next one
	pos  int
	fOff int         // lexing frontier: offset after the last raw token
	fSt  lexer.State // state at the frontier
	eof  bool        // the EOF token is in buf

	mode lexer.Mode
	sub  int // 1 + index of the inactive region being parsed, or 0

	steps, budget, fuel int
	dead                bool
	tooLarge            bool
	maxNodes            int
	depth               int
	lastEnd             int // end of the text of the last token emitted
	// emitted counts the tokens added to the tree. Use it, not pos
	// (which compaction moves back), to tell whether a call consumed
	// anything.
	emitted int

	// pslNames maps the lower-cased names of the PSL properties and
	// sequences declared so far in the file to their declaration kind,
	// so that `p(a, b)` builds a property or sequence instance and a
	// concurrent `assert p(a, b)` is a PSL directive (#31). Only
	// looked up, never iterated: no output depends on map order.
	pslNames map[string]cst.Kind

	probs        []Problem
	dropped      int // problems not recorded past MaxProblems
	firstDropped int // offset of the first dropped problem
	lead, trail  []cst.Trivia
	arena        []lexer.Trivia
	parena       []lexer.Problem
	reportedDeep bool
	// cut: tooDeep consumed the rest of the input; the unclosed levels
	// then report no syntax errors (one "nested too deeply" error
	// stands for them).
	cut bool
}

// raw is a buffered token. It holds no pointers (its trivia and
// problems are index ranges into the parser's arenas), so buffering
// costs no GC work.
type raw struct {
	lexer.Token
	tlo, tmid, thi int32 // lead = arena[tlo:tmid], trail = arena[tmid:thi]
	plo, phi       int32 // problems = parena[plo:phi]
	before         lexer.State
	skip           int32 // for the first token of an inactive region: index after
}

func newParser(src []byte, opts Options, lo lexer.Options) *Parser {
	p := &Parser{
		src:      src,
		text:     string(src),
		opts:     opts,
		lx:       lexer.New(src, lo),
		b:        cst.NewBuilder(),
		fSt:      lexer.Initial(lexer.VHDL),
		maxNodes: cmpOr(opts.MaxNodes, MaxNodes),
	}
	p.budget = opts.Budget
	if p.budget <= 0 {
		p.budget = stepsPerByte*len(src) + baseSteps
	}
	return p
}

// std returns the selected standard.
func (p *Parser) std() token.Standard { return p.opts.Standard }

// Mode returns the current lexer mode.
func (p *Parser) Mode() lexer.Mode { return p.mode }

// setMode switches the lexer mode for the current token and everything
// after it: buffered lookahead is lexed again in the new mode. The PSL
// grammar calls setMode(lexer.PSL) at a PSL trigger (before consuming
// the trigger word) and setMode(lexer.VHDL) after the `;` or `}` that
// ends the PSL text (#6).
func (p *Parser) setMode(m lexer.Mode) {
	if m == p.mode {
		return
	}
	p.mode = m
	if p.pos < len(p.buf) {
		r := p.buf[p.pos]
		p.buf = p.buf[:p.pos]
		p.arena = p.arena[:r.tlo]
		p.parena = p.parena[:r.plo]
		p.fOff, p.fSt = r.FullStart, r.before.WithMode(m)
		p.eof = false
		return
	}
	p.fSt = p.fSt.WithMode(m)
}

// fill makes sure n raw tokens from pos are buffered (or EOF is).
func (p *Parser) fill(n int) {
	for len(p.buf)-p.pos < n && !p.eof {
		t, st := p.lx.Lex(p.fOff, p.fSt)
		r := raw{Token: t, before: p.fSt}
		r.tlo = int32(len(p.arena))
		p.arena = append(p.arena, p.lx.Leading(t)...)
		r.tmid = int32(len(p.arena))
		p.arena = append(p.arena, p.lx.Trailing(t)...)
		r.thi = int32(len(p.arena))
		r.plo = int32(len(p.parena))
		p.parena = append(p.parena, p.lx.Problems(t)...)
		r.phi = int32(len(p.parena))
		p.lx.Reset()
		p.buf = append(p.buf, r)
		p.fOff, p.fSt = t.FullEnd, st
		if t.Kind == token.EOF {
			p.eof = true
		}
	}
}

// compact drops consumed tokens (and their trivia and problems) from
// the buffers once they dominate them, so memory stays bounded by the
// lookahead.
func (p *Parser) compact() {
	if p.pos < 64 || p.pos*2 < len(p.buf) {
		return
	}
	n := copy(p.buf, p.buf[p.pos:])
	p.buf = p.buf[:n]
	if n == 0 {
		p.arena, p.parena = p.arena[:0], p.parena[:0]
	} else {
		tb, pb := p.buf[0].tlo, p.buf[0].plo
		p.arena = p.arena[:copy(p.arena, p.arena[tb:])]
		p.parena = p.parena[:copy(p.parena, p.parena[pb:])]
		for i := range p.buf {
			r := &p.buf[i]
			r.tlo, r.tmid, r.thi = r.tlo-tb, r.tmid-tb, r.thi-tb
			r.plo, r.phi = r.plo-pb, r.phi-pb
			if r.skip > 0 {
				r.skip -= int32(p.pos)
			}
		}
	}
	p.pos = 0
}

// rawAt returns buffered token i (filling as needed); past EOF it
// returns the EOF token.
func (p *Parser) rawAt(i int) *raw {
	p.fill(i - p.pos + 1)
	if i >= len(p.buf) {
		return &p.buf[len(p.buf)-1]
	}
	return &p.buf[i]
}

// skipRegion returns the index just after the inactive region whose
// first token is buf[i].
func (p *Parser) skipRegion(i int) int {
	r := p.rawAt(i)
	if int(r.skip) > i {
		return int(r.skip)
	}
	region := r.Region
	j := i + 1
	for j < len(p.buf) || !p.eof {
		if p.rawAt(j).Region != region || p.rawAt(j).Kind == token.EOF {
			break
		}
		j++
	}
	p.buf[i].skip = int32(j)
	return j
}

// nthIdx returns the buffer index of the nth (from 0) visible token, or
// -1 when the inactive branch being parsed ends first. In the main parse
// inactive tokens are invisible; inside p.inactive only the tokens of
// that branch are visible.
func (p *Parser) nthIdx(n int) int {
	i := p.pos
	for {
		r := p.rawAt(i)
		if r.Kind == token.EOF {
			if p.sub != 0 {
				return -1
			}
			return min(i, len(p.buf)-1)
		}
		if p.sub != 0 {
			if r.Region != p.sub {
				return -1
			}
		} else if r.Region != 0 {
			i = p.skipRegion(i)
			continue
		}
		if n == 0 {
			return i
		}
		n--
		i++
	}
}

// step charges one unit of budget; it kills the parse when the budget
// or the progress fuel runs out, or the tree grows past MaxNodes.
func (p *Parser) step() bool {
	if p.dead {
		return false
	}
	p.steps++
	p.fuel++
	if p.b.Nodes() > p.maxNodes {
		p.tooLarge = true
		p.dead = true
		return false
	}
	if p.steps > p.budget || p.fuel > maxFuel {
		p.dead = true
		p.problemAt(p.peekStart(), p.peekStart(), diag.SyntaxError,
			"the parser stopped here (step budget exhausted); the "+
				"rest of the file is not parsed")
		return false
	}
	return true
}

func (p *Parser) peekStart() int {
	if i := p.nthIdxNoStep(); i >= 0 {
		return p.buf[i].Start
	}
	return p.lastEnd
}

func (p *Parser) nthIdxNoStep() int {
	if p.dead && p.pos >= len(p.buf) {
		return -1
	}
	return p.nthIdx(0)
}

// peek returns the nth (from 0) visible token, or nil at the end of the
// input (or of the inactive branch) and after the budget ran out.
func (p *Parser) peek(n int) *raw {
	if !p.step() {
		return nil
	}
	i := p.nthIdx(n)
	if i < 0 {
		return nil
	}
	return &p.buf[i]
}

// nth returns the kind of the nth (from 0) visible token; EOF at the end.
func (p *Parser) nth(n int) token.Kind {
	if r := p.peek(n); r != nil {
		return r.Kind
	}
	return token.EOF
}

// nthWord returns the keyword spelled by the nth visible token (its
// Word), or 0.
func (p *Parser) nthWord(n int) token.Kind {
	if r := p.peek(n); r != nil {
		return r.Word
	}
	return 0
}

// nthText returns the text of the nth visible token.
func (p *Parser) nthText(n int) string {
	if r := p.peek(n); r != nil && r.Kind != token.EOF {
		return p.text[r.Start:r.End]
	}
	return ""
}

// atEOF reports whether no visible token is left.
func (p *Parser) atEOF() bool { return p.nth(0) == token.EOF }

// at reports whether the current token has kind k exactly.
func (p *Parser) at(k token.Kind) bool { return p.nth(0) == k }

// atAny reports whether the current token's kind is in s.
func (p *Parser) atAny(s token.Set) bool { return s.Has(p.nth(0)) }

// atKw reports whether the current token is keyword k, either lexed as
// k or as an identifier spelling k (a later standard's keyword, a PSL
// word outside PSL). Use it only where an identifier cannot appear, or
// after looking ahead.
func (p *Parser) atKw(k token.Kind) bool { return p.nthIsKw(0, k) }

// nthIsKw is atKw for the nth visible token.
func (p *Parser) nthIsKw(n int, k token.Kind) bool {
	r := p.peek(n)
	if r == nil {
		return false
	}
	return r.Kind == k || (r.Kind == token.Identifier && r.Word == k)
}

// bump consumes the current token as it was lexed. At the end of the
// input it does nothing.
func (p *Parser) bump() { p.bumpAs(0) }

// bumpAs consumes the current token with kind k (0: as lexed).
func (p *Parser) bumpAs(k token.Kind) {
	if p.dead {
		return
	}
	i := p.nthIdx(0)
	if i < 0 || p.buf[i].Kind == token.EOF {
		return
	}
	p.flushInactive(i)
	p.emit(i, k)
	p.pos = i + 1
	p.fuel = 0
	p.compact()
}

// eat consumes the current token if it has kind k.
func (p *Parser) eat(k token.Kind) bool {
	if p.at(k) {
		p.bump()
		return true
	}
	return false
}

// expect consumes a token of kind k, or reports "expected k" and
// inserts a missing k.
func (p *Parser) expect(k token.Kind) bool {
	if p.eat(k) {
		return true
	}
	p.errorExpected(describeKind(k))
	p.missing(k)
	return false
}

// bumpKw consumes the current token as keyword k. If it was lexed as an
// identifier because the selected standard predates k, it reports
// syntax/standard (#31).
func (p *Parser) bumpKw(k token.Kind) {
	r := p.peek(0)
	if r == nil {
		return
	}
	if r.Kind == token.Identifier && r.Word == k {
		if kw, ok := k.Keyword(); ok && kw.Gated(p.std()) {
			p.problemAt(r.Start, r.End, diag.SyntaxStandard,
				"the reserved word "+kw.Word, kw.First.String(),
				p.std().String())
		}
	}
	p.bumpAs(k)
}

// eatKw consumes keyword k (see atKw) if present.
func (p *Parser) eatKw(k token.Kind) bool {
	if p.atKw(k) {
		p.bumpKw(k)
		return true
	}
	return false
}

// expectKw consumes keyword k or reports it missing.
func (p *Parser) expectKw(k token.Kind) bool {
	if p.eatKw(k) {
		return true
	}
	p.errorExpected(describeKind(k))
	p.missing(k)
	return false
}

// atIdent reports whether the current token can be consumed by ident: an
// identifier or extended identifier, a PSL word used as a name inside
// PSL, or (with psl.strict_keywords) a PSL-only 2008 word.
func (p *Parser) atIdent() bool { return p.nthIsIdent(0) }

// nthIsIdent is atIdent for the nth visible token.
func (p *Parser) nthIsIdent(n int) bool {
	r := p.peek(n)
	if r == nil {
		return false
	}
	switch r.Kind {
	case token.Identifier, token.ExtendedIdentifier:
		return true
	}
	kw, ok := r.Kind.Keyword()
	if !ok {
		return false
	}
	if p.mode == lexer.PSL || r.Flags&lexer.PSLComment != 0 {
		return kw.Class == token.ClassPSL || kw.Class == token.ClassPSL2008
	}
	return kw.Class == token.ClassPSL2008
}

// ident consumes an identifier, reporting false (and inserting a missing
// Identifier) when there is none. It applies the PSL reservation rules
// (#6, #11 entry 3): outside PSL, a PSL-only 2008 word gives the
// portability hint syntax/psl-identifier, or with psl.strict_keywords
// the error syntax/psl-keyword; the token becomes an Identifier.
func (p *Parser) ident() bool {
	if p.identOpt() {
		return true
	}
	p.errorExpected("an identifier")
	p.missing(token.Identifier)
	return false
}

// identOpt is ident without the error: it consumes an identifier if
// there is one.
func (p *Parser) identOpt() bool {
	r := p.peek(0)
	if r == nil || !p.atIdent() {
		return false
	}
	inPSL := p.mode == lexer.PSL || r.Flags&lexer.PSLComment != 0
	switch r.Kind {
	case token.ExtendedIdentifier:
		p.bump()
		return true
	case token.Identifier:
		if !inPSL && p.std() >= token.Std2008 {
			if kw, ok := r.Word.Keyword(); ok &&
				kw.Class == token.ClassPSL2008 {
				p.problemAt(r.Start, r.End, diag.SyntaxPSLIdentifier,
					kw.Word)
			}
		}
		p.bump()
		return true
	}
	if kw, ok := r.Kind.Keyword(); ok && kw.Class == token.ClassPSL2008 &&
		!inPSL {
		p.problemAt(r.Start, r.End, diag.SyntaxPSLKeyword, kw.Word)
	}
	p.bumpAs(token.Identifier)
	return true
}

// missing inserts a zero-width token of kind k flagged missing.
func (p *Parser) missing(k token.Kind) {
	f := cst.FlagMissing
	if p.sub != 0 {
		f |= cst.FlagInactive
	}
	p.b.Token(k, "", nil, nil, f)
}

// open starts a node of kind k.
func (p *Parser) open(k cst.Kind) { p.b.StartNode(k) }

// close finishes the innermost open node.
func (p *Parser) close() { p.b.FinishNode() }

// mark returns a checkpoint for openAt.
func (p *Parser) mark() cst.Checkpoint { return p.b.Checkpoint() }

// openAt starts a node of kind k whose first children are those added
// since m.
func (p *Parser) openAt(m cst.Checkpoint, k cst.Kind) {
	p.b.StartNodeAt(m, k)
}

// enter increases the nesting depth; it returns false (after reporting
// once) when the input nests too deeply. Callers that get false must not
// recurse; call leave either way.
func (p *Parser) enter() bool {
	p.depth++
	if p.depth <= maxDepth {
		return true
	}
	if !p.reportedDeep {
		p.reportedDeep = true
		s := p.peekStart()
		p.problemAt(s, s, diag.SyntaxError, "constructs are nested too "+
			"deeply (more than "+itoa(maxDepth)+" levels)")
	}
	return false
}

// leave undoes enter.
func (p *Parser) leave() { p.depth-- }

// tooDeep consumes the rest of the input (or of the inactive branch)
// into an ErrorNode; callers use it when enter returns false:
//
//	if !p.enter() {
//		p.tooDeep()
//		p.leave()
//		return true
//	}
//	defer p.leave()
func (p *Parser) tooDeep() {
	p.open(cst.KindErrorNode)
	for !p.atEOF() {
		p.bump()
	}
	p.close()
	if p.sub == 0 {
		p.cut = true
	}
}

// recover skips tokens into an ErrorNode until the current token is in
// stop (not consumed) or the input ends. It always consumes at least one
// token unless already at stop or at the end.
func (p *Parser) recover(stop token.Set) {
	if p.atEOF() || p.atAny(stop) {
		return
	}
	p.open(cst.KindErrorNode)
	for !p.atEOF() && !p.atAny(stop) {
		p.bump()
	}
	p.close()
}

// skipOne consumes the current token into an ErrorNode after reporting
// msg at it (nothing at the end of the input).
func (p *Parser) skipOne(msg string) {
	if p.atEOF() {
		return
	}
	p.error(msg)
	p.open(cst.KindErrorNode)
	p.bump()
	p.close()
}

// skipBalanced consumes tokens into a node of kind k until a token in
// stop at parenthesis depth 0 (not consumed) or the end. Stubs use it.
func (p *Parser) skipBalanced(k cst.Kind, stop token.Set) {
	p.open(k)
	depth := 0
	for !p.atEOF() {
		t := p.nth(0)
		if depth == 0 && stop.Has(t) {
			break
		}
		switch t {
		case token.LParen:
			depth++
		case token.RParen:
			if depth == 0 {
				p.close()
				return
			}
			depth--
		}
		p.bump()
	}
	p.close()
}

// error reports syntax/error msg at the current token (at the end of the
// last token when the input is exhausted).
func (p *Parser) error(msg string) {
	if r := p.peek(0); r != nil && r.Kind != token.EOF {
		p.problemAt(r.Start, r.End, diag.SyntaxError, msg)
		return
	}
	end := p.lastEnd
	if r := p.peek(0); r != nil {
		end = r.Start
	}
	p.problemAt(end, end, diag.SyntaxError, msg)
}

// errorExpected reports "expected <what>, found <current>" right after
// the last consumed token.
func (p *Parser) errorExpected(what string) {
	found := "end of file"
	if r := p.peek(0); r != nil && r.Kind != token.EOF {
		found = describe(r.Kind, p.text[r.Start:r.End])
	}
	p.problemAt(p.lastEnd, p.lastEnd, diag.SyntaxError,
		"expected "+what+", found "+found)
}

// gate reports syntax/standard for construct (first standard first) on
// the current token when the selected standard is older. Parser-gated
// rules of rules.csv go through here.
func (p *Parser) gate(construct string, first token.Standard,
	start, end int,
) {
	if p.std() < first {
		p.problemAt(start, end, diag.SyntaxStandard, construct,
			first.String(), p.std().String())
	}
}

// curSpan returns the text range of the current token (empty at the
// end).
func (p *Parser) curSpan() (start, end int) {
	if r := p.peek(0); r != nil && r.Kind != token.EOF {
		return r.Start, r.End
	}
	return p.lastEnd, p.lastEnd
}

func (p *Parser) problemAt(start, end int, code diag.Code,
	args ...string,
) {
	if p.cut && code == diag.SyntaxError {
		return
	}
	p.addProblem(Problem{Start: start, End: end, Code: code,
		Args: args, Inactive: p.sub != 0})
}

// addProblem records pr, or counts it as dropped once MaxProblems
// problems are recorded.
func (p *Parser) addProblem(pr Problem) {
	if len(p.probs) >= MaxProblems {
		if p.dropped == 0 {
			p.firstDropped = pr.Start
		}
		p.dropped++
		return
	}
	p.probs = append(p.probs, pr)
}

// nprob is the number of problems reported so far, dropped ones
// included.
func (p *Parser) nprob() int { return len(p.probs) + p.dropped }

// emit adds buffered token i to the tree, kind overridden by k (if
// non-zero), and records its lexical problems.
func (p *Parser) emit(i int, k token.Kind) {
	r := &p.buf[i]
	if k == 0 {
		k = r.Kind
	}
	p.lead = p.lead[:0]
	for _, t := range p.arena[r.tlo:r.tmid] {
		p.lead = append(p.lead, cst.Trivia{Kind: t.Kind,
			Text: p.text[t.Start:t.End]})
	}
	p.trail = p.trail[:0]
	for _, t := range p.arena[r.tmid:r.thi] {
		p.trail = append(p.trail, cst.Trivia{Kind: t.Kind,
			Text: p.text[t.Start:t.End]})
	}
	var f cst.Flags
	if r.Flags&lexer.Inactive != 0 {
		f |= cst.FlagInactive
	}
	if r.Flags&lexer.PSLComment != 0 {
		f |= cst.FlagPSLComment
	}
	p.b.Token(k, p.text[r.Start:r.End], p.lead, p.trail, f)
	p.emitted++
	for _, pr := range p.parena[r.plo:r.phi] {
		p.addProblem(Problem{Start: pr.Start, End: pr.End,
			Code: pr.Code, Args: pr.Args, Inactive: r.Region != 0})
	}
	if r.Kind != token.EOF {
		p.lastEnd = r.End
	}
}

// flushInactive emits the raw tokens of inactive regions buffered before
// index i (in the main parse) as flat InactiveBranch nodes, one per
// region. Branches at list heads are parsed by p.inactive instead.
func (p *Parser) flushInactive(i int) {
	for p.pos < i {
		region := p.buf[p.pos].Region
		p.b.StartNode(cst.KindInactiveBranch)
		for p.pos < i && p.buf[p.pos].Region == region {
			p.emit(p.pos, 0)
			p.pos++
		}
		p.b.FinishNode()
	}
}

// inactive parses the inactive conditional-analysis branch that starts
// at the current position, if any, into an InactiveBranch node by
// calling item until the branch is exhausted (#10). item follows the
// parseX convention (false: nothing consumed); tokens no item accepts
// are skipped with an error. Problems inside are reported as inactive.
// It returns false, doing nothing, when no inactive branch starts here.
func (p *Parser) inactive(item func() bool) bool {
	if p.sub != 0 || p.dead {
		return false
	}
	r := p.rawAt(p.pos)
	if r.Region == 0 || r.Kind == token.EOF {
		return false
	}
	p.sub = r.Region
	p.b.StartNode(cst.KindInactiveBranch)
	for !p.atEOF() {
		before := p.emitted
		if !item() && p.emitted == before {
			p.skipOne("unexpected " + p.describeCurrent())
		}
	}
	p.b.FinishNode()
	p.sub = 0
	return true
}

func (p *Parser) describeCurrent() string {
	if r := p.peek(0); r != nil && r.Kind != token.EOF {
		return describe(r.Kind, p.text[r.Start:r.End])
	}
	return "end of file"
}

// finish ends the parse: after a budget stop it puts every remaining
// token into an ErrorNode, then it emits the EOF token.
func (p *Parser) finish() {
	if p.dead {
		p.open(cst.KindErrorNode)
		for {
			p.fill(1)
			r := &p.buf[p.pos]
			if r.Kind == token.EOF {
				break
			}
			p.emit(p.pos, 0)
			p.pos++
		}
		p.close()
	}
	p.sub = 0
	i := p.nthIdx(0)
	for p.buf[i].Kind != token.EOF {
		// Only reachable if grammar code stopped early: keep the text.
		p.open(cst.KindErrorNode)
		for p.buf[i].Kind != token.EOF {
			p.flushInactive(i)
			p.emit(i, 0)
			p.pos = i + 1
			i = p.nthIdx(0)
		}
		p.close()
	}
	p.flushInactive(i)
	p.emit(i, 0)
	p.pos = i + 1
}

// describe returns a short description of a token for messages.
func describe(k token.Kind, text string) string {
	switch {
	case k == token.Identifier || k == token.ExtendedIdentifier:
		return "identifier " + text
	case k.IsKeyword():
		return "reserved word " + k.Text()
	case k == token.IntegerLiteral || k == token.RealLiteral:
		return "literal " + text
	case k == token.StringLiteral || k == token.BitStringLiteral ||
		k == token.CharacterLiteral:
		return "literal " + text
	case k == token.EOF:
		return "end of file"
	case k == token.Error:
		return "invalid text"
	}
	return "'" + k.Text() + "'"
}

// describeKind describes an expected token kind.
func describeKind(k token.Kind) string {
	switch k {
	case token.Identifier:
		return "an identifier"
	case token.StringLiteral:
		return "a string literal"
	case token.CharacterLiteral:
		return "a character literal"
	case token.IntegerLiteral:
		return "an integer literal"
	}
	if k.IsKeyword() {
		return "reserved word " + k.Text()
	}
	if t := k.Text(); t != "" {
		return "'" + t + "'"
	}
	return k.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
