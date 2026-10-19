package parser

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Embedded PSL (builder: psl): IEEE 1850 PSL as embedded in VHDL-2008,
// VHDL flavour (#6, #31).
//
// Mode handling (#6): a directive or declaration switches the lexer to
// PSL at its trigger word (after any label) and back to the previous
// mode right after its closing `;`. Inside a verification unit the mode
// is already PSL and stays PSL (the unit shell, parsePSLUnit, owns it).
// Tokens from `-- psl` comment lines are lexed as PSL by the lexer
// itself; the grammar never switches modes for them and treats the
// first token that is not on a PSL comment line as the end of the PSL
// text.
//
// Tree shapes:
//
//   - Every HDL operand used as a PSL Boolean or SERE element is a
//     PSLBoolean node around one Expr node (or a PSLUnion of Expr
//     nodes). Abort conditions, clock expressions, counts, fairness
//     operands and next_event conditions are plain Expr nodes.
//   - Repetitions: PSLRepetition holds [operand] op [PSLCount] `]`; the
//     operand is absent for a bare `[*]`, `[+]` or `[*n]` SERE item. In
//     next forms PSLCount includes the brackets. An `inf` bound is a
//     KwInf token in PSLCount (not an Expr), so the high accessor is nil.
//   - Unary `not`/`!` is a PSLLogicalProp with a single operand (the lhs
//     accessor). The LTL letter forms map onto the named operators: G is
//     PSLAlways, F PSLEventually, X/X! PSLNext, `[p U q]`/`[p W q]`
//     PSLUntil (the operator token tells them apart).
//   - Property and sequence instances whose actuals are all HDL
//     expressions (`p1(a, b)`), and PSL built-in functions used inside
//     an HDL expression, are ordinary VHDL names (CallOrIndexName); sema
//     resolves them. An instance with a PSL actual (`s(4, {a; b})`) is
//     PSLSequenceInstance inside a sequence and PSLPropertyInstance in a
//     property, holding the name and an AssociationList of PSL nodes. A
//     built-in call used directly as a PSL operand (`rose(a) -> b`) is
//     PSLBuiltinCall.
//   - A concurrent `assert` (2008+) whose property is a plain Boolean
//     with no PSL operator becomes an ordinary ConcurrentAssertionStmt
//     lexed as VHDL (#6). The conc builder decides first (assertIsPSL);
//     parsePSLDirective applies the same rule again, so a caller that
//     hands it a plain assert still gets the VHDL node. Inside a
//     verification unit and in a `-- psl` comment an assert is always a
//     PSLAssert.
//
// Operator precedence follows IEEE 1850 (loosest first): `@` on a
// property, always/never/G and forall, -> <->, |-> |=>, until* before*,
// the occurrence operators (next*, X, F, eventually!, next_event*, OBE
// unary), abort forms, FL and/or, not/!, then SEREs. Inside braces: `;`,
// `:`, `|`, `&`/`&&`, within, then repetitions and `@` on an item.
//
// Cooperation with the expression builder (expr.go): in its binary
// operator loop it must call p.pslExprStop() before consuming an
// operator and stop when it returns true, so that in `a and always b`
// the FL `and` is left to the PSL grammar. The call is cheap and always
// false outside PSL.

// Property binding powers (higher binds tighter).
const (
	bpClock = 1 + iota
	bpInvariance
	bpImplication
	bpSuffix
	bpBounding
	bpOccurrence
	bpAbort
	bpLogical
	bpNot
)

// SERE binding powers.
const (
	sbConcat = 1 + iota
	sbFusion
	sbOr
	sbAnd
	sbWithin
)

// pslScanLimit bounds the lookahead scans that classify parentheses and
// built-in calls; pslStmtScanLimit the scan of a concurrent assert.
const (
	pslScanLimit     = 4096
	pslStmtScanLimit = 4096
)

// pslParser is the parser inside one PSL directive or declaration.
type pslParser struct {
	*Parser
	// comment: the PSL text is on `-- psl` comment lines.
	comment bool
}

// pslAt returns the PSL grammar for text starting at the nth visible
// token.
func (p *Parser) pslAt(n int) pslParser {
	r := p.peek(n)
	return pslParser{Parser: p,
		comment: r != nil && r.Flags&lexer.PSLComment != 0}
}

// pslAllowed reports whether PSL may start at the nth visible token:
// under 2008 or later, inside a verification unit, or on a `-- psl`
// comment line.
func (p *Parser) pslAllowed(n int) bool {
	if p.std() >= token.Std2008 || p.mode == lexer.PSL {
		return true
	}
	r := p.peek(n)
	return r != nil && r.Flags&lexer.PSLComment != 0
}

// atPSLTrigger reports whether a PSL directive or declaration starts
// here (#31 transition table): default clock, property, sequence,
// endpoint (inside PSL only), assume, assume_guarantee, restrict,
// restrict_guarantee, cover, fairness, strong fairness and a concurrent
// assert (2008+), each optionally labelled. The conc and decl builders
// call parsePSLDirective / parsePSLDeclaration at the start of every
// item, before parsing a label; both return false, consuming nothing,
// when no PSL starts there. A `postponed assert` and any assert before
// 2008 (outside `-- psl` comments) are left to the conc builder.
func (p *Parser) atPSLTrigger() bool {
	k, _ := p.pslDirectiveAt()
	return k != 0 || p.pslDeclAt() != 0
}

