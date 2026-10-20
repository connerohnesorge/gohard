package parser

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Expressions, names, association lists, aggregates, choices and the
// range/subtype tails of expressions (builder: expr). VHDL-1993 §7,
// VHDL-2008 §9 and §8, VHDL-2019 §9. Subtype indications, constraints,
// resolution indications and discrete ranges are in types.go.
//
// Precedence, loosest first (VHDL-2008 §9.2):
//
//	?? primary                        (2008; expression level only)
//	and or xor xnor nand nor          one kind of operator per chain;
//	                                  nand and nor do not chain
//	= /= < <= > >= ?= ?/= ?< ?<= ?> ?>=   not associative
//	sll srl sla sra rol ror           not associative
//	[sign] term {+ - & term}          the sign covers the first term
//	factor {* / mod rem factor}
//	unary [** unary]                  ** is not associative
//	abs not and or nand nor xor xnor primary  (unary; reductions 2008)
//
// Operators of one level are left-associative. Breaking a rule above
// (mixing logical operators, chaining a non-associative operator, a
// sign after an operator) is reported as syntax/error and parsed
// left-associatively, so the tree stays useful.
//
// Ambiguous forms are not resolved here (sema does it): `name(...)` is
// one CallOrIndexName node for calls, indexing and type conversions; it
// is a SliceName only when its single element is syntactically a range
// (`a to b`, `a downto b`, `t range ...`). Every node is built with
// post-hoc wrapping (mark, then exWrap once the extent is known), so no
// token is ever re-read and no lookahead beyond a few tokens is needed,
// except the bounded scan of exSignatureAhead.

// exFlags select the optional tails of exOrRange.
type exFlags uint8

const (
	// exWhen allows a VHDL-2019 conditional expression tail
	// (`a when c else b`).
	exWhen exFlags = 1 << iota
	// exSubtype allows a subtype indication with a resolution function
	// (`resolved std_logic`), as in generic type actuals.
	exSubtype
)

// exParenStop ends the error recovery inside parentheses.
var exParenStop = token.NewSet(token.Comma, token.Semicolon, token.KwIs,
	token.KwBegin, token.KwEnd, token.KwThen, token.KwLoop,
	token.KwGenerate)

// exExternalStop ends the error recovery inside an external name.
var exExternalStop = token.NewSet(token.Colon, token.GtGt,
	token.Semicolon)

// exUnaryOps are the operators of a unary expression (sign excluded).
var exUnaryOps = token.NewSet(token.KwAbs, token.KwNot, token.KwAnd,
	token.KwOr, token.KwNand, token.KwNor, token.KwXor, token.KwXnor)

// exLogicalOps, exRelOps, exMatchOps, exShiftOps, exAddOps and
// exMulOps are the binary operators by level.
var (
	exLogicalOps = token.NewSet(token.KwAnd, token.KwOr, token.KwNand,
		token.KwNor, token.KwXor, token.KwXnor)
	exRelOps = token.NewSet(token.Eq, token.Ne, token.Lt, token.Le,
		token.Gt, token.Ge, token.MatchEq, token.MatchNe, token.MatchLt,
		token.MatchLe, token.MatchGt, token.MatchGe)
	exMatchOps = token.NewSet(token.MatchEq, token.MatchNe,
		token.MatchLt, token.MatchLe, token.MatchGt, token.MatchGe)
	exShiftOps = token.NewSet(token.KwSll, token.KwSrl, token.KwSla,
		token.KwSra, token.KwRol, token.KwRor)
	exPowOps = token.NewSet(token.DoubleStar)
	exAddOps = token.NewSet(token.Plus, token.Minus, token.Ampersand)
	exMulOps = token.NewSet(token.Star, token.Slash, token.KwMod,
		token.KwRem)
)

// exWrap wraps everything added since m into a node of kind k.
func (p *Parser) exWrap(m cst.Checkpoint, k cst.Kind) {
	p.openAt(m, k)
	p.close()
}

// exIsName reports whether k is a node kind of the Name group.
func exIsName(k cst.Kind) bool {
	switch k {
	case cst.KindSimpleName, cst.KindOperatorSymbol, cst.KindCharacterName,
		cst.KindSelectedName, cst.KindCallOrIndexName, cst.KindSliceName,
		cst.KindAttributeName, cst.KindExternalName:
		return true
	}
	return false
}

// exQuiet reports whether the input ended after a too-deep cut
// (tooDeep consumed the rest): the unclosed levels report nothing more.
func (p *Parser) exQuiet() bool { return p.reportedDeep && p.atEOF() }

