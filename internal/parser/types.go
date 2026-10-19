package parser

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Type definitions, subtype indications and constraints (builder:
// types), 1076-2019 §5 and §6.3.

// typeStop ends the recovery of a broken range or constraint.
var typeStop = token.NewSet(token.Semicolon, token.Comma, token.RParen,
	token.KwIs, token.KwOpen, token.VarAssign, token.KwGenerate,
	token.KwLoop, token.KwThen)

// maxLookahead bounds the token scans that tell constraint forms apart.
const maxLookahead = 64

// parseTypeDefinition parses the definition after `type t is` into one
// of the TypeDef group's nodes (including protected types and bodies
// and the 2019 protected type instantiation, see v2019.go). It returns
// false, consuming nothing, if no definition starts here.
func (p *Parser) parseTypeDefinition() bool {
	ok, _ := p.typeDefinition()
	return ok
}

// typeDefinition is parseTypeDefinition; ended reports that the
// definition ended with its own end clause, `;` included (physical,
// record and protected types).
func (p *Parser) typeDefinition() (ok, ended bool) {
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return true, true
	}
	defer p.leave()
	switch p.kwAt(0) {
	case token.LParen:
		p.parseEnumerationDef()
	case token.KwRange:
		return true, p.parseRangeOrPhysicalDef()
	case token.KwArray:
		p.parseArrayDef()
	case token.KwRecord:
		p.parseRecordDef()
		return true, true
	case token.KwAccess:
		p.open(cst.KindAccessTypeDef)
		p.bumpKw(token.KwAccess)
		p.declSubtype()
		p.close()
	case token.KwFile:
		p.open(cst.KindFileTypeDef)
		p.bumpKw(token.KwFile)
		p.expectKw(token.KwOf)
		p.declTypeMark()
		p.close()
	case token.KwProtected:
		p.parseProtectedDef()
		return true, true
	case token.KwNew:
		return p.parseProtectedTypeInstantiation(), false
	default:
		return false, false
	}
	return true, false
}

// parseEnumerationDef parses `( literal {, literal} )`.
func (p *Parser) parseEnumerationDef() {
	p.open(cst.KindEnumerationTypeDef)
	p.bump()
	for {
		switch {
		case p.at(token.CharacterLiteral):
			p.bump()
		case p.atIdent():
			p.ident()
		default:
			p.errorExpected("an enumeration literal")
			p.recover(token.NewSet(token.Comma, token.RParen,
				token.Semicolon))
		}
		if !p.eat(token.Comma) {
			break
		}
	}
	p.expect(token.RParen)
	p.close()
}

// parseRangeConstraint parses `range range` into a RangeConstraint.
func (p *Parser) parseRangeConstraint() {
	p.open(cst.KindRangeConstraint)
	p.bumpKw(token.KwRange)
	p.requireRange()
	p.close()
}

// requireRange parses a required discrete range.
func (p *Parser) requireRange() {
	if !p.parseDiscreteRange() {
		p.errorExpected("a range")
		p.recover(typeStop)
	}
}

// parseRangeOrPhysicalDef parses an integer or floating type definition
// (`range r`) or a physical one (`range r units ... end units [t];`).
// It reports whether the definition ended with its end clause.
func (p *Parser) parseRangeOrPhysicalDef() bool {
	m := p.mark()
	p.parseRangeConstraint()
	if !p.atKw(token.KwUnits) {
		p.openAt(m, cst.KindRangeTypeDef)
		p.close()
		return false
	}
	p.openAt(m, cst.KindPhysicalTypeDef)
	p.bumpKw(token.KwUnits)
	p.open(cst.KindPrimaryUnitDecl)
	p.ident()
	p.expect(token.Semicolon)
	p.close()
	item := func() bool {
		if !p.atIdent() {
			return false
		}
		p.open(cst.KindSecondaryUnitDecl)
		p.ident()
		p.expect(token.Eq)
		p.declExpr()
		p.expect(token.Semicolon)
		p.close()
		return true
	}
	for {
		if p.inactive(item) {
			continue
		}
		if p.atEOF() || p.atKw(token.KwEnd) {
			break
		}
		if !item() {
			p.error("expected a secondary unit declaration, found " +
				p.describeCurrent())
			p.recoverItem(endOnly)
		}
	}
	p.endClause(true, token.KwUnits)
	p.close()
	return true
}

// atIndexSubtypeDef reports whether `type_mark range <>` starts at the
// nth visible token.
func (p *Parser) atIndexSubtypeDef(n int) bool {
	for i := n; i < n+maxLookahead; i += 2 {
		if !p.nthIsIdent(i) {
			return false
		}
		if p.nth(i+1) != token.Dot {
			return p.nthIsKw(i+1, token.KwRange) &&
				p.nth(i+2) == token.Box
		}
	}
	return false
}