// pslDirectiveAt returns the directive kind that starts here and the
// number of label tokens before its keyword (0 or 2), or 0.
func (p *Parser) pslDirectiveAt() (cst.Kind, int) {
	off := 0
	if p.nthIsIdent(0) && p.nth(1) == token.Colon {
		off = 2
	}
	if !p.pslAllowed(off) {
		return 0, 0
	}
	var kind cst.Kind
	switch {
	case p.nthIsKw(off, token.KwAssert):
		return cst.KindPSLAssert, off
	case p.nthIsKw(off, token.KwStrong):
		if p.nthIsKw(off+1, token.KwFairness) {
			return cst.KindPSLFairness, off
		}
		return 0, 0
	case p.nthIsKw(off, token.KwAssume):
		kind = cst.KindPSLAssume
	case p.nthIsKw(off, token.KwAssumeGuarantee):
		kind = cst.KindPSLAssumeGuarantee
	case p.nthIsKw(off, token.KwRestrict):
		kind = cst.KindPSLRestrict
	case p.nthIsKw(off, token.KwRestrictGuarantee):
		kind = cst.KindPSLRestrictGuarantee
	case p.nthIsKw(off, token.KwCover):
		kind = cst.KindPSLCover
	case p.nthIsKw(off, token.KwFairness):
		kind = cst.KindPSLFairness
	default:
		return 0, 0
	}
	// A PSL-only word may also be a VHDL name outside PSL: `assume <=
	// x;` assigns to a signal called assume.
	switch p.nth(off + 1) {
	case token.Colon, token.Le, token.VarAssign, token.Semicolon,
		token.Dot, token.Tick, token.KwPort, token.KwGeneric, token.EOF:
		return 0, 0
	}
	return kind, off
}

// pslDeclAt returns the PSL declaration kind that starts here, or 0.
func (p *Parser) pslDeclAt() cst.Kind {
	if !p.pslAllowed(0) {
		return 0
	}
	switch {
	case p.atKw(token.KwDefault):
		if p.nthIsKw(1, token.KwClock) {
			return cst.KindPSLDefaultClock
		}
	case p.atKw(token.KwProperty):
		if p.nthIsIdent(1) {
			return cst.KindPSLPropertyDecl
		}
	case p.atKw(token.KwSequence):
		if p.nthIsIdent(1) {
			return cst.KindPSLSequenceDecl
		}
	case p.at(token.KwEndpoint):
		// Only lexed as a keyword inside PSL (a vunit or `-- psl`).
		if p.nthIsIdent(1) {
			return cst.KindPSLEndpointDecl
		}
	}
	return 0
}

// enterPSL switches the lexer to PSL for the current token on and
// returns the mode to restore.
func (x pslParser) enterPSL() lexer.Mode {
	prev := x.mode
	if !x.comment {
		x.setMode(lexer.PSL)
	}
	return prev
}

// leavePSL restores the mode saved by enterPSL.
func (x pslParser) leavePSL(prev lexer.Mode) {
	if !x.comment {
		x.setMode(prev)
	}
}

// ended reports whether the PSL text is exhausted: the end of the input
// or, in a `-- psl` comment, the first token not on a comment line.
func (x pslParser) ended() bool {
	r := x.peek(0)
	if r == nil || r.Kind == token.EOF {
		return true
	}
	return x.comment && r.Flags&lexer.PSLComment == 0
}

// atOp is at for operator positions: false once the PSL text ended.
func (x pslParser) atOp(k token.Kind) bool { return !x.ended() && x.at(k) }

// terminate consumes the `;` ending a directive or declaration,
// skipping unexpected tokens before it into an ErrorNode.
func (x pslParser) terminate() {
	if x.atOp(token.Semicolon) {
		x.bump()
		return
	}
	x.errorExpected("';'")
	if !x.ended() && !x.atAny(pslSync) {
		x.open(cst.KindErrorNode)
		for !x.ended() && !x.atAny(pslSync) {
			x.bump()
		}
		x.close()
	}
	if x.atOp(token.Semicolon) {
		x.bump()
	} else {
		x.missing(token.Semicolon)
	}
}

// pslSync are the tokens that end the recovery skip of a directive:
// its `;`, the `}` of a verification unit, and the begin or end of the
// enclosing region (a missing `;` must not swallow them).
var pslSync = token.NewSet(token.Semicolon, token.RBrace, token.KwBegin,
	token.KwEnd)

// parsePSLDeclaration parses a property, sequence or endpoint
// declaration or a default clock declaration; false, consuming nothing,
// if none starts here.
func (p *Parser) parsePSLDeclaration() bool {
	kind := p.pslDeclAt()
	if kind == 0 {
		return false
	}
	x := p.pslAt(0)
	prev := x.enterPSL()
	x.open(kind)
	switch kind {
	case cst.KindPSLDefaultClock:
		x.bumpKw(token.KwDefault)
		x.bumpKw(token.KwClock)
		x.expectKw(token.KwIs)
		x.hdl("a clock expression")
	default:
		kw := token.KwProperty
		switch kind {
		case cst.KindPSLSequenceDecl:
			kw = token.KwSequence
		case cst.KindPSLEndpointDecl:
			kw = token.KwEndpoint
		}
		x.bumpKw(kw)
		if kind != cst.KindPSLEndpointDecl && x.atIdent() {
			x.declarePSLName(x.nthText(0), kind)
		}
		x.ident()
		if x.atOp(token.LParen) {
			x.formalParams()
		}
		x.expectKw(token.KwIs)
		if kind == cst.KindPSLPropertyDecl {
			x.propOrErr(0)
		} else {
			x.sequenceOrErr()
		}
	}
	x.terminate()
	x.close()
	x.leavePSL(prev)
	return true
}