// exExpect is expect, silent after a too-deep cut.
func (p *Parser) exExpect(k token.Kind) bool {
	if p.exQuiet() {
		return false
	}
	return p.expect(k)
}

// exMissing reports a missing operand and inserts an ErrorNode holding
// a zero-width missing identifier in its place.
func (p *Parser) exMissing() cst.Kind {
	if p.exQuiet() {
		return 0
	}
	p.errorExpected("an expression")
	p.open(cst.KindErrorNode)
	p.missing(token.Identifier)
	p.close()
	return cst.KindErrorNode
}

// exRequire parses an expression with parse, or a missing operand.
func (p *Parser) exRequire(parse func() cst.Kind) {
	if parse() == 0 {
		p.exMissing()
	}
}

// exGateTok gates the current token with rules.csv row id.
func (p *Parser) exGateTok(id string) {
	s, e := p.curSpan()
	p.gateRule(id, s, e)
}

// ------------------------------------------------------- expressions

// parseExpression parses an expression into a node of the Expr group.
// It returns false, consuming nothing, when no expression starts here.
// It never consumes `when`: statement builders own the when/else forms
// of waveforms and assignments; use parseConditionalExpression where
// VHDL-2019 allows a conditional expression.
func (p *Parser) parseExpression() bool { return p.exExpr() != 0 }

// parseConditionalExpression parses `expr {when cond else expr} [when
// cond]` (VHDL-2019 §9.1, gated by cond_expr) into a ConditionalExpr
// of WhenBranch nodes when a `when` follows, else into the plain
// expression. False, consuming nothing, if no expression starts here.
func (p *Parser) parseConditionalExpression() bool {
	m := p.mark()
	if p.exExpr() == 0 {
		return false
	}
	if p.at(token.KwWhen) {
		p.exCondTail(m)
	}
	return true
}

// exExpr parses an expression and returns the kind of its node, or 0
// (nothing consumed).
func (p *Parser) exExpr() cst.Kind {
	if !p.enter() {
		p.leave()
		if p.atEOF() {
			return 0
		}
		p.tooDeep()
		return cst.KindErrorNode
	}
	defer p.leave()
	if p.at(token.Condition) {
		m := p.mark()
		p.exGateTok("cond_conv")
		p.bump()
		p.exRequire(p.exPrimary)
		p.exWrap(m, cst.KindUnaryExpr)
		return cst.KindUnaryExpr
	}
	m := p.mark()
	k := p.exLogical()
	if k == 0 || !p.inPSL() {
		return k
	}
	// PSL's union binds looser than every HDL operator (IEEE 1850
	// 5.1.1): {(a union b) = 1}, countones(a union b).
	for p.at(token.KwUnion) {
		p.bump()
		p.exRequire(p.exLogical)
		p.exWrap(m, cst.KindPSLUnion)
		k = cst.KindPSLUnion
	}
	return k
}

// inPSL reports whether the current token is PSL text (PSL mode or a
// `-- psl` comment line).
func (p *Parser) inPSL() bool {
	r := p.peek(0)
	return r != nil && (p.mode == lexer.PSL || r.Flags&lexer.PSLComment != 0)
}

// exLogical parses relation {logical_operator relation}.
func (p *Parser) exLogical() cst.Kind {
	m := p.mark()
	k := p.exRelation()
	if k == 0 {
		return 0
	}
	var first token.Kind
	for {
		op := p.nth(0)
		if !exLogicalOps.Has(op) || p.pslExprStop() {
			return k
		}
		switch {
		case first == 0:
			first = op
		case op != first:
			p.error("a chain of logical operators must use one operator; " +
				"add parentheses")
		case op == token.KwNand || op == token.KwNor:
			p.error(op.Text() + " is not associative; add parentheses")
		}
		p.bump()
		p.exRequire(p.exRelation)
		p.exWrap(m, cst.KindBinaryExpr)
		k = cst.KindBinaryExpr
	}
}

// exNonAssoc parses operand [op operand] for a non-associative level;
// a second operator is reported and parsed left-associatively.
func (p *Parser) exNonAssoc(ops token.Set, operand func() cst.Kind,
	what string,
) cst.Kind {
	m := p.mark()
	k := operand()
	if k == 0 {
		return 0
	}
	for n := 0; ops.Has(p.nth(0)); n++ {
		if n > 0 {
			p.error(what + " operators are not associative; add " +
				"parentheses")
		}
		if exMatchOps.Has(p.nth(0)) {
			p.exGateTok("match_ops")
		}
		p.bump()
		p.exRequire(operand)
		p.exWrap(m, cst.KindBinaryExpr)
		k = cst.KindBinaryExpr
	}
	return k
}