// parseArrayDef parses an unbounded (`array (t range <>, ...) of s`) or
// constrained (`array (r, ...) of s`) array definition.
func (p *Parser) parseArrayDef() {
	m := p.mark()
	p.bumpKw(token.KwArray)
	if p.at(token.LParen) && p.atIndexSubtypeDef(1) {
		p.openAt(m, cst.KindUnboundedArrayDef)
		p.bump()
		for {
			p.open(cst.KindIndexSubtypeDef)
			p.declTypeMark()
			p.expectKw(token.KwRange)
			p.expect(token.Box)
			p.close()
			if !p.eat(token.Comma) {
				break
			}
		}
		p.expect(token.RParen)
	} else {
		p.openAt(m, cst.KindConstrainedArrayDef)
		if p.at(token.LParen) {
			p.open(cst.KindArrayConstraint)
			p.indexConstraint()
			p.close()
		} else {
			p.errorExpected("'('")
		}
	}
	p.expectKw(token.KwOf)
	p.declSubtype()
	p.close()
}

// indexConstraint parses `( discrete_range {, discrete_range} )` at the
// current `(` into the open node.
func (p *Parser) indexConstraint() {
	p.bump()
	for {
		p.requireRange()
		if !p.eat(token.Comma) {
			break
		}
	}
	p.expect(token.RParen)
}

// parseRecordDef parses `record {element_declaration} end record
// [name];` (an empty record is 2019).
func (p *Parser) parseRecordDef() {
	p.open(cst.KindRecordTypeDef)
	p.bumpKw(token.KwRecord)
	item := func() bool {
		if !p.atIdent() {
			return false
		}
		p.open(cst.KindElementDecl)
		p.declIdentList()
		p.expect(token.Colon)
		p.declSubtype()
		p.expect(token.Semicolon)
		p.close()
		return true
	}
	n := 0
	for {
		if p.inactive(item) {
			n++
			continue
		}
		if p.atEOF() || p.atKw(token.KwEnd) {
			break
		}
		if item() {
			n++
			continue
		}
		p.error("expected an element declaration, found " +
			p.describeCurrent())
		p.recoverItem(endOnly)
	}
	if n == 0 {
		s, e := p.curSpan()
		p.gateRule("empty_record", s, e)
	}
	p.endClause(true, token.KwRecord)
	p.close()
}

// parseProtectedDef parses a protected type declaration `protected
// [header] decls end protected [name];` or body `protected body decls
// end protected body [name];` (2002; the 2019 header via
// parseProtectedTypeHeader).
func (p *Parser) parseProtectedDef() {
	// prot_type (rules.csv) is the gate; it replaces the keyword
	// diagnostic of `protected` so the construct is reported once.
	s, e := p.curSpan()
	if p.nthIsKw(1, token.KwBody) {
		p.gateRule("prot_type", s, e)
		p.open(cst.KindProtectedTypeBody)
		p.bumpAs(token.KwProtected)
		p.bumpKw(token.KwBody)
		p.declarativePart(RegionProtectedBody, endOnly)
		p.endClause(true, token.KwProtected, token.KwBody)
		p.close()
		return
	}
	p.gateRule("prot_type", s, e)
	p.open(cst.KindProtectedTypeDef)
	p.bumpAs(token.KwProtected)
	if p.atKw(token.KwGeneric) {
		p.parseProtectedTypeHeader()
	}
	p.declarativePart(RegionProtected, endOnly)
	p.endClause(true, token.KwProtected)
	p.close()
}

// ------------------------------------------------- subtype indications

// declTypeMark parses a type mark: a selected name, optionally followed
// by type attributes ('subtype (2008), 'base, 'element, 'record
// (2019), chained as in m'element'element).
func (p *Parser) declTypeMark() {
	if !p.atIdent() {
		p.errorExpected("a type mark")
		p.missing(token.Identifier)
		return
	}
	if p.atIndexedTypeMark() {
		p.parseName()
		return
	}
	m := p.mark()
	p.parseSelectedName()
	for p.at(token.Tick) {
		gate := ""
		switch {
		case p.nthIsKw(1, token.KwSubtype):
			gate = "subtype_attr"
		case p.nthIsKw(1, token.KwRecord):
			gate = "record_attr"
		case p.nthIsIdent(1):
		case p.nthIsKw(1, token.KwRange) && p.nth(2) == token.Tick:
			// t'range'record (2019)
		default:
			return
		}
		p.openAt(m, cst.KindAttributeName)
		p.bump()
		s, e := p.curSpan()
		if gate != "" {
			p.gateRule(gate, s, e)
		}
		if p.atIdent() {
			p.ident()
		} else {
			p.bump()
		}
		p.close()
	}
}

// atIndexedTypeMark reports whether a type mark of the 2008 form
// `name(index)...'subtype` (or 'element) starts here: a selected name,
// parenthesized groups, then the attribute. Such a prefix is a general
// name, so the expression parser builds it.
func (p *Parser) atIndexedTypeMark() bool {
	i := 0
	for p.nth(i+1) == token.Dot && i < maxLookahead {
		i += 2
	}
	i++
	if p.nth(i) != token.LParen {
		return false
	}
	depth := 0
	for end := i + maxLookahead; i < end; i++ {
		switch p.nth(i) {
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 && p.nth(i+1) != token.LParen {
				return p.nth(i+1) == token.Tick &&
					(p.nthIsKw(i+2, token.KwSubtype) ||
						p.nthIsIdent(i+2))
			}
		case token.EOF, token.Semicolon:
			return false
		}
	}
	return false
}