// declarePSLName records a property or sequence declaration (kind) of
// name.
func (p *Parser) declarePSLName(name string, kind cst.Kind) {
	if p.pslNames == nil {
		p.pslNames = map[string]cst.Kind{}
	}
	p.pslNames[pslKey(name)] = kind
}

// pslNameAt returns the declaration kind of the PSL property or
// sequence named by the simple identifier at buffer index i, or 0.
func (p *Parser) pslNameAt(i int) cst.Kind {
	if i < 0 || p.pslNames == nil {
		return 0
	}
	r := p.rawAt(i)
	if r.Kind == token.EOF {
		return 0
	}
	return p.pslNames[pslKey(p.text[r.Start:r.End])]
}

// pslKey is the lookup key of a name: basic identifiers are case
// insensitive, extended identifiers are not.
func pslKey(name string) string {
	if strings.HasPrefix(name, "\\") {
		return name
	}
	return strings.ToLower(name)
}

// formalParams parses `( param_spec name {, name} {; ...} )`.
func (x pslParser) formalParams() {
	x.open(cst.KindPSLFormalParameterList)
	x.bump()
	for !x.ended() && !x.at(token.RParen) {
		x.open(cst.KindPSLFormalParameter)
		x.paramSpec()
		x.ident()
		for x.atOp(token.Comma) {
			x.bump()
			x.ident()
		}
		x.close()
		if !x.atOp(token.Semicolon) {
			break
		}
		x.bump()
	}
	x.expect(token.RParen)
	x.close()
}

// paramSpec parses `const | [const|mutable] value_type | sequence |
// property` where value_type is a PSL type class or `hdltype type`.
func (x pslParser) paramSpec() {
	qual := x.atOp(token.KwConst) || x.atOp(token.KwMutable)
	if qual {
		x.bump()
	}
	switch x.nth(0) {
	case token.KwBoolean, token.KwBit, token.KwBitvector, token.KwNumeric,
		token.KwString, token.KwSequence, token.KwProperty:
		x.bump()
	case token.KwHdltype:
		x.bump()
		if x.typeMarkThenName() {
			// `hdltype integer i`: a bare type mark, not a resolution
			// function before a type mark.
			x.open(cst.KindSubtypeIndication)
			x.parseSelectedName()
			x.close()
		} else if !x.parseSubtypeIndication() {
			x.errorExpected("an HDL type")
		}
	default:
		if !qual {
			x.errorExpected("a PSL parameter type (const, boolean, " +
				"bit, bitvector, numeric, string, sequence, property " +
				"or hdltype)")
		}
	}
}

// typeMarkThenName reports whether a type mark `name {. name}` is
// followed directly by an identifier.
func (x pslParser) typeMarkThenName() bool {
	i := x.nthIdx(0)
	for n := 0; i >= 0 && n < pslScanLimit; n++ {
		if !x.identAt(i) {
			return false
		}
		i = x.visibleAt(i + 1)
		if x.kindAt(i) != token.Dot {
			return x.identAt(i)
		}
		i = x.visibleAt(i + 1)
	}
	return false
}

// identAt reports whether buffered token i can be an identifier in PSL.
func (x pslParser) identAt(i int) bool {
	switch k := x.kindAt(i); k {
	case token.Identifier, token.ExtendedIdentifier:
		return true
	default:
		kw, ok := k.Keyword()
		return ok && kw.Class == token.ClassPSL
	}
}

// parsePSLDirective parses a labelled or unlabelled PSL directive
// (assert, assume, assume_guarantee, restrict, restrict_guarantee,
// cover, fairness, strong fairness), or else a PSL declaration (see
// parsePSLDeclaration); false, consuming nothing, if neither starts
// here. A plain-Boolean concurrent assert becomes a VHDL
// ConcurrentAssertionStmt (#6).
func (p *Parser) parsePSLDirective() bool {
	kind, off := p.pslDirectiveAt()
	if kind == 0 {
		// A caller at a PSL trigger may hand over a declaration (an
		// endpoint in a `-- psl` comment) too.
		return p.parsePSLDeclaration()
	}
	x := p.pslAt(off)
	m := x.mark()
	if off > 0 {
		x.open(cst.KindLabel)
		x.ident()
		x.expect(token.Colon)
		x.close()
	}
	prev := x.enterPSL()
	if kind == cst.KindPSLAssert && !x.comment && prev == lexer.VHDL &&
		x.plainAssert() {
		x.leavePSL(prev)
		x.openAt(m, cst.KindConcurrentAssertionStmt)
		x.bumpKw(token.KwAssert)
		x.hdl("a condition")
		if x.eatKw(token.KwReport) {
			x.hdl("a report expression")
		}
		if x.eatKw(token.KwSeverity) {
			x.hdl("a severity expression")
		}
		x.terminate()
		x.close()
		return true
	}
	x.openAt(m, kind)
	switch kind {
	case cst.KindPSLAssert, cst.KindPSLAssume,
		cst.KindPSLAssumeGuarantee:
		x.bumpKw(directiveKw[kind])
		x.propOrErr(0)
	case cst.KindPSLRestrict, cst.KindPSLRestrictGuarantee,
		cst.KindPSLCover:
		x.bumpKw(directiveKw[kind])
		x.sequenceOrErr()
	case cst.KindPSLFairness:
		strong := x.atKw(token.KwStrong)
		if strong {
			x.bumpKw(token.KwStrong)
		}
		x.bumpKw(token.KwFairness)
		x.hdl("a fairness condition")
		if strong || x.atOp(token.Comma) {
			// strong fairness b1, b2 (#6); plain fairness takes one
			// condition, or two.
			x.expect(token.Comma)
			x.hdl("a fairness condition")
		}
	}
	if kind != cst.KindPSLFairness &&
		(x.atKwOp(token.KwReport) || x.atKwOp(token.KwSeverity)) {
		x.open(cst.KindPSLReport)
		if x.eatKw(token.KwReport) {
			x.hdl("a report expression")
		}
		if x.atKwOp(token.KwSeverity) {
			x.bumpKw(token.KwSeverity)
			x.hdl("a severity expression")
		}
		x.close()
	}
	x.terminate()
	x.close()
	x.leavePSL(prev)
	return true
}