// exRelation parses shift_expression [relational_operator
// shift_expression] (LRM 9.1). Relational operators do not chain: a
// second one (a = b = c) is a syntax error, and the chain is parsed
// left-associatively so the tree stays whole.
func (p *Parser) exRelation() cst.Kind {
	return p.exNonAssoc(exRelOps, p.exShift, "relational")
}

// exShift parses simple_expression [shift_operator simple_expression].
func (p *Parser) exShift() cst.Kind {
	return p.exNonAssoc(exShiftOps, p.exSimple, "shift")
}

// exSimple parses [sign] term {adding_operator term}. The sign applies
// to the first term: -a*b is -(a*b), -a+b is (-a)+b.
func (p *Parser) exSimple() cst.Kind {
	m := p.mark()
	var k cst.Kind
	if t := p.nth(0); t == token.Plus || t == token.Minus {
		p.bump()
		p.exRequire(p.exTerm)
		p.exWrap(m, cst.KindUnaryExpr)
		k = cst.KindUnaryExpr
	} else if k = p.exTerm(); k == 0 {
		return 0
	}
	for exAddOps.Has(p.nth(0)) {
		p.bump()
		p.exRequire(p.exTerm)
		p.exWrap(m, cst.KindBinaryExpr)
		k = cst.KindBinaryExpr
	}
	return k
}

// exTerm parses factor {multiplying_operator factor}.
func (p *Parser) exTerm() cst.Kind {
	m := p.mark()
	k := p.exFactor()
	if k == 0 {
		return 0
	}
	for exMulOps.Has(p.nth(0)) {
		p.bump()
		p.exRequire(p.exFactor)
		p.exWrap(m, cst.KindBinaryExpr)
		k = cst.KindBinaryExpr
	}
	return k
}

// exFactor parses unary_expression [** unary_expression].
func (p *Parser) exFactor() cst.Kind {
	return p.exNonAssoc(exPowOps, p.exUnary, "**")
}

// exUnary parses abs/not/reduction primary, or a primary. A sign here
// (after an operator, as in a * -b) is an error but parsed as a unary
// expression, as is a unary operator after another one.
func (p *Parser) exUnary() cst.Kind { return p.exUnaryAfter(0) }

// exUnaryAfter is exUnary right after the unary operator prev (0: none).
func (p *Parser) exUnaryAfter(prev token.Kind) cst.Kind {
	t := p.nth(0)
	sign := t == token.Plus || t == token.Minus
	if !sign && !exUnaryOps.Has(t) {
		if prev != 0 {
			return p.exRequirePrimary()
		}
		return p.exPrimary()
	}
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return cst.KindErrorNode
	}
	defer p.leave()
	m := p.mark()
	switch {
	case prev != 0:
		p.error("a unary operator cannot follow " + prev.Text() +
			"; add parentheses")
	case sign:
		p.error("a sign cannot follow an operator; add parentheses")
	}
	if t != token.KwAbs && t != token.KwNot && !sign {
		p.exGateTok("unary_logic")
	}
	p.bump()
	p.exUnaryAfter(t)
	p.exWrap(m, cst.KindUnaryExpr)
	return cst.KindUnaryExpr
}

// exRequirePrimary parses a primary or inserts a missing operand.
func (p *Parser) exRequirePrimary() cst.Kind {
	if k := p.exPrimary(); k != 0 {
		return k
	}
	return p.exMissing()
}

