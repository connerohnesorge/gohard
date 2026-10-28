package parser

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Concurrent statements (builder: conc). The shared pieces (labels,
// targets, waveforms, choices, when/else chains) are in seq.go.

// concFirst is the set of tokens that can start a concurrent statement
// (besides identifiers); the list loops resynchronize on it.
var concFirst = token.NewSet(token.KwProcess, token.KwPostponed,
	token.KwBlock, token.KwAssert, token.KwWith, token.KwFor, token.KwIf,
	token.KwCase, token.KwCover, token.KwDefault, token.KwProperty,
	token.KwSequence, token.KwAssume, token.KwAssumeGuarantee,
	token.KwRestrict, token.KwRestrictGuarantee, token.KwFairness,
	token.KwStrong, token.KwComponent, token.KwEntity,
	token.KwConfiguration)

// Stop sets of generate bodies.
var (
	ifGenStop   = token.NewSet(token.KwElsif, token.KwElse, token.KwEnd)
	caseGenStop = whenStop
)

// pslTemporal are the words that make a concurrent assert a PSL
// property rather than a plain Boolean (#6): the temporal operators. The
// PSL built-in functions (prev, rose, ...) are not in it; a plain
// Boolean may call functions.
var pslTemporal = token.NewSet(token.KwAlways, token.KwNever,
	token.KwEventually, token.KwNext, token.KwNextA, token.KwNextE,
	token.KwNextEvent, token.KwNextEventA, token.KwNextEventE,
	token.KwUntil, token.KwBefore, token.KwWithin, token.KwAbort,
	token.KwAsyncAbort, token.KwSyncAbort, token.KwForall,
	token.At, token.LBrace, token.LBracket)

// maxAssertScan bounds the look-ahead of the PSL assert decision, so a
// file of asserts without `;` stays linear.
const maxAssertScan = 1024

// parseConcurrentStatement parses one concurrent statement (optionally
// labelled), including PSL directives and declarations through
// parsePSLDirective/parsePSLDeclaration, and returns false, consuming
// nothing, when the current token cannot start one. Nested statement
// parts go through p.statementPart(stop).
func (p *Parser) parseConcurrentStatement() bool {
	if !p.atAny(concFirst) && !p.atIdent() && !p.at(token.LParen) &&
		!p.at(token.LtLt) {
		return false
	}
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return true
	}
	defer p.leave()
	before := p.lastEnd // p.pos moves back on compaction
	off := 0
	if p.atLabel() {
		off = 2
	}
	switch {
	case p.pslTriggerAt(off):
		p.pslItem(off)
	case p.nthIsKw(off, token.KwAssert) && p.assertIsPSL(off):
		if !p.parsePSLDirective() {
			p.pslFallback()
		}
	default:
		p.concStmt(off == 2)
	}
	if p.lastEnd == before && !p.atEOF() {
		p.skipOne("expected a concurrent statement, found " +
			p.describeCurrent())
	}
	return true
}

// concStmt parses a VHDL concurrent statement.
func (p *Parser) concStmt(labelled bool) {
	m := p.mark()
	if labelled {
		p.label()
	}
	postponed := p.eatKw(token.KwPostponed)
	switch {
	case p.atKw(token.KwProcess):
		p.processStmt(m)
	case p.atKw(token.KwAssert):
		p.assertStmt(m, cst.KindConcurrentAssertionStmt)
	case p.atKw(token.KwWith):
		p.selectedAssign(m, true)
	case postponed:
		p.concNameStmt(m, false)
	case p.atKw(token.KwBlock):
		p.blockStmt(m)
	case p.atKw(token.KwFor):
		p.forGenerate(m)
	case p.atKw(token.KwIf):
		p.ifGenerate(m)
	case p.atKw(token.KwCase):
		p.caseGenerate(m)
	case p.atKw(token.KwComponent), p.atKw(token.KwEntity),
		p.atKw(token.KwConfiguration):
		if !labelled {
			p.error("an instantiation needs a label")
		}
		p.open(cst.KindInstantiatedUnit)
		isEntity := p.atKw(token.KwEntity)
		p.bump()
		p.parseSelectedName()
		if isEntity && p.at(token.LParen) {
			p.bump()
			p.ident()
			p.expect(token.RParen)
		}
		p.close()
		p.instantiationRest(m)
	default:
		p.concNameStmt(m, labelled)
	}
}