// directiveKw maps a directive kind to its keyword.
var directiveKw = map[cst.Kind]token.Kind{
	cst.KindPSLAssert:            token.KwAssert,
	cst.KindPSLAssume:            token.KwAssume,
	cst.KindPSLAssumeGuarantee:   token.KwAssumeGuarantee,
	cst.KindPSLRestrict:          token.KwRestrict,
	cst.KindPSLRestrictGuarantee: token.KwRestrictGuarantee,
	cst.KindPSLCover:             token.KwCover,
}

func (x pslParser) atKwOp(k token.Kind) bool { return !x.ended() && x.atKw(k) }

// parsePSLUnitItem parses one item of a vunit, vmode or vprop body (the
// lexer is already in PSL mode): inherit, PSL declarations and
// directives, and VHDL declarations and concurrent statements.
func (p *Parser) parsePSLUnitItem() bool {
	if p.atEOF() || p.at(token.RBrace) {
		return false
	}
	if p.atKw(token.KwInherit) && p.nthIsIdent(1) {
		p.open(cst.KindPSLInheritSpec)
		p.bump()
		p.parseSelectedName()
		for p.eat(token.Comma) {
			p.parseSelectedName()
		}
		p.expect(token.Semicolon)
		p.close()
		return true
	}
	if p.parsePSLDirective() || p.parsePSLDeclaration() {
		return true
	}
	if p.parseDeclarativeItem(RegionPSLUnit) {
		return true
	}
	return p.parseConcurrentStatement()
}

// parsePSLProperty parses a PSL property into a node of the PSLProp
// group; parsePSLSequence a sequence into a node of the PSLSere group.
// Both return false, consuming nothing, when none starts here. The
// caller owns the lexer mode.
func (p *Parser) parsePSLProperty() bool { return p.pslAt(0).prop(0) }

func (p *Parser) parsePSLSequence() bool { return p.pslAt(0).sere(sbFusion) }

// pslExprStop reports whether the expression grammar must stop before
// the current binary operator because it belongs to the PSL grammar: an
// `and` or `or` inside PSL whose right operand is a PSL-only property
// (`a and always b`, `a or (b -> c)`). The expr builder calls it in its
// binary-operator loop. It is always false outside PSL.
func (p *Parser) pslExprStop() bool {
	r := p.peek(0)
	if r == nil || (p.mode != lexer.PSL && r.Flags&lexer.PSLComment == 0) {
		return false
	}
	if r.Kind != token.KwAnd && r.Kind != token.KwOr {
		return false
	}
	x := pslParser{Parser: p}
	return x.pslOperandAt(p.visibleAt(p.nthIdx(0) + 1))
}

// ------------------------------------------------------- properties

func (x pslParser) propOrErr(min int) {
	if !x.prop(min) {
		x.errorExpected("a PSL property")
	}
}

// prop parses a property whose operators bind at least as tightly as
// min.
func (x pslParser) prop(min int) bool {
	if !x.enter() {
		x.tooDeep()
		x.leave()
		return true
	}
	defer x.leave()
	m := x.mark()
	if !x.propPrimary() {
		return false
	}
	for !x.ended() {
		k := x.nth(0)
		switch {
		case k == token.At && min <= bpClock:
			x.openAt(m, cst.KindPSLClockedProp)
			x.bump()
			x.hdl("a clock expression")
		case (k == token.PSLImplies || k == token.PSLIff) &&
			min <= bpImplication:
			x.openAt(m, cst.KindPSLImplication)
			x.bump()
			x.propOrErr(bpImplication)
		case (k == token.PSLOverlapImplies || k == token.PSLNextImplies) &&
			min <= bpSuffix:
			x.openAt(m, cst.KindPSLSuffixImplication)
			x.bump()
			x.propOrErr(bpSuffix)
		case (k == token.KwUntil || k == token.KwBefore) &&
			min <= bpBounding:
			kind := cst.KindPSLUntil
			if k == token.KwBefore {
				kind = cst.KindPSLBefore
			}
			x.openAt(m, kind)
			x.bump()
			x.suffixes()
			x.propOrErr(bpBounding)
		case (k == token.KwAbort || k == token.KwAsyncAbort ||
			k == token.KwSyncAbort) && min <= bpAbort:
			x.openAt(m, cst.KindPSLAbort)
			x.bump()
			x.hdl("an abort condition")
		case (k == token.KwAnd || k == token.KwOr || k == token.PSLOrOr) &&
			min <= bpLogical:
			x.openAt(m, cst.KindPSLLogicalProp)
			x.bump()
			x.propOrErr(bpLogical + 1)
		default:
			return true
		}
		x.close()
	}
	return true
}

// suffixes consumes the `!` and `_` suffixes of until, before and the
// occurrence operators.
func (x pslParser) suffixes() {
	if x.atOp(token.PSLBangSuffix) {
		x.bump()
	}
	if x.atOp(token.PSLUnderscoreSuffix) {
		x.bump()
	}
}