// exPrimary parses a primary: a name (with its suffixes, a function
// call or a qualified expression), a literal, an aggregate or
// parenthesized expression, an allocator, an external name or
// unaffected. It returns 0 when none starts here.
func (p *Parser) exPrimary() cst.Kind {
	m := p.mark()
	switch p.nth(0) {
	case token.IntegerLiteral, token.RealLiteral:
		p.bump()
		if t := p.nth(0); t == token.Identifier ||
			t == token.ExtendedIdentifier {
			p.open(cst.KindSimpleName)
			p.identOpt()
			p.close()
			p.exWrap(m, cst.KindPhysicalLiteral)
			return cst.KindPhysicalLiteral
		}
		p.exWrap(m, cst.KindLiteralExpr)
		return cst.KindLiteralExpr
	case token.StringLiteral:
		if t := p.nth(1); t == token.LParen || t == token.Dot {
			// "+"(a, b), "+".d: an operator symbol as a name prefix.
			p.open(cst.KindOperatorSymbol)
			p.bump()
			p.close()
			return p.exSuffixes(m, cst.KindOperatorSymbol, true)
		}
		p.open(cst.KindLiteralExpr)
		p.bump()
		p.close()
		return cst.KindLiteralExpr
	case token.CharacterLiteral:
		if p.exCharNameAhead() {
			// '1'[return t]'attr, '1''a: an enumeration literal as the
			// prefix of an attribute name.
			p.open(cst.KindCharacterName)
			p.bump()
			p.close()
			return p.exSuffixes(m, cst.KindCharacterName, true)
		}
		p.open(cst.KindLiteralExpr)
		p.bump()
		p.close()
		return cst.KindLiteralExpr
	case token.BitStringLiteral, token.KwNull:
		p.open(cst.KindLiteralExpr)
		p.bump()
		p.close()
		return cst.KindLiteralExpr
	case token.LParen:
		return p.exParen()
	case token.KwNew:
		return p.exAllocator()
	case token.LtLt:
		p.exExternal()
		return p.exSuffixes(m, cst.KindExternalName, true)
	case token.KwUnaffected:
		p.open(cst.KindUnaffectedExpr)
		p.bump()
		p.close()
		return cst.KindUnaffectedExpr
	}
	if !p.atIdent() {
		return 0
	}
	p.open(cst.KindSimpleName)
	p.identOpt()
	p.close()
	return p.exSuffixes(m, cst.KindSimpleName, true)
}

// parseNameOrAggregate parses a name or an aggregate (or parenthesized
// expression): the forms of an assignment target. False, consuming
// nothing, if neither starts here.
func (p *Parser) parseNameOrAggregate() bool {
	if p.at(token.LParen) {
		return p.exParen() != 0
	}
	return p.parseName()
}

// exCondTail parses the `when cond {else expr when cond} [else expr]`
// after the expression that started at m and wraps it all into a
// ConditionalExpr (VHDL-2019, cond_expr).
func (p *Parser) exCondTail(m cst.Checkpoint) cst.Kind {
	p.exGateTok("cond_expr")
	p.bump() // when
	p.exRequire(p.exExpr)
	p.exWrap(m, cst.KindWhenBranch)
	for p.at(token.KwElse) {
		p.bump()
		mb := p.mark()
		p.exRequire(p.exExpr)
		if !p.at(token.KwWhen) {
			p.exWrap(mb, cst.KindWhenBranch)
			break
		}
		p.bump()
		p.exRequire(p.exExpr)
		p.exWrap(mb, cst.KindWhenBranch)
	}
	p.exWrap(m, cst.KindConditionalExpr)
	return cst.KindConditionalExpr
}

// exOrRange parses an expression, then the tails that make it a range
// or a subtype indication: `to`/`downto` (RangeExpr), `range ...` after
// a name (SubtypeIndication with a RangeConstraint), and with f the
// optional `when` and resolution-function tails.
func (p *Parser) exOrRange(f exFlags) cst.Kind {
	m := p.mark()
	k := p.exExpr()
	if k == 0 {
		return 0
	}
	switch p.nth(0) {
	case token.KwTo, token.KwDownto:
		p.bump()
		p.exRequire(p.exSimple)
		p.exWrap(m, cst.KindRangeExpr)
		return cst.KindRangeExpr
	case token.KwRange:
		if exIsName(k) {
			p.parseConstraint()
			p.exWrap(m, cst.KindSubtypeIndication)
			return cst.KindSubtypeIndication
		}
	case token.KwWhen:
		if f&exWhen != 0 {
			return p.exCondTail(m)
		}
	}
	if f&exSubtype != 0 && exIsName(k) && p.nthIsIdent(0) {
		// resolved std_logic: the first name is a resolution function.
		p.exWrap(m, cst.KindResolutionFunction)
		p.exTypeMark()
		p.parseConstraint()
		p.exWrap(m, cst.KindSubtypeIndication)
		return cst.KindSubtypeIndication
	}
	return k
}

// ------------------------------------------------------------- names

