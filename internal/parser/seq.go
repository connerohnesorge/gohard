package parser

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Sequential statements (builder: seq), plus the pieces the concurrent
// statements share with them: labels, targets, waveforms, delay
// mechanisms and when/else chains. Choices, names and aggregates come
// from expr.go (parseChoices, parseNameOrAggregate); parseExpression
// never consumes `when`, so the when/else forms are built here.
//
// Statement nodes whose kind depends on what follows the target (an
// assignment, a call, a conditional or selected form) are opened late
// with openAt on a checkpoint taken before the label, so the label and
// target always end up inside the statement node.

// seqFirst is the set of tokens that can start a sequential statement
// (besides identifiers); the list loops resynchronize on it.
var seqFirst = token.NewSet(token.KwWait, token.KwAssert,
	token.KwReport, token.KwIf, token.KwCase, token.KwLoop,
	token.KwWhile, token.KwFor, token.KwNext, token.KwExit,
	token.KwReturn, token.KwNull, token.KwWith, token.KwBlock)

// Stop sets of the nested statement lists.
var (
	ifStop   = token.NewSet(token.KwElsif, token.KwElse, token.KwEnd)
	whenStop = token.NewSet(token.KwWhen, token.KwEnd)
	// stmtSync ends the skip after a statement that lacks its `;`.
	stmtSync = token.NewSet(token.Semicolon, token.KwEnd, token.KwBegin)
)

// parseSequentialStatement parses one sequential statement (optionally
// labelled) and returns false, consuming nothing, when the current token
// cannot start one. Nested statement lists go through
// p.sequenceOfStatements(stop), which handles inactive branches and
// recovery.
func (p *Parser) parseSequentialStatement() bool {
	if !p.atAny(seqFirst) && !p.atIdent() && !p.at(token.LParen) &&
		!p.at(token.LtLt) && !p.atOperatorPrefix() {
		return false
	}
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return true
	}
	defer p.leave()
	before := p.lastEnd // p.pos moves back on compaction
	m := p.mark()
	if p.atLabel() {
		p.label()
	}
	switch {
	case p.atKw(token.KwWait):
		p.waitStmt(m)
	case p.atKw(token.KwAssert):
		p.assertStmt(m, cst.KindAssertStmt)
	case p.atKw(token.KwReport):
		p.reportStmt(m)
	case p.atKw(token.KwIf):
		p.ifStmt(m)
	case p.atKw(token.KwCase):
		p.caseStmt(m)
	case p.atKw(token.KwLoop), p.atKw(token.KwWhile),
		p.atKw(token.KwFor):
		p.loopStmt(m)
	case p.atKw(token.KwNext):
		p.nextExitStmt(m, cst.KindNextStmt, token.KwNext)
	case p.atKw(token.KwExit):
		p.nextExitStmt(m, cst.KindExitStmt, token.KwExit)
	case p.atKw(token.KwReturn):
		p.returnStmt(m)
	case p.atKw(token.KwNull):
		p.openAt(m, cst.KindNullStmt)
		p.bumpKw(token.KwNull)
		p.stmtEnd()
		p.close()
	case p.atKw(token.KwWith):
		p.selectedAssign(m, false)
	case p.atKw(token.KwBlock):
		p.seqBlockStmt(m)
	default:
		p.seqNameStmt(m)
	}
	if p.lastEnd == before && !p.atEOF() {
		p.skipOne("expected a sequential statement, found " +
			p.describeCurrent())
	}
	return true
}

// ------------------------------------------------------------ shared

// atOperatorPrefix reports whether an operator symbol used as the
// prefix of a selected name ("-".v, a function call's expanded name)
// starts here.
func (p *Parser) atOperatorPrefix() bool {
	return p.at(token.StringLiteral) && p.nth(1) == token.Dot
}

// atLabel reports whether `identifier :` starts here.
func (p *Parser) atLabel() bool {
	return p.atIdent() && p.nth(1) == token.Colon
}

// label parses `identifier :` into a Label node.
func (p *Parser) label() {
	p.open(cst.KindLabel)
	p.ident()
	p.expect(token.Colon)
	p.close()
}

// stmtEnd consumes the `;` that ends a statement. When it is missing it
// reports the error and skips to the next `;` (consumed) or to a token
// that ends a statement list.
func (p *Parser) stmtEnd() { p.stmtEndExpected("';'") }

func (p *Parser) stmtEndExpected(what string) {
	if p.eat(token.Semicolon) {
		return
	}
	p.errorExpected(what)
	p.recover(stmtSync)
	if !p.eat(token.Semicolon) {
		p.missing(token.Semicolon)
	}
}