// propPrimary parses a prefix-operator property, a parenthesized or
// bracketed property, or a sequence item.
func (x pslParser) propPrimary() bool {
	if x.ended() {
		return false
	}
	i := x.nthIdx(0)
	if i < 0 {
		return false
	}
	k := x.kindAt(i)
	if x.prefixAt(i) {
		x.prefixOp(k)
		return true
	}
	switch k {
	case token.LBracket:
		// [p U q] / [p W q]
		x.open(cst.KindPSLUntil)
		x.bump()
		x.propOrErr(bpInvariance)
		if x.atOp(token.KwU) || x.atOp(token.KwW) {
			x.bump()
		} else {
			x.errorExpected("U or W")
		}
		x.propOrErr(bpInvariance)
		x.expect(token.RBracket)
		x.close()
		return true
	case token.PSLBang:
		x.notOp()
		return true
	case token.KwNot:
		if x.pslOperandAt(x.visibleAt(i + 1)) {
			x.notOp()
			return true
		}
	case token.LParen:
		if x.parenIsPSL(i) {
			x.parenProp()
			return true
		}
	}
	// A sequence used as a property; compound SEREs (`{a} | {b}`) are
	// accepted unbraced too.
	return x.sereT(sbFusion, false)
}

func (x pslParser) notOp() {
	x.open(cst.KindPSLLogicalProp)
	x.bump()
	x.propOrErr(bpNot)
	x.close()
}

func (x pslParser) parenProp() {
	x.open(cst.KindPSLParenProp)
	x.bump()
	x.propOrErr(0)
	x.expect(token.RParen)
	x.close()
}

// prefixOp parses a property that starts with prefix operator k.
func (x pslParser) prefixOp(k token.Kind) {
	switch k {
	case token.KwAlways, token.KwG:
		x.open(cst.KindPSLAlways)
		x.bump()
		x.propOrErr(bpInvariance)
	case token.KwNever:
		x.open(cst.KindPSLNever)
		x.bump()
		x.propOrErr(bpInvariance)
	case token.KwEventually, token.KwF:
		// The lexer reports `eventually` without `!`.
		x.open(cst.KindPSLEventually)
		x.bump()
		x.suffixes()
		x.propOrErr(bpOccurrence)
	case token.KwNext, token.KwX:
		x.open(cst.KindPSLNext)
		x.bump()
		if k == token.KwX && x.atOp(token.PSLBang) && x.adjacent() {
			x.bump() // X! (the lexer has no suffix rule for X)
		}
		x.suffixes()
		if x.atOp(token.LBracket) {
			x.count()
		}
		x.propOrErr(bpOccurrence)
	case token.KwNextA, token.KwNextE:
		kind := cst.KindPSLNextA
		if k == token.KwNextE {
			kind = cst.KindPSLNextE
		}
		x.open(kind)
		x.bump()
		x.suffixes()
		if x.atOp(token.LBracket) {
			x.count()
		} else {
			x.errorExpected("'[' and a range")
		}
		x.propOrErr(bpOccurrence)
	case token.KwNextEvent, token.KwNextEventA, token.KwNextEventE:
		kind := cst.KindPSLNextEvent
		switch k {
		case token.KwNextEventA:
			kind = cst.KindPSLNextEventA
		case token.KwNextEventE:
			kind = cst.KindPSLNextEventE
		}
		x.open(kind)
		x.bump()
		x.suffixes()
		x.expect(token.LParen)
		x.hdl("a Boolean")
		x.expect(token.RParen)
		if x.atOp(token.LBracket) {
			x.count()
		} else if k != token.KwNextEvent {
			x.errorExpected("'[' and a range")
		}
		if x.atOp(token.LParen) {
			x.parenProp()
		} else {
			x.errorExpected("'(' and a property")
		}
	case token.KwForall:
		x.forall()
	case token.KwAx, token.KwAg, token.KwAf, token.KwEx, token.KwEg,
		token.KwEf:
		x.open(cst.KindPSLOBEUnary)
		x.bump()
		x.propOrErr(bpOccurrence)
	case token.KwA, token.KwE:
		x.open(cst.KindPSLOBEUntil)
		x.bump()
		x.expect(token.LBracket)
		x.propOrErr(bpInvariance)
		if x.atOp(token.KwU) {
			x.bump()
		} else {
			x.errorExpected("U")
			x.missing(token.KwU)
		}
		x.propOrErr(bpInvariance)
		x.expect(token.RBracket)
	}
	x.close()
}

// adjacent reports whether the current token directly follows the last
// consumed one, with no trivia between them.
func (x pslParser) adjacent() bool {
	i := x.nthIdx(0)
	return i >= 0 && x.buf[i].FullStart == x.buf[i].Start &&
		x.lastEnd == x.buf[i].Start
}

// forall parses `forall name [(range)] in value_set : property`.
func (x pslParser) forall() {
	x.open(cst.KindPSLForall)
	x.bump()
	x.ident()
	if x.atOp(token.LParen) {
		x.bump()
		x.hdl("a range bound")
		if x.atOp(token.KwTo) || x.atOp(token.KwDownto) {
			x.bump()
			x.hdl("a range bound")
		}
		x.expect(token.RParen)
	}
	x.expectKw(token.KwIn)
	if x.atOp(token.KwBoolean) {
		x.open(cst.KindPSLValueSet)
		x.bump()
		x.close()
	} else if x.atOp(token.LBrace) {
		x.parsePSLValueSet()
	} else {
		x.open(cst.KindPSLValueSet)
		x.expect(token.LBrace)
		x.close()
	}
	x.expect(token.Colon)
	x.propOrErr(bpInvariance)
}