// parseName parses a name (simple, selected, indexed/call, slice,
// attribute, external, operator symbol, character literal) into a node
// of the Name group; false, consuming nothing, if none starts here.
// Unlike a name in an expression it does not take a qualified
// expression (`t'(...)`) or a VHDL-2019 `generic map` call suffix, so
// statement builders can tell `u : c generic map (...)` apart.
func (p *Parser) parseName() bool {
	m := p.mark()
	switch {
	case p.at(token.StringLiteral):
		p.open(cst.KindOperatorSymbol)
		p.bump()
		p.close()
		p.exSuffixes(m, cst.KindOperatorSymbol, false)
	case p.at(token.CharacterLiteral):
		p.open(cst.KindCharacterName)
		p.bump()
		p.close()
		p.exSuffixes(m, cst.KindCharacterName, false)
	case p.at(token.LtLt):
		p.exExternal()
		p.exSuffixes(m, cst.KindExternalName, false)
	case p.atIdent():
		p.open(cst.KindSimpleName)
		p.identOpt()
		p.close()
		p.exSuffixes(m, cst.KindSimpleName, false)
	default:
		return false
	}
	return true
}

// exSuffixes parses the suffixes of the name of kind k that started at
// m: .suffix, (...), [signature]'attr, 'attr [(expr)], and with inExpr
// a qualified expression and the VHDL-2019 generic map call. It returns
// the kind of the final node.
func (p *Parser) exSuffixes(m cst.Checkpoint, k cst.Kind,
	inExpr bool,
) cst.Kind {
	for {
		switch p.nth(0) {
		case token.Dot:
			p.bump()
			switch {
			case p.atIdent():
				p.identOpt()
			case p.at(token.KwAll), p.at(token.CharacterLiteral),
				p.at(token.StringLiteral):
				p.bump()
			default:
				p.errorExpected("an identifier, character literal, " +
					"operator symbol or all")
				p.missing(token.Identifier)
			}
			p.exWrap(m, cst.KindSelectedName)
			k = cst.KindSelectedName
		case token.LParen:
			k = p.exParenSuffix(m)
		case token.LBracket:
			if !p.exSignatureAhead() {
				return k
			}
			p.parseSignature()
			if !p.at(token.Tick) || !p.exAttrAhead() {
				p.errorExpected("an attribute designator")
				return k
			}
			k = p.exAttribute(m, true)
		case token.Tick:
			switch {
			case p.nth(1) == token.LParen:
				if !inExpr {
					return k
				}
				p.bump()
				p.exParen()
				p.exWrap(m, cst.KindQualifiedExpr)
				return cst.KindQualifiedExpr
			case p.exAttrAhead():
				k = p.exAttribute(m, true)
			default:
				return k
			}
		case token.KwGeneric:
			if !inExpr || !p.nthIsKw(1, token.KwMap) {
				return k
			}
			k = p.exCallMaps(m)
		case token.KwParameter, token.Identifier:
			// parameter is an identifier before 2008.
			if !p.atKw(token.KwParameter) || !p.nthIsKw(1, token.KwMap) {
				return k
			}
			k = p.exCallMaps(m)
		default:
			return k
		}
	}
}

// exAttrAhead reports whether the tick at the current token starts an
// attribute designator.
func (p *Parser) exAttrAhead() bool { return p.exAttrAheadAt(0) }

// exAttrAheadAt is exAttrAhead for a tick at the nth visible token.
func (p *Parser) exAttrAheadAt(n int) bool {
	switch p.nth(n + 1) {
	case token.KwRange, token.KwSubtype, token.KwRecord:
		return true
	}
	return p.nthIsIdent(n + 1)
}

// exCharNameAhead reports whether the character literal here is the
// prefix of an attribute name: a signature then a tick, or a tick and
// an attribute designator.
func (p *Parser) exCharNameAhead() bool {
	switch p.nth(1) {
	case token.LBracket:
		return p.exSignatureAheadAt(1)
	case token.Tick:
		return p.exAttrAheadAt(1)
	}
	return false
}

// exAttribute parses ' designator [( expression )] (the argument only
// with arg) and wraps the name (and signature) since m into an
// AttributeName.
func (p *Parser) exAttribute(m cst.Checkpoint, arg bool) cst.Kind {
	p.bump() // '
	switch p.nth(0) {
	case token.KwRange:
		p.bump()
	case token.KwSubtype:
		p.exGateTok("subtype_attr")
		p.bump()
	case token.KwRecord:
		p.exGateTok("record_attr")
		p.bump()
	default:
		p.identOpt()
	}
	if arg && p.at(token.LParen) && !p.exIndexAhead() {
		p.bump()
		p.exRequire(p.exExpr)
		if !p.at(token.RParen) {
			p.errorExpected("')'")
			p.exSkip()
		}
		p.exExpect(token.RParen)
	}
	p.exWrap(m, cst.KindAttributeName)
	return cst.KindAttributeName
}