// endClauseReq parses the required `end kws... [label];` of a compound
// statement. When the `end` is missing, or belongs to an enclosing
// construct (`end` followed by another reserved word), it reports the
// error and inserts the missing words without consuming anything, so
// the enclosing construct keeps its own end.
func (p *Parser) endClauseReq(kws ...token.Kind) {
	if p.atKw(token.KwEnd) {
		if r := p.peek(1); r == nil || !r.Kind.IsKeyword() ||
			p.nthIsKw(1, kws[0]) {
			p.endClause(true, kws...)
			return
		}
	}
	p.open(cst.KindEndClause)
	what := "end"
	for _, k := range kws {
		what += " " + k.Text()
	}
	p.errorExpected(what)
	p.missing(token.KwEnd)
	for _, k := range kws {
		p.missing(k)
	}
	p.missing(token.Semicolon)
	p.close()
}

// condition parses a required condition (an expression).
func (p *Parser) condition() {
	if !p.parseExpression() {
		p.errorExpected("a condition")
	}
}

// expr parses a required expression.
func (p *Parser) expr(what string) {
	if !p.parseExpression() {
		p.errorExpected(what)
	}
}

// exprValue is the value parser of conditional and selected
// expressions.
func (p *Parser) exprValue() { p.expr("an expression") }

// optExprValue is exprValue for `return [expr] when ...`.
func (p *Parser) optExprValue() { p.parseExpression() }

// target parses the target of an assignment or the name of a call: a
// name or an aggregate (parseNameOrAggregate, which leaves a following
// `<=` alone).
func (p *Parser) target() bool { return p.parseNameOrAggregate() }

// wrapTarget wraps what was parsed since tm into a Target node.
func (p *Parser) wrapTarget(tm cst.Checkpoint) {
	p.openAt(tm, cst.KindTarget)
	p.close()
}

// choices parses required choices (parseChoices, in expr.go).
func (p *Parser) choices() {
	if !p.parseChoices() {
		p.errorExpected("a choice")
	}
}

// waveform parses `element {, element}` or `unaffected` into a Waveform
// node; an element is `value [after time]` (null is a value).
func (p *Parser) waveform() {
	p.open(cst.KindWaveform)
	if p.atKw(token.KwUnaffected) {
		p.open(cst.KindUnaffectedExpr)
		p.bumpKw(token.KwUnaffected)
		p.close()
		p.close()
		return
	}
	for !p.atEOF() {
		p.open(cst.KindWaveformElement)
		p.expr("a waveform element")
		if p.eatKw(token.KwAfter) {
			p.expr("a time expression")
		}
		p.close()
		if !p.eat(token.Comma) {
			break
		}
	}
	p.close()
}

// atDelay reports whether a delay mechanism starts here.
func (p *Parser) atDelay() bool {
	return p.atKw(token.KwTransport) || p.atKw(token.KwReject) ||
		p.atKw(token.KwInertial)
}

// delayMechanism parses `transport | [reject time] inertial`.
func (p *Parser) delayMechanism() {
	p.open(cst.KindDelayMechanism)
	switch {
	case p.eatKw(token.KwTransport):
	case p.eatKw(token.KwReject):
		p.expr("a time expression")
		p.expectKw(token.KwInertial)
	default:
		p.bumpKw(token.KwInertial)
	}
	p.close()
}

// condBranches parses the rest of a when/else chain whose first value
// was parsed since vm (the current token is `when`) into a container
// node of kind k holding WhenBranch nodes; value parses the later
// values. The `else` tokens are children of the container.
func (p *Parser) condBranches(vm cst.Checkpoint, k cst.Kind,
	value func(),
) {
	p.openAt(vm, k)
	p.openAt(vm, cst.KindWhenBranch)
	for {
		p.bumpKw(token.KwWhen)
		p.condition()
		p.close()
		if !p.atKw(token.KwElse) {
			break
		}
		p.bumpKw(token.KwElse)
		p.open(cst.KindWhenBranch)
		value()
		if !p.atKw(token.KwWhen) {
			p.close()
			break
		}
	}
	p.close()
}

// selBranches parses `value when choices {, value when choices}` into a
// container node of kind k holding SelectBranch nodes.
func (p *Parser) selBranches(k cst.Kind, value func()) {
	p.open(k)
	for !p.atEOF() {
		p.open(cst.KindSelectBranch)
		value()
		p.expectKw(token.KwWhen)
		p.choices()
		p.close()
		if !p.eat(token.Comma) {
			break
		}
	}
	p.close()
}