// parsePSLValueSet parses `{ value [to value] {, value [to value]} }`
// at a `{` into a PSLValueSet (forall, nondet).
func (p *Parser) parsePSLValueSet() {
	x := pslParser{Parser: p}
	x.open(cst.KindPSLValueSet)
	x.bump() // {
	for !x.ended() && !x.at(token.RBrace) {
		if !x.hdl("a value") {
			break
		}
		if x.atOp(token.KwTo) {
			x.bump()
			x.hdl("a value")
		}
		if !x.atOp(token.Comma) {
			break
		}
		x.bump()
	}
	x.expect(token.RBrace)
	x.close()
}

// count parses `[ bound [to|: bound] ]` into a PSLCount.
func (x pslParser) count() {
	x.open(cst.KindPSLCount)
	x.bump()
	x.bounds()
	x.expect(token.RBracket)
	x.close()
}

// bounds parses `bound [to|: bound]`.
func (x pslParser) bounds() {
	x.bound()
	if x.atOp(token.KwTo) || x.atOp(token.Colon) {
		x.bump()
		x.bound()
	}
}

func (x pslParser) bound() {
	if x.atOp(token.KwInf) {
		x.bump()
		return
	}
	x.hdl("a count")
}

// hdl parses a VHDL expression, reporting what was expected if none
// starts here.
func (x pslParser) hdl(what string) bool {
	if !x.ended() && x.parseExpression() {
		return true
	}
	x.errorExpected(what)
	return false
}

// -------------------------------------------------------- sequences

func (x pslParser) sequenceOrErr() {
	if !x.sere(sbFusion) {
		x.errorExpected("a PSL sequence")
	}
}

// sere parses a SERE whose operators bind at least as tightly as min
// (sbConcat inside braces; sbFusion where `;` ends the directive).
func (x pslParser) sere(min int) bool { return x.sereT(min, true) }

// sereT is sere; tight says whether an `@` binds to an item (inside a
// sequence) or is left to the enclosing property.
func (x pslParser) sereT(min int, tight bool) bool {
	if !x.enter() {
		x.tooDeep()
		x.leave()
		return true
	}
	defer x.leave()
	m := x.mark()
	if !x.seqItem(tight) {
		return false
	}
	for !x.ended() {
		kind, bp := x.sereOp()
		if kind == 0 || bp < min {
			break
		}
		x.openAt(m, kind)
		x.bump()
		if !x.sereT(bp+1, tight) {
			x.errorExpected("a PSL sequence")
		}
		x.close()
	}
	return true
}

func (x pslParser) sereOp() (cst.Kind, int) {
	switch x.nth(0) {
	case token.Semicolon:
		return cst.KindPSLSereConcat, sbConcat
	case token.Colon:
		return cst.KindPSLSereFusion, sbFusion
	case token.Bar:
		return cst.KindPSLSereOr, sbOr
	case token.Ampersand, token.PSLAndAnd:
		return cst.KindPSLSereAnd, sbAnd
	case token.KwWithin:
		return cst.KindPSLSereWithin, sbWithin
	}
	return 0, 0
}

// seqItem parses a braced SERE, a bare repetition, a built-in call or
// an HDL Boolean, then its repetitions and (with tight) an `@` clock.
func (x pslParser) seqItem(tight bool) bool {
	if x.ended() {
		return false
	}
	m := x.mark()
	switch {
	case x.at(token.LBrace):
		x.open(cst.KindPSLBracedSere)
		x.bump()
		if !x.sere(sbConcat) {
			x.errorExpected("a PSL sequence")
		}
		x.expect(token.RBrace)
		x.close()
	case x.atRep():
		x.open(cst.KindPSLRepetition)
		x.repSuffix()
		x.close()
	case x.at(token.LParen) && x.parenIsPSL(x.nthIdx(0)):
		// ({a; b}): a parenthesized sequence or property.
		x.parenProp()
	case x.atInstance():
		kind := cst.KindPSLSequenceInstance
		switch x.pslNameAt(x.nthIdx(0)) {
		case cst.KindPSLPropertyDecl:
			kind = cst.KindPSLPropertyInstance
		case cst.KindPSLSequenceDecl:
		default:
			if !tight {
				kind = cst.KindPSLPropertyInstance
			}
		}
		x.instance(kind)
	case x.atBuiltinCall():
		x.open(cst.KindPSLBuiltinCall)
		x.bump()
		x.bump() // (
		x.builtinArg()
		for x.atOp(token.Comma) {
			x.bump()
			x.builtinArg()
		}
		x.expect(token.RParen)
		x.close()
	default:
		k := x.exExpr()
		if k == 0 {
			return false
		}
		// exExpr builds `a union b` (a PSLUnion) itself in PSL text
		// (but not across the end of a `-- psl` comment).
		for x.atOp(token.KwUnion) {
			x.openAt(m, cst.KindPSLUnion)
			x.bump()
			x.hdl("an expression")
			x.close()
			k = cst.KindPSLUnion
		}
		if k != cst.KindPSLUnion {
			x.openAt(m, cst.KindPSLBoolean)
			x.close()
		}
	}
	for !x.ended() {
		switch {
		case x.atRep():
			x.openAt(m, cst.KindPSLRepetition)
			x.repSuffix()
			x.close()
		case tight && x.at(token.At):
			x.openAt(m, cst.KindPSLClockedProp)
			x.bump()
			x.hdl("a clock expression")
			x.close()
		default:
			return true
		}
	}
	return true
}

// builtinArg parses an argument of a built-in function: an HDL
// expression, or a sequence (`ended({a; b})`).
func (x pslParser) builtinArg() {
	if x.pslOperandAt(x.nthIdx(0)) {
		x.propOrErr(0)
		return
	}
	x.hdl("an argument")
}