// exIndexAhead reports whether the `(` here holds more than one
// element or a range (PT'AT2(2, 3), PT'AT1(2 to 4)): then it indexes or
// slices the attribute's value instead of being its argument. The scan
// is bounded; past the bound it is taken as an argument.
func (p *Parser) exIndexAhead() bool {
	depth := 0
	for i := 0; i < 64; i++ {
		switch p.nth(i) {
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 {
				return false
			}
		case token.Comma, token.KwTo, token.KwDownto:
			if depth == 1 {
				return true
			}
		case token.Semicolon, token.EOF:
			return false
		}
	}
	return false
}

// exCallMaps parses the VHDL-2019 [generic map (...)] [parameter map
// (...)] [(...)] of a subprogram call and wraps it into a
// CallOrIndexName.
func (p *Parser) exCallMaps(m cst.Checkpoint) cst.Kind {
	if p.atKw(token.KwGeneric) {
		p.exGateTok("subp_generic_map_call")
		p.open(cst.KindCallGenericMap)
		p.bumpKw(token.KwGeneric)
		p.expectKw(token.KwMap)
		p.parseAssociationList()
		p.close()
	}
	if p.atKw(token.KwParameter) && p.nthIsKw(1, token.KwMap) {
		p.exGateTok("param_map")
		p.open(cst.KindParameterMapAspect)
		p.bumpAs(token.KwParameter) // param_map covers the keyword gate
		p.bumpKw(token.KwMap)
		p.parseAssociationList()
		p.close()
	}
	if p.at(token.LParen) {
		p.parseAssociationList()
	}
	p.exWrap(m, cst.KindCallOrIndexName)
	return cst.KindCallOrIndexName
}

// exParenSuffix parses the ( ... ) after the name that started at m:
// a SliceName when the only element is a range, else a CallOrIndexName
// with an AssociationList (sema decides call, index or conversion).
func (p *Parser) exParenSuffix(m cst.Checkpoint) cst.Kind {
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return cst.KindErrorNode
	}
	defer p.leave()
	ml := p.mark()
	p.bump() // (
	me := p.mark()
	k := p.exActual(exWhen)
	if (k == cst.KindRangeExpr || k == cst.KindSubtypeIndication) &&
		p.at(token.RParen) {
		p.bump()
		p.exWrap(m, cst.KindSliceName)
		return cst.KindSliceName
	}
	p.exAssocTail(me, k)
	for p.exListSep() {
		p.exAssocElement()
	}
	p.exExpect(token.RParen)
	p.exWrap(ml, cst.KindAssociationList)
	p.exWrap(m, cst.KindCallOrIndexName)
	return cst.KindCallOrIndexName
}

// exListSep ends one element of a parenthesized list: it consumes a
// comma and reports true when another element follows. Junk before the
// next `,` or `)` is skipped into an ErrorNode.
func (p *Parser) exListSep() bool {
	if p.eat(token.Comma) {
		return true
	}
	if p.at(token.RParen) || p.atEOF() {
		return false
	}
	p.errorExpected("',' or ')'")
	p.exSkip()
	return p.eat(token.Comma)
}

// exSkip skips balanced tokens into an ErrorNode up to a `,`, an
// unmatched `)` or a token of exParenStop.
func (p *Parser) exSkip() {
	if p.atEOF() || p.at(token.RParen) || p.atAny(exParenStop) {
		return
	}
	p.skipBalanced(cst.KindErrorNode, exParenStop)
}

// parseSignature parses `[ [type_mark {, type_mark}] [return
// type_mark] ]` into a Signature node; false if no `[` is here.
func (p *Parser) parseSignature() bool {
	if !p.at(token.LBracket) {
		return false
	}
	p.open(cst.KindSignature)
	p.bump()
	if p.atIdent() {
		for {
			p.exTypeMark()
			if !p.eat(token.Comma) {
				break
			}
		}
	}
	if p.eatKw(token.KwReturn) {
		p.exTypeMark()
	}
	p.exExpect(token.RBracket)
	p.close()
	return true
}

// exSignatureAhead reports whether the `[` here starts a signature
// followed by an attribute tick (f[integer return bit]'path_name): a
// bounded scan, so PSL brackets after a name are never taken.
func (p *Parser) exSignatureAhead() bool { return p.exSignatureAheadAt(0) }