// signalAssign parses a signal assignment from its `<=` to its `;`;
// the label and target were built since m. conc selects the concurrent
// node kinds (and allows guarded); sequential assignments may be force
// or release assignments and conditional ones are gated (cond_seq_sig).
func (p *Parser) signalAssign(m cst.Checkpoint, conc bool) {
	if !conc && p.forceRelease(m) {
		return
	}
	simple, cond := cst.KindSignalAssignStmt,
		cst.KindConditionalSignalAssignStmt
	if conc {
		simple, cond = cst.KindConcurrentSimpleSignalAssign,
			cst.KindConcurrentConditionalSignalAssign
	}
	p.bump() // <=
	if conc {
		p.eatKw(token.KwGuarded)
	}
	if p.atDelay() {
		p.delayMechanism()
	}
	wm := p.mark()
	p.waveform()
	if p.atKw(token.KwWhen) {
		if !conc {
			s, e := p.curSpan()
			p.gateRule("cond_seq_sig", s, e)
		}
		p.openAt(m, cond)
		p.condBranches(wm, cst.KindConditionalWaveforms, p.waveform)
	} else {
		p.openAt(m, simple)
	}
	p.stmtEnd()
	p.close()
}

// forceRelease parses `<= force [in|out] value;` or `<= release
// [in|out];` (sequential, 2008). It returns false, consuming nothing,
// when the word after `<=` is a signal named force or release.
func (p *Parser) forceRelease(m cst.Checkpoint) bool {
	isForce := p.forceAfterLe(token.KwForce)
	isRelease := !isForce && p.forceAfterLe(token.KwRelease)
	if !isForce && !isRelease {
		return false
	}
	kind, kw := cst.KindForceAssignStmt, token.KwForce
	if isRelease {
		kind, kw = cst.KindReleaseAssignStmt, token.KwRelease
	}
	p.openAt(m, kind)
	p.bump() // <=
	// The force/release row of rules.csv is the gate; it replaces the
	// keyword diagnostic so the construct is reported once.
	s, e := p.curSpan()
	p.gateKind(kind, s, e)
	p.bumpAs(kw)
	if p.atKw(token.KwIn) || p.atKw(token.KwOut) {
		p.bump()
	}
	if isForce {
		vm := p.mark()
		p.exprValue()
		if p.atKw(token.KwWhen) {
			p.condBranches(vm, cst.KindConditionalExpressions,
				p.exprValue)
		}
	}
	p.stmtEnd()
	p.close()
	return true
}

// forceAfterLe reports whether force or release (kw) follows the
// current `<=` as the keyword. Before 2008 the word lexes as an
// identifier; it is taken as the keyword (with syntax/standard, #31)
// only where the parse needs the keyword: when what follows cannot
// continue an expression naming a signal called force or release.
// `s <= release;` is legal VHDL-1993 (a signal named release) and
// stays an ordinary assignment.
func (p *Parser) forceAfterLe(kw token.Kind) bool {
	if p.nth(1) == kw {
		return true
	}
	if !p.nthIsKw(1, kw) {
		return false
	}
	switch p.nth(2) {
	case token.KwIn, token.KwOut:
		return true
	case token.Identifier, token.ExtendedIdentifier,
		token.IntegerLiteral, token.RealLiteral, token.CharacterLiteral,
		token.StringLiteral, token.BitStringLiteral:
		return kw == token.KwForce
	}
	return false
}

// variableAssign parses a variable assignment from its `:=` to its `;`.
func (p *Parser) variableAssign(m cst.Checkpoint) {
	p.bump() // :=
	vm := p.mark()
	p.exprValue()
	if p.atKw(token.KwWhen) {
		s, e := p.curSpan()
		p.gateRule("cond_var", s, e)
		p.openAt(m, cst.KindConditionalVariableAssignStmt)
		p.condBranches(vm, cst.KindConditionalExpressions, p.exprValue)
	} else {
		p.openAt(m, cst.KindVariableAssignStmt)
	}
	p.stmtEnd()
	p.close()
}