// atInstance reports whether a property or sequence instance with a
// PSL actual (`seq_b(4, {a; b})`) starts here; one with only HDL
// actuals is an ordinary VHDL name (a function call to sema).
func (x pslParser) atInstance() bool {
	i := x.nthIdx(0)
	if i < 0 || pslBuiltins.Has(x.kindAt(i)) || !x.identAt(i) {
		return false
	}
	if x.pslNameAt(i) != 0 {
		// A call of a property or sequence declared in this file.
		return x.kindAt(x.visibleAt(i+1)) == token.LParen
	}
	for n := 0; n < pslScanLimit; n++ {
		j := x.visibleAt(i + 1)
		switch x.kindAt(j) {
		case token.Dot:
			i = x.visibleAt(j + 1)
			if !x.identAt(i) {
				return false
			}
			continue
		case token.LParen:
			return x.parenIsPSL(j)
		}
		return false
	}
	return false
}

// instance parses `name ( actual {, actual} )` with PSL actuals.
func (x pslParser) instance(kind cst.Kind) {
	x.open(kind)
	x.parseSelectedName()
	x.open(cst.KindAssociationList)
	x.bump() // (
	for !x.ended() {
		x.propOrErr(0)
		if !x.atOp(token.Comma) {
			break
		}
		x.bump()
	}
	x.expect(token.RParen)
	x.close()
	x.close()
}

func (x pslParser) atRep() bool {
	switch x.nth(0) {
	case token.PSLRepStar, token.PSLRepPlus, token.PSLRepEq,
		token.PSLRepGoto:
		return true
	}
	return false
}

// repSuffix parses `[* [count]]`, `[+]`, `[= count]` or `[-> [count]]`.
func (x pslParser) repSuffix() {
	k := x.nth(0)
	x.bump()
	switch k {
	case token.PSLRepPlus:
		return
	case token.PSLRepStar, token.PSLRepGoto:
		if !x.atOp(token.RBracket) && !x.ended() {
			x.repCount()
		}
	default:
		x.repCount()
	}
	x.expect(token.RBracket)
}

func (x pslParser) repCount() {
	x.open(cst.KindPSLCount)
	x.bounds()
	x.close()
}

// pslBuiltins are the PSL built-in functions.
var pslBuiltins = token.NewSet(token.KwPrev, token.KwRose, token.KwFell,
	token.KwStable, token.KwOnehot, token.KwOnehot0, token.KwEnded,
	token.KwNondet, token.KwNondetVector)

// atBuiltinCall reports whether a built-in function call that is a PSL
// operand on its own (not part of a larger HDL expression) starts here.
func (x pslParser) atBuiltinCall() bool {
	i := x.nthIdx(0)
	if i < 0 || !pslBuiltins.Has(x.kindAt(i)) {
		return false
	}
	j := x.visibleAt(i + 1)
	if x.kindAt(j) != token.LParen {
		return false
	}
	after := x.afterParen(j)
	if after < 0 {
		return false
	}
	k := x.kindAt(after)
	if k == token.KwAnd || k == token.KwOr {
		return x.pslOperandAt(x.visibleAt(after + 1))
	}
	return !hdlContinuation.Has(k)
}

// hdlContinuation holds the tokens that continue an HDL expression
// after a primary.
var hdlContinuation = token.NewSet(token.KwAnd, token.KwOr,
	token.KwNand, token.KwNor, token.KwXor, token.KwXnor, token.Eq,
	token.Ne, token.Lt, token.Le, token.Gt, token.Ge, token.MatchEq,
	token.MatchNe, token.MatchLt, token.MatchLe, token.MatchGt,
	token.MatchGe, token.KwSll, token.KwSrl, token.KwSla, token.KwSra,
	token.KwRol, token.KwRor, token.Plus, token.Minus, token.Ampersand,
	token.Star, token.Slash, token.KwMod, token.KwRem, token.DoubleStar,
	token.Tick, token.Dot, token.LParen, token.KwWhen)

// ------------------------------------------------- lookahead scans
//
// The scans walk buffered tokens by index (no budget charge, bounded by
// pslScanLimit) so classifying a parenthesis costs time linear in its
// length.

// visibleAt returns the index of the first visible token at or after
// buffer index i (see nthIdx), the EOF index at the end, or -1 when the
// inactive branch being parsed ends.
func (p *Parser) visibleAt(i int) int {
	if i < 0 {
		return -1
	}
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
		return i
	}
}

// kindAt returns the kind of buffered token i (EOF for -1).
func (p *Parser) kindAt(i int) token.Kind {
	if i < 0 {
		return token.EOF
	}
	return p.rawAt(i).Kind
}

// afterParen returns the index of the token after the `)` matching the
// `(` at i, or -1.
func (x pslParser) afterParen(i int) int {
	depth, braces := 0, 0
	for n := 0; i >= 0 && n < pslScanLimit; n++ {
		switch x.kindAt(i) {
		case token.EOF:
			return -1
		case token.Semicolon:
			if braces == 0 {
				return -1
			}
		case token.LBrace:
			braces++
		case token.RBrace:
			braces--
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 {
				return x.visibleAt(i + 1)
			}
		}
		i = x.visibleAt(i + 1)
	}
	return -1
}

// parenIsPSL reports whether the parenthesis at i holds PSL (a
// PSLParenProp) rather than an HDL expression.
func (x pslParser) parenIsPSL(i int) bool {
	depth := 0
	prev := token.Kind(0)
	for n := 0; i >= 0 && n < pslScanLimit; n++ {
		k := x.kindAt(i)
		switch k {
		case token.EOF, token.Semicolon:
			return false
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 {
				return false
			}
		}
		if x.pslIndicator(i, prev) {
			return true
		}
		prev = k
		i = x.visibleAt(i + 1)
	}
	return false
}