// parseSubtypeIndication parses `[resolution] type_mark [constraint]`
// into a SubtypeIndication node. The resolution is a function name or
// (2008) a parenthesized element or record resolution.
func (p *Parser) parseSubtypeIndication() bool {
	if !p.atIdent() && !p.at(token.LParen) {
		return false
	}
	p.open(cst.KindSubtypeIndication)
	if p.at(token.LParen) {
		p.resolutionIndication()
		p.declTypeMark()
	} else {
		m := p.mark()
		p.declTypeMark()
		if p.atIdent() {
			// The first name was a resolution function.
			p.openAt(m, cst.KindResolutionFunction)
			p.close()
			p.declTypeMark()
		}
	}
	p.parseConstraint()
	p.close()
	return true
}

// resolutionIndication parses a resolution function name or (2008) `(
// resolution_indication )` or `( element resolution_indication {, ...}
// )`.
func (p *Parser) resolutionIndication() {
	if !p.at(token.LParen) {
		p.open(cst.KindResolutionFunction)
		p.declTypeMark()
		p.close()
		return
	}
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return
	}
	defer p.leave()
	s, e := p.curSpan()
	p.gateRule("elem_resolution", s, e)
	if p.nthIsIdent(1) && (p.nthIsIdent(2) || p.nth(2) == token.LParen) {
		p.open(cst.KindRecordResolution)
		p.bump()
		for {
			p.open(cst.KindRecordElementResolution)
			p.ident()
			p.resolutionIndication()
			p.close()
			if !p.eat(token.Comma) {
				break
			}
		}
	} else {
		p.open(cst.KindElementResolution)
		p.bump()
		p.resolutionIndication()
	}
	p.expect(token.RParen)
	p.close()
}

// parseConstraint parses a range, array or record constraint into a
// node of the Constraint group; false if none starts here. An array
// constraint may be followed by an element constraint, and (2008) its
// index constraint may be `(open)`.
func (p *Parser) parseConstraint() bool {
	switch {
	case p.atKw(token.KwRange):
		p.parseRangeConstraint()
		return true
	case !p.at(token.LParen):
		return false
	}
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return true
	}
	defer p.leave()
	if p.atRecordConstraint() {
		p.open(cst.KindRecordConstraint)
		s, e := p.curSpan()
		p.gateRule("record_constraint", s, e)
		p.bump()
		for {
			p.open(cst.KindRecordElementConstraint)
			p.ident()
			if !p.parseConstraint() {
				p.errorExpected("an element constraint")
			}
			p.close()
			if !p.eat(token.Comma) {
				break
			}
		}
		p.expect(token.RParen)
		p.close()
		return true
	}
	p.open(cst.KindArrayConstraint)
	if p.nthIsKw(1, token.KwOpen) {
		p.bump()
		s, e := p.curSpan()
		p.gateRule("record_constraint", s, e)
		p.open(cst.KindOpenExpr)
		p.bumpKw(token.KwOpen)
		p.close()
		p.expect(token.RParen)
	} else {
		p.indexConstraint()
	}
	if p.at(token.LParen) {
		s, e := p.curSpan()
		p.gateRule("record_constraint", s, e)
		p.parseConstraint()
	}
	p.close()
	return true
}

// atRecordConstraint reports whether the `(` here starts a record
// constraint: `( element ( ... ) {( ... )}` followed by `,` or `)`. An
// index constraint element of that form would be a call or an indexed
// name, which cannot be a discrete range on its own.
func (p *Parser) atRecordConstraint() bool {
	if !p.nthIsIdent(1) || p.nth(2) != token.LParen {
		return false
	}
	depth := 0
	for i := 2; i < maxLookahead; i++ {
		switch p.nth(i) {
		case token.LParen:
			depth++
		case token.RParen:
			depth--
			if depth == 0 {
				switch p.nth(i + 1) {
				case token.Comma, token.RParen:
					return true
				case token.LParen: // f(1)(2): an element constraint
				default:
					return false
				}
			}
		case token.EOF, token.Semicolon:
			return false
		}
	}
	return false
}

// parseDiscreteRange parses a discrete range: `a to|downto b`
// (RangeExpr), a discrete subtype indication `t range r`, or a range
// attribute or type name (as the expression parser builds it). False,
// consuming nothing, if no expression starts here.
func (p *Parser) parseDiscreteRange() bool {
	m := p.mark()
	if !p.parseExpression() {
		return false
	}
	switch {
	case p.atKw(token.KwTo) || p.atKw(token.KwDownto):
		p.openAt(m, cst.KindRangeExpr)
		p.bump()
		p.declExpr()
		p.close()
	case p.atKw(token.KwRange):
		p.openAt(m, cst.KindSubtypeIndication)
		p.parseRangeConstraint()
		p.close()
	}
	return true
}