// selectedAssign parses `with expr select[?] target <= ...;` (signal)
// or, in sequential code, `... target := ...;` (variable).
func (p *Parser) selectedAssign(m cst.Checkpoint, conc bool) {
	ws, we := p.curSpan()
	p.bumpKw(token.KwWith)
	p.expr("a selector expression")
	p.expectKw(token.KwSelect)
	if p.at(token.Question) {
		s, e := p.curSpan()
		p.gateRule("select_match", s, e)
		p.bump()
	}
	tm := p.mark()
	if !p.target() {
		p.errorExpected("a target")
	}
	p.wrapTarget(tm)
	kind := cst.KindConcurrentSelectedSignalAssign
	if !conc {
		kind = cst.KindSelectedSignalAssignStmt
	}
	switch {
	case p.at(token.VarAssign) && !conc:
		p.gateRule("sel_var", ws, we)
		p.openAt(m, cst.KindSelectedVariableAssignStmt)
		p.bump()
		p.selBranches(cst.KindSelectedExpressions, p.exprValue)
	case p.at(token.Le):
		if !conc {
			p.gateRule("seq_selected_sig", ws, we)
		}
		p.openAt(m, kind)
		p.bump()
		if conc {
			p.eatKw(token.KwGuarded)
		}
		if p.atDelay() {
			p.delayMechanism()
		}
		p.selBranches(cst.KindSelectedWaveforms, p.waveform)
	default:
		if conc {
			p.errorExpected("'<='")
		} else {
			p.errorExpected("'<=' or ':='")
		}
		p.openAt(m, kind)
	}
	p.stmtEnd()
	p.close()
}

// ------------------------------------------------------- statements

// seqNameStmt parses a statement that starts with a name or aggregate:
// a signal or variable assignment or a procedure call.
func (p *Parser) seqNameStmt(m cst.Checkpoint) {
	tm := p.mark()
	if !p.target() {
		p.errorExpected("a sequential statement")
		p.openAt(m, cst.KindErrorNode)
		p.recover(stmtSync)
		p.eat(token.Semicolon)
		p.close()
		return
	}
	if p.atCallMaps() {
		p.exCallMaps(tm)
	}
	switch {
	case p.at(token.Le):
		p.wrapTarget(tm)
		p.signalAssign(m, false)
	case p.at(token.VarAssign):
		p.wrapTarget(tm)
		p.variableAssign(m)
	default:
		p.openAt(m, cst.KindProcedureCallStmt)
		p.stmtEndExpected("'<=', ':=' or ';'")
		p.close()
	}
}

// atCallMaps reports whether a VHDL-2019 `generic map` or `parameter
// map` follows the name of a procedure call (parseName leaves them to
// the statement; exCallMaps in expr.go parses them into the call).
func (p *Parser) atCallMaps() bool {
	return (p.atKw(token.KwGeneric) || p.atKw(token.KwParameter)) &&
		p.nthIsKw(1, token.KwMap)
}

// waitStmt parses `wait [on names] [until cond] [for time];`.
func (p *Parser) waitStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindWaitStmt)
	p.bumpKw(token.KwWait)
	if p.atKw(token.KwOn) {
		p.open(cst.KindSensitivityClause)
		p.bumpKw(token.KwOn)
		p.open(cst.KindSensitivityList)
		p.nameList()
		p.close()
		p.close()
	}
	if p.atKw(token.KwUntil) {
		p.open(cst.KindConditionClause)
		p.bumpKw(token.KwUntil)
		p.condition()
		p.close()
	}
	if p.atKw(token.KwFor) {
		p.open(cst.KindTimeoutClause)
		p.bumpKw(token.KwFor)
		p.expr("a time expression")
		p.close()
	}
	p.stmtEnd()
	p.close()
}

// nameList parses `name {, name}` (sensitivity lists).
func (p *Parser) nameList() {
	for !p.atEOF() {
		if !p.parseName() {
			p.errorExpected("a signal name")
			return
		}
		if !p.eat(token.Comma) {
			return
		}
	}
}

// assertStmt parses `assert cond [report expr] [severity expr];` into
// a node of kind k (sequential or concurrent assertion).
func (p *Parser) assertStmt(m cst.Checkpoint, k cst.Kind) {
	p.openAt(m, k)
	p.bumpKw(token.KwAssert)
	p.condition()
	if p.eatKw(token.KwReport) {
		p.expr("a report expression")
	}
	if p.eatKw(token.KwSeverity) {
		p.expr("a severity expression")
	}
	p.stmtEnd()
	p.close()
}

// reportStmt parses `report expr [severity expr];`.
func (p *Parser) reportStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindReportStmt)
	p.bumpKw(token.KwReport)
	p.expr("a report expression")
	if p.eatKw(token.KwSeverity) {
		p.expr("a severity expression")
	}
	p.stmtEnd()
	p.close()
}