// plainAssert reports whether the property of the concurrent assert at
// the current token is a plain HDL Boolean: no PSL operator before the
// report or severity clause or the closing `;`.
func (x pslParser) plainAssert() bool {
	i := x.visibleAt(x.nthIdx(0) + 1)
	if x.pslNameAt(i) != 0 {
		return false // assert p(a, b) of a declared property
	}
	depth := 0
	prev := token.KwAssert
	for n := 0; i >= 0 && n < pslStmtScanLimit; n++ {
		k := x.kindAt(i)
		switch k {
		case token.EOF:
			return true
		case token.LParen:
			depth++
		case token.RParen:
			depth = max(depth-1, 0)
		case token.Semicolon, token.KwReport, token.KwSeverity:
			if depth == 0 {
				return true
			}
		}
		if x.pslIndicator(i, prev) {
			return false
		}
		prev = k
		i = x.visibleAt(i + 1)
	}
	return false
}

// pslIndicator reports whether token i (after a token of kind prev)
// can only be PSL.
func (x pslParser) pslIndicator(i int, prev token.Kind) bool {
	k := x.kindAt(i)
	switch {
	case k.IsPSLOnly(), k == token.At, k == token.LBrace,
		k == token.RBrace:
		return true
	case k == token.LBracket:
		// A signature after a name is VHDL: f[integer]'path_name.
		return !operandEnd(prev)
	}
	switch k {
	case token.KwUntil, token.KwBefore, token.KwWithin, token.KwAbort,
		token.KwAsyncAbort, token.KwSyncAbort:
		// union is not here: the HDL expression grammar takes it in
		// PSL text, so (a union b) = 1 stays one HDL operand.
		return operandEnd(prev)
	}
	return x.prefixAt(i)
}

// operandEnd reports whether a token of kind k can end an operand.
func operandEnd(k token.Kind) bool {
	switch k {
	case token.Identifier, token.ExtendedIdentifier, token.IntegerLiteral,
		token.RealLiteral, token.CharacterLiteral, token.StringLiteral,
		token.BitStringLiteral, token.RParen, token.RBracket, token.RBrace,
		token.PSLRepPlus, token.KwNull, token.KwAll:
		return true
	}
	kw, ok := k.Keyword()
	return ok && kw.Class == token.ClassPSL
}

// operandStart reports whether a token of kind k can start a PSL or
// HDL operand.
func operandStart(k token.Kind) bool {
	switch k {
	case token.Identifier, token.ExtendedIdentifier, token.IntegerLiteral,
		token.RealLiteral, token.CharacterLiteral, token.StringLiteral,
		token.BitStringLiteral, token.LParen, token.LBrace,
		token.LBracket, token.PSLBang, token.LtLt, token.PSLRepStar,
		token.PSLRepPlus, token.KwNot, token.KwNull, token.KwNext:
		return true
	case token.KwBefore, token.KwWithin, token.KwAbort,
		token.KwAsyncAbort, token.KwSyncAbort, token.KwUnion, token.KwU,
		token.KwW:
		return false // binary PSL operators
	}
	kw, ok := k.Keyword()
	return ok && kw.Class == token.ClassPSL
}

// prefixAt reports whether token i is a PSL prefix operator (always,
// next!, AG, ...) rather than a name spelled like one: it must be
// followed by what the operator takes. The one-letter words X, F and G
// before `(` are taken as names (x(3)).
func (x pslParser) prefixAt(i int) bool {
	k := x.kindAt(i)
	switch k {
	case token.KwAlways, token.KwNever, token.KwEventually, token.KwNext,
		token.KwNextA, token.KwNextE, token.KwNextEvent,
		token.KwNextEventA, token.KwNextEventE, token.KwForall,
		token.KwAx, token.KwAg, token.KwAf, token.KwEx, token.KwEg,
		token.KwEf, token.KwA, token.KwE, token.KwX, token.KwF,
		token.KwG:
	default:
		return false
	}
	nk := x.kindAt(x.visibleAt(i + 1))
	switch k {
	case token.KwEventually:
		return nk == token.PSLBangSuffix || operandStart(nk)
	case token.KwNext:
		return nk == token.PSLBangSuffix || operandStart(nk)
	case token.KwNextA, token.KwNextE, token.KwNextEvent,
		token.KwNextEventA, token.KwNextEventE:
		return nk == token.PSLBangSuffix || operandStart(nk)
	case token.KwForall:
		return nk == token.Identifier || nk == token.ExtendedIdentifier ||
			operandStart(nk) && nk != token.LParen
	case token.KwA, token.KwE:
		return nk == token.LBracket
	case token.KwX:
		return nk == token.PSLBangSuffix || nk == token.PSLBang ||
			(operandStart(nk) && nk != token.LParen)
	case token.KwF, token.KwG:
		return operandStart(nk) && nk != token.LParen
	}
	return operandStart(nk)
}

// pslOperandAt reports whether a PSL-only property starts at token i:
// a prefix operator, a braced SERE, `!`, a repetition, a bracketed
// until, a parenthesized property, or `not` before one of those.
func (x pslParser) pslOperandAt(i int) bool {
	for n := 0; i >= 0 && n < pslScanLimit; n++ {
		switch x.kindAt(i) {
		case token.LBrace, token.LBracket, token.PSLBang,
			token.PSLRepStar, token.PSLRepPlus:
			return true
		case token.LParen:
			return x.parenIsPSL(i)
		case token.KwNot:
			i = x.visibleAt(i + 1)
			continue
		}
		return x.prefixAt(i)
	}
	return false
}