// concNameStmt parses a concurrent statement that starts with a name or
// aggregate: a signal assignment, a procedure call or (labelled) a
// component instantiation `l : name [generic map] [port map];`.
func (p *Parser) concNameStmt(m cst.Checkpoint, labelled bool) {
	plain := labelled && p.plainNameThenSemi()
	nm := p.mark()
	if !p.target() {
		p.errorExpected("a concurrent statement")
		p.openAt(m, cst.KindErrorNode)
		p.recover(stmtSync)
		p.eat(token.Semicolon)
		p.close()
		return
	}
	if p.atCallMaps() && (!labelled || p.atKw(token.KwParameter)) {
		// A 2019 call with maps; labelled `generic map` instantiates.
		p.exCallMaps(nm)
	}
	switch {
	case p.at(token.Le):
		p.wrapTarget(nm)
		p.signalAssign(m, true)
	case p.atKw(token.KwGeneric) || p.atKw(token.KwPort) || plain:
		if !labelled {
			p.error("an instantiation needs a label")
		}
		p.openAt(nm, cst.KindInstantiatedUnit)
		p.close()
		p.instantiationRest(m)
	default:
		p.openAt(m, cst.KindConcurrentProcedureCallStmt)
		p.stmtEndExpected("'<=', ';' or a generic or port map")
		p.close()
	}
}

// plainNameThenSemi reports whether `name { . name } ;` follows: after a
// label that is a component instantiation without maps, not a call.
func (p *Parser) plainNameThenSemi() bool {
	i := 0
	for range 32 {
		if !p.nthIsIdent(i) {
			return false
		}
		switch p.nth(i + 1) {
		case token.Semicolon:
			return true
		case token.Dot:
			i += 2
		default:
			return false
		}
	}
	return false
}

// instantiationRest parses `[generic map] [port map] ;` of a component
// instantiation whose label and unit were built since m.
func (p *Parser) instantiationRest(m cst.Checkpoint) {
	p.openAt(m, cst.KindComponentInstantiationStmt)
	if p.atKw(token.KwGeneric) {
		p.parseGenericMapAspect()
	}
	if p.atKw(token.KwPort) {
		p.parsePortMapAspect()
	}
	p.stmtEnd()
	p.close()
}

// processStmt parses `process [(sensitivity)] [is] decls begin stmts
// end [postponed] process [label];` (postponed already consumed).
func (p *Parser) processStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindProcessStmt)
	p.bumpKw(token.KwProcess)
	if p.at(token.LParen) {
		p.open(cst.KindProcessSensitivityList)
		p.bump()
		if p.atKw(token.KwAll) {
			s, e := p.curSpan()
			p.gateRule("process_all", s, e)
			p.bumpKw(token.KwAll)
		} else {
			p.nameList()
		}
		p.expect(token.RParen)
		p.close()
	}
	p.eatKw(token.KwIs)
	p.declarativePart(RegionProcess, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.sequenceOfStatements(endOnly)
	if p.atKw(token.KwEnd) && (p.nthIsKw(1, token.KwPostponed) ||
		p.nthIsKw(1, token.KwProcess)) {
		p.open(cst.KindEndClause)
		p.bumpKw(token.KwEnd)
		p.eatKw(token.KwPostponed)
		p.expectKw(token.KwProcess)
		p.identOpt()
		p.expect(token.Semicolon)
		p.close()
	} else {
		p.endClauseReq(token.KwProcess)
	}
	p.close()
}

// blockStmt parses `block [(guard)] [is] [header] decls begin stmts
// end block [label];`.
func (p *Parser) blockStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindBlockStmt)
	p.bumpKw(token.KwBlock)
	if p.eat(token.LParen) {
		p.condition()
		p.expect(token.RParen)
	}
	p.eatKw(token.KwIs)
	if p.atKw(token.KwGeneric) || p.atKw(token.KwPort) {
		p.open(cst.KindBlockHeader)
		if p.atKw(token.KwGeneric) && !p.nthIsKw(1, token.KwMap) {
			p.parseGenericClause()
		}
		if p.atKw(token.KwGeneric) && p.nthIsKw(1, token.KwMap) {
			p.parseGenericMapAspect()
			p.expect(token.Semicolon)
		}
		if p.atKw(token.KwPort) && !p.nthIsKw(1, token.KwMap) {
			p.parsePortClause()
		}
		if p.atKw(token.KwPort) && p.nthIsKw(1, token.KwMap) {
			p.parsePortMapAspect()
			p.expect(token.Semicolon)
		}
		p.close()
	}
	p.declarativePart(RegionBlock, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.statementPart(endOnly)
	p.endClauseReq(token.KwBlock)
	p.close()
}