// exSignatureAheadAt is exSignatureAhead for a `[` at the nth visible
// token.
func (p *Parser) exSignatureAheadAt(n int) bool {
	for i := n + 1; i < n+64; i++ {
		switch p.nth(i) {
		case token.Identifier, token.ExtendedIdentifier, token.Dot,
			token.Comma, token.KwReturn, token.StringLiteral,
			token.CharacterLiteral:
		case token.RBracket:
			return p.nth(i+1) == token.Tick
		default:
			return false
		}
	}
	return false
}

// exTypeMark parses a type mark: a selected name, optionally with an
// attribute (`x'subtype`, `t'base`, `a'element`).
func (p *Parser) exTypeMark() {
	if !p.atIdent() {
		p.errorExpected("a type mark")
		p.open(cst.KindSimpleName)
		p.missing(token.Identifier)
		p.close()
		return
	}
	m := p.mark()
	p.parseSelectedName()
	if p.at(token.Tick) && p.exAttrAhead() {
		p.exAttribute(m, false)
	}
}

// ---------------------------------------------- external names (2008)

// exExternal parses << class path : subtype >> into an ExternalName.
func (p *Parser) exExternal() {
	p.open(cst.KindExternalName)
	p.exGateTok("ext_name")
	p.bump() // <<
	switch p.nth(0) {
	case token.KwConstant, token.KwSignal, token.KwVariable:
		p.bump()
	default:
		p.errorExpected("constant, signal or variable")
		p.missing(token.KwSignal)
	}
	nprob := p.nprob()
	switch p.nth(0) {
	case token.At:
		// @ library . package . { package . } object
		p.open(cst.KindPackagePathname)
		p.bump()
		p.ident()
		for range 2 {
			p.exExpect(token.Dot)
			p.ident()
		}
		for p.eat(token.Dot) {
			p.ident()
		}
		p.close()
	case token.Dot:
		p.open(cst.KindAbsolutePathname)
		p.bump()
		p.exPartialPath()
		p.close()
	default:
		p.open(cst.KindRelativePathname)
		for p.eat(token.Caret) {
			p.exExpect(token.Dot)
		}
		p.exPartialPath()
		p.close()
	}
	if !p.at(token.Colon) && !p.exQuiet() {
		// Report once (the path may have reported already), then skip
		// to the `:` so the subtype still parses.
		if p.nprob() == nprob {
			p.errorExpected("':'")
		}
		p.recover(exExternalStop)
	}
	if p.eat(token.Colon) {
		if !p.parseSubtypeIndication() {
			p.errorExpected("a subtype indication")
			p.open(cst.KindSubtypeIndication)
			p.open(cst.KindSimpleName)
			p.missing(token.Identifier)
			p.close()
			p.close()
		}
	} else {
		p.missing(token.Colon)
	}
	p.exExpect(token.GtGt)
	p.close()
}

// exPartialPath parses {pathname_element .} object_simple_name.
func (p *Parser) exPartialPath() {
	for {
		if !p.atIdent() {
			p.ident()
			return
		}
		next := p.nth(1)
		if next != token.Dot && next != token.LParen {
			p.ident()
			return
		}
		p.open(cst.KindPathnameElement)
		p.ident()
		if p.eat(token.LParen) {
			p.exRequire(p.exExpr)
			p.exExpect(token.RParen)
		}
		p.exExpect(token.Dot)
		p.close()
	}
}

// ----------------------------------------------- aggregates, allocators

// exParen parses ( ... ) into a ParenExpr (one element without choices)
// or an Aggregate of ElementAssociation nodes.
func (p *Parser) exParen() cst.Kind {
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return cst.KindErrorNode
	}
	defer p.leave()
	m := p.mark()
	p.bump() // (
	kind := cst.KindAggregate
	for n := 0; ; n++ {
		me := p.mark()
		k := p.exChoice(exWhen)
		if p.at(token.Bar) || p.at(token.Arrow) {
			for p.eat(token.Bar) {
				if p.exChoice(0) == 0 {
					p.exMissing()
				}
			}
			p.exWrap(me, cst.KindChoices)
			p.exExpect(token.Arrow)
			p.exRequire(p.exValue)
			p.exWrap(me, cst.KindElementAssociation)
		} else {
			switch k {
			case 0:
				p.exMissing()
			case cst.KindOthersChoice:
				p.errorExpected("'=>'")
			}
			if n == 0 && p.at(token.RParen) && k != cst.KindOthersChoice {
				kind = cst.KindParenExpr
			} else {
				p.exWrap(me, cst.KindElementAssociation)
			}
		}
		if !p.exListSep() {
			break
		}
	}
	p.exExpect(token.RParen)
	p.exWrap(m, kind)
	return kind
}