// ifStmt parses `if cond then stmts {elsif cond then stmts} [else
// stmts] end if [label];`.
func (p *Parser) ifStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindIfStmt)
	p.bumpKw(token.KwIf)
	p.condition()
	p.expectKw(token.KwThen)
	p.sequenceOfStatements(ifStop)
	for p.atKw(token.KwElsif) {
		p.open(cst.KindElsifBranch)
		p.bumpKw(token.KwElsif)
		p.condition()
		p.expectKw(token.KwThen)
		p.sequenceOfStatements(ifStop)
		p.close()
	}
	if p.atKw(token.KwElse) {
		p.open(cst.KindElseBranch)
		p.bumpKw(token.KwElse)
		p.sequenceOfStatements(ifStop)
		p.close()
	}
	p.endClauseReq(token.KwIf)
	p.close()
}

// caseStmt parses `case[?] expr is {when choices => stmts} end case[?]
// [label];`.
func (p *Parser) caseStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindCaseStmt)
	p.bumpKw(token.KwCase)
	if p.at(token.Question) {
		s, e := p.curSpan()
		p.gateRule("case_match", s, e)
		p.bump()
	}
	p.expr("a case expression")
	p.expectKw(token.KwIs)
	for {
		if p.inactive(p.caseAlternative) {
			continue
		}
		if p.atEOF() || p.atKw(token.KwEnd) {
			break
		}
		if !p.caseAlternative() {
			p.error("expected when, found " + p.describeCurrent())
			p.recoverItem(whenStop)
		}
	}
	if p.atKw(token.KwEnd) && p.nthIsKw(1, token.KwCase) {
		p.open(cst.KindEndClause)
		p.bumpKw(token.KwEnd)
		p.bumpKw(token.KwCase)
		p.eat(token.Question)
		p.identOpt()
		p.expect(token.Semicolon)
		p.close()
	} else {
		p.endClauseReq(token.KwCase)
	}
	p.close()
}

// caseAlternative parses `when choices => stmts`.
func (p *Parser) caseAlternative() bool {
	if !p.atKw(token.KwWhen) {
		return false
	}
	p.open(cst.KindCaseAlternative)
	p.bumpKw(token.KwWhen)
	p.choices()
	p.expect(token.Arrow)
	p.sequenceOfStatements(whenStop)
	p.close()
	return true
}

// loopStmt parses `[while cond | for param] loop stmts end loop
// [label];`.
func (p *Parser) loopStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindLoopStmt)
	switch {
	case p.atKw(token.KwWhile):
		p.open(cst.KindWhileScheme)
		p.bumpKw(token.KwWhile)
		p.condition()
		p.close()
	case p.atKw(token.KwFor):
		p.open(cst.KindForScheme)
		p.bumpKw(token.KwFor)
		p.parameterSpec()
		p.close()
	}
	p.expectKw(token.KwLoop)
	p.sequenceOfStatements(endOnly)
	p.endClauseReq(token.KwLoop)
	p.close()
}

// parameterSpec parses `identifier in discrete_range` (loops and for
// generates).
func (p *Parser) parameterSpec() {
	p.open(cst.KindParameterSpecification)
	p.ident()
	p.expectKw(token.KwIn)
	if !p.parseDiscreteRange() {
		p.errorExpected("a discrete range")
	}
	p.close()
}

// nextExitStmt parses `next|exit [loop_label] [when cond];`.
func (p *Parser) nextExitStmt(m cst.Checkpoint, k cst.Kind,
	kw token.Kind,
) {
	p.openAt(m, k)
	p.bumpKw(kw)
	p.identOpt()
	if p.eatKw(token.KwWhen) {
		p.condition()
	}
	p.stmtEnd()
	p.close()
}

// returnStmt parses `return [expr];` or the 2019 conditional return
// `return [expr] when cond {else [expr] when cond} [else expr];`.
func (p *Parser) returnStmt(m cst.Checkpoint) {
	rs, re := p.curSpan()
	p.bumpKw(token.KwReturn)
	vm := p.mark()
	p.parseExpression()
	if p.atKw(token.KwWhen) {
		p.gateRule("cond_return", rs, re)
		p.openAt(m, cst.KindConditionalReturnStmt)
		p.condBranches(vm, cst.KindConditionalExpressions,
			p.optExprValue)
	} else {
		p.openAt(m, cst.KindReturnStmt)
	}
	p.stmtEnd()
	p.close()
}

// seqBlockStmt parses the 2019 sequential block statement `block [is]
// decls begin stmts end block [label];`.
func (p *Parser) seqBlockStmt(m cst.Checkpoint) {
	p.openAt(m, cst.KindSequentialBlockStmt)
	s, e := p.curSpan()
	p.gateRule("seq_block", s, e)
	p.bumpKw(token.KwBlock)
	p.eatKw(token.KwIs)
	p.declarativePart(RegionSequentialBlock, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.sequenceOfStatements(endOnly)
	p.endClauseReq(token.KwBlock)
	p.close()
}