// ---------------------------------------------------------- generate

// gateGen reports the 2008 generate extensions (elsif, else, case
// generate, alternative labels, body end clauses) at the current token.
func (p *Parser) gateGen() {
	s, e := p.curSpan()
	p.gateRule("gen_elsif_else_case", s, e)
}

// altLabel parses an optional alternative label `alt :`.
func (p *Parser) altLabel() {
	if p.atLabel() {
		p.gateGen()
		p.label()
	}
}

// generateBody parses `[decls begin] stmts [end [alt_label];]` into a
// GenerateBody node; stop ends the statements (the `end` of the body's
// own end clause is told apart from `end generate`).
func (p *Parser) generateBody(stop token.Set) {
	p.open(cst.KindGenerateBody)
	if p.atKw(token.KwBegin) || p.atAny(declFirst) {
		p.declarativePart(RegionGenerate, beginOrEnd)
		p.expectKw(token.KwBegin)
	}
	p.statementPart(stop)
	if p.atKw(token.KwEnd) && !p.nthIsKw(1, token.KwGenerate) {
		p.gateGen()
		p.endClause(false)
	}
	p.close()
}

// forGenerate parses `for param generate body end generate [label];`.
func (p *Parser) forGenerate(m cst.Checkpoint) {
	p.openAt(m, cst.KindForGenerateStmt)
	p.bumpKw(token.KwFor)
	p.parameterSpec()
	p.expectKw(token.KwGenerate)
	p.generateBody(endOnly)
	p.endClauseReq(token.KwGenerate)
	p.close()
}

// ifGenerate parses `if [alt:] cond generate body {elsif [alt:] cond
// generate body} [else [alt:] generate body] end generate [label];`.
func (p *Parser) ifGenerate(m cst.Checkpoint) {
	p.openAt(m, cst.KindIfGenerateStmt)
	p.open(cst.KindGenerateBranch)
	p.bumpKw(token.KwIf)
	p.altLabel()
	p.condition()
	p.expectKw(token.KwGenerate)
	p.generateBody(ifGenStop)
	p.close()
	for p.atKw(token.KwElsif) {
		p.open(cst.KindGenerateBranch)
		p.gateGen()
		p.bumpKw(token.KwElsif)
		p.altLabel()
		p.condition()
		p.expectKw(token.KwGenerate)
		p.generateBody(ifGenStop)
		p.close()
	}
	if p.atKw(token.KwElse) {
		p.open(cst.KindGenerateBranch)
		p.gateGen()
		p.bumpKw(token.KwElse)
		p.altLabel()
		p.expectKw(token.KwGenerate)
		p.generateBody(ifGenStop)
		p.close()
	}
	p.endClauseReq(token.KwGenerate)
	p.close()
}

// caseGenerate parses `case expr generate {when [alt:] choices => body}
// end generate [label];` (2008).
func (p *Parser) caseGenerate(m cst.Checkpoint) {
	p.openAt(m, cst.KindCaseGenerateStmt)
	p.gateGen()
	p.bumpKw(token.KwCase)
	p.expr("a case expression")
	p.expectKw(token.KwGenerate)
	for {
		if p.inactive(p.caseGenerateAlternative) {
			continue
		}
		if p.atEOF() || p.atKw(token.KwEnd) {
			break
		}
		if !p.caseGenerateAlternative() {
			p.error("expected when, found " + p.describeCurrent())
			p.recoverItem(whenStop)
		}
	}
	p.endClauseReq(token.KwGenerate)
	p.close()
}

// caseGenerateAlternative parses `when [alt:] choices => body`.
func (p *Parser) caseGenerateAlternative() bool {
	if !p.atKw(token.KwWhen) {
		return false
	}
	p.open(cst.KindCaseGenerateAlternative)
	p.bumpKw(token.KwWhen)
	p.altLabel()
	p.choices()
	p.expect(token.Arrow)
	p.generateBody(caseGenStop)
	p.close()
	return true
}