// exValue parses an element value: an expression, VHDL-2019
// conditional expressions included.
func (p *Parser) exValue() cst.Kind {
	m := p.mark()
	k := p.exExpr()
	if k != 0 && p.at(token.KwWhen) {
		return p.exCondTail(m)
	}
	return k
}

// exChoice parses one choice: others, or an expression, range or
// discrete subtype indication.
func (p *Parser) exChoice(f exFlags) cst.Kind {
	if p.at(token.KwOthers) {
		p.open(cst.KindOthersChoice)
		p.bump()
		p.close()
		return cst.KindOthersChoice
	}
	return p.exOrRange(f)
}

// parseChoices parses `choice {| choice}` into a Choices node (case
// alternatives, selected assignments, aggregates); false, consuming
// nothing, if no choice starts here.
func (p *Parser) parseChoices() bool {
	m := p.mark()
	if p.exChoice(0) == 0 {
		return false
	}
	for p.eat(token.Bar) {
		if p.exChoice(0) == 0 {
			p.exMissing()
		}
	}
	p.exWrap(m, cst.KindChoices)
	return true
}

// exAllocator parses new subtype_indication | new qualified_expression.
func (p *Parser) exAllocator() cst.Kind {
	p.open(cst.KindAllocator)
	p.bump() // new
	m := p.mark()
	switch {
	case p.atIdent():
		p.parseSelectedName()
		if p.at(token.Tick) && p.nth(1) == token.LParen {
			p.bump()
			p.exParen()
			p.exWrap(m, cst.KindQualifiedExpr)
			break
		}
		p.parseConstraint()
		p.exWrap(m, cst.KindSubtypeIndication)
	case p.at(token.LParen):
		p.parseSubtypeIndication()
	default:
		p.errorExpected("a subtype indication or qualified expression")
		p.missing(token.Identifier)
	}
	p.close()
	return cst.KindAllocator
}

// ------------------------------------------------- association lists

// parseAssociationList parses `( association_element {,
// association_element} )` into an AssociationList node; the parentheses
// are part of the node. Without a `(` it reports it missing.
func (p *Parser) parseAssociationList() {
	p.open(cst.KindAssociationList)
	if p.exExpect(token.LParen) {
		if p.enter() {
			for {
				p.exAssocElement()
				if !p.exListSep() {
					break
				}
			}
		} else {
			p.tooDeep()
		}
		p.leave()
		p.exExpect(token.RParen)
	}
	p.close()
}

// exAssocElement parses [formal =>] actual into an AssociationElement.
func (p *Parser) exAssocElement() {
	me := p.mark()
	p.exAssocTail(me, p.exActual(exWhen))
}

// exAssocTail finishes the association element that started at me
// with a first part of kind k (0: missing).
func (p *Parser) exAssocTail(me cst.Checkpoint, k cst.Kind) {
	if k == 0 {
		p.exMissing()
	}
	if p.eat(token.Arrow) {
		if p.exActual(exSubtype|exWhen) == 0 {
			p.exMissing()
		}
	}
	p.exWrap(me, cst.KindAssociationElement)
}

// exActual parses an actual part (or a formal): open, inertial expr
// (2008), <>, default (interface package maps), an expression, a range
// or a subtype indication; with exSubtype (after `=>`) also a subtype
// indication with a resolution function (`t => resolved std_logic`).
func (p *Parser) exActual(f exFlags) cst.Kind {
	switch p.nth(0) {
	case token.KwOpen:
		p.open(cst.KindOpenExpr)
		p.bump()
		p.close()
		return cst.KindOpenExpr
	case token.KwInertial:
		p.open(cst.KindInertialActual)
		p.exGateTok("inertial_actual")
		p.bump()
		p.exRequire(p.exExpr)
		p.close()
		return cst.KindInertialActual
	case token.LBrace:
		if p.inPSL() {
			// nondet({1, 3 to 7, 9}): a PSL value set.
			p.parsePSLValueSet()
			return cst.KindPSLValueSet
		}
	case token.Box, token.KwDefault:
		// generic map (<>) and generic map (default) of an interface
		// package; `default` has no node kind of its own yet.
		p.open(cst.KindBoxExpr)
		p.bump()
		p.close()
		return cst.KindBoxExpr
	}
	return p.exOrRange(f)
}