// --------------------------------------------------------------- PSL

// pslTriggerAt is atPSLTrigger for the visible token n (0, or 2 after a
// label). Only directives can be labelled.
func (p *Parser) pslTriggerAt(n int) bool {
	if n == 0 {
		return p.atPSLTrigger()
	}
	r := p.peek(n)
	if r == nil {
		return false
	}
	if p.std() < token.Std2008 && r.Flags&lexer.PSLComment == 0 &&
		p.mode != lexer.PSL {
		return false
	}
	for _, k := range []token.Kind{token.KwCover, token.KwAssume,
		token.KwAssumeGuarantee, token.KwRestrict,
		token.KwRestrictGuarantee, token.KwFairness, token.KwStrong} {
		if p.nthIsKw(n, k) {
			switch p.nth(n + 1) {
			case token.Colon, token.Le, token.VarAssign, token.Semicolon,
				token.Dot, token.KwPort, token.KwGeneric:
				return false
			}
			return true
		}
	}
	return false
}

// pslItem hands a PSL declaration or directive at visible token off to
// psl.go; the label, if any, is left for the directive parser.
func (p *Parser) pslItem(off int) {
	var ok bool
	if off == 0 && (p.atKw(token.KwDefault) || p.atKw(token.KwProperty) ||
		p.atKw(token.KwSequence)) {
		ok = p.parsePSLDeclaration()
	} else {
		ok = p.parsePSLDirective()
	}
	if !ok {
		p.pslFallback()
	}
}

// pslFallback skips a PSL item the PSL parser did not accept, up to and
// including its `;`, in PSL mode, into an ErrorNode.
func (p *Parser) pslFallback() {
	old := p.mode
	p.setMode(lexer.PSL)
	p.error("expected a PSL directive or declaration, found " +
		p.describeCurrent())
	p.open(cst.KindErrorNode)
	depth := 0
	for !p.atEOF() {
		switch p.nth(0) {
		case token.LParen, token.LBrace, token.LBracket:
			depth++
		case token.RParen, token.RBrace, token.RBracket:
			depth--
		case token.Semicolon:
			if depth <= 0 {
				p.bump()
				p.close()
				p.setMode(old)
				return
			}
		case token.KwEnd, token.KwBegin:
			if depth <= 0 {
				p.close()
				p.setMode(old)
				return
			}
		}
		p.bump()
	}
	p.close()
	p.setMode(old)
}

// assertIsPSL decides whether the concurrent assert at visible token n
// is a PSL assert directive (#6, #31): always inside a `-- psl` comment
// or PSL code; under 2008 or later when its property uses PSL operators
// (a PSL-only token, a temporal word, a clock `@`, braces or brackets)
// before the `;` or the report/severity part; otherwise it is an
// ordinary VHDL concurrent assertion. The look-ahead is lexed in PSL
// mode and then dropped, so the lexer state is unchanged.
func (p *Parser) assertIsPSL(n int) bool {
	r := p.peek(n)
	if r == nil {
		return false
	}
	if r.Flags&lexer.PSLComment != 0 || p.mode == lexer.PSL {
		return true
	}
	if p.std() < token.Std2008 {
		return false
	}
	p.setMode(lexer.PSL)
	defer p.setMode(lexer.VHDL)
	i := p.nthIdx(n + 1)
	if i < 0 {
		return false
	}
	if p.pslInstanceAt(i) {
		return true // assert p(a, b) or (p) of a declared property
	}
	depth := 0
	for steps := 0; steps < maxAssertScan; steps++ {
		r := p.rawAt(i)
		if r.Kind == token.EOF || (p.sub != 0 && r.Region != p.sub) {
			return false
		}
		if p.sub == 0 && r.Region != 0 {
			i = p.skipRegion(i)
			continue
		}
		k := r.Kind
		switch {
		case k.IsPSLOnly() || pslTemporal.Has(k):
			return true
		case k == token.LParen:
			depth++
		case k == token.RParen:
			depth--
		case k == token.KwEnd || k == token.KwBegin || k == token.KwAssert ||
			k == token.KwProcess:
			return false
		case depth <= 0 && (k == token.Semicolon || k == token.KwReport ||
			k == token.KwSeverity):
			return false
		}
		i++
	}
	return false
}
