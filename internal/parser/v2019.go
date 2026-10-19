package parser

import (
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
)

// VHDL-2019 constructs (builder: v2019) and the node-kind gate engine.
//
// The decl, types, seq and expr builders call these at the points where
// the 2019 grammar applies; each returns false, consuming nothing, when
// its construct does not start here. Every construct parses under every
// standard into the same tree; under an older standard it only adds
// syntax/standard (#7, #31). A 2019 word lexed as an identifier before
// 2019 (view, private) is consumed as the keyword without the separate
// keyword diagnostic: the construct gate is the one report.
//
// Call points:
//   - parseModeViewDecl: at the start of a declarative item.
//   - parseModeViewIndication: at the mode of an interface object (after
//     `names :`).
//   - parsePrivateVariableDecl: at an item of a protected type
//     declaration.
//   - parseIncompleteTypeDef: after `type t is` in a generic list.
//   - parseAnonymousTypeIndication: at the subtype of an interface object
//     (`type is ...`) and inside incomplete type definitions.
//   - parseProtectedTypeHeader: right after `protected`.
//   - parseProtectedTypeInstantiation: after `type t is` at `new`.
//   - parseSequentialBlock: at the start of a sequential statement,
//     before any label (it parses `[label :] block`).

// ruleIDForKind is the gate engine's table: the rules.csv rule that a
// node of kind k is an occurrence of, for the rules that are a pure
// function of the node kind. Rules decided by a token inside a node
// (process (all), case?, the matching operators, ...) are gated by their
// builders with gateRule at that token.
func ruleIDForKind(k cst.Kind) string {
	switch k {
	case cst.KindProtectedTypeDef, cst.KindProtectedTypeBody:
		return "prot_type"
	case cst.KindInterfaceTypeDecl:
		return "generic_type"
	case cst.KindInterfaceSubprogramDecl:
		return "generic_subp"
	case cst.KindInterfacePackageDecl:
		return "generic_pkg"
	case cst.KindPackageInstantiation:
		return "pkg_inst"
	case cst.KindSubprogramInstantiation:
		return "subp_inst"
	case cst.KindRecordConstraint:
		return "record_constraint"
	case cst.KindExternalName:
		return "ext_name"
	case cst.KindForceAssignStmt:
		return "force"
	case cst.KindReleaseAssignStmt:
		return "release"
	case cst.KindElementResolution:
		return "elem_resolution"
	case cst.KindCaseGenerateStmt:
		return "gen_elsif_else_case"
	case cst.KindInertialActual:
		return "inertial_actual"
	case cst.KindModeViewDecl, cst.KindModeViewIndication,
		cst.KindArrayModeViewIndication:
		return "mode_view"
	case cst.KindPrivateVariableDecl:
		return "private_var"
	case cst.KindPrivateIncompleteTypeDef,
		cst.KindScalarIncompleteTypeDef,
		cst.KindDiscreteIncompleteTypeDef,
		cst.KindIntegerIncompleteTypeDef,
		cst.KindPhysicalIncompleteTypeDef,
		cst.KindFloatingIncompleteTypeDef, cst.KindArrayIncompleteTypeDef,
		cst.KindAccessIncompleteTypeDef, cst.KindFileIncompleteTypeDef:
		return "incomplete_generic"
	case cst.KindAnonymousTypeIndication:
		return "anon_type"
	case cst.KindSequentialBlockStmt:
		return "seq_block"
	case cst.KindConditionalExpr:
		return "cond_expr"
	case cst.KindConditionalReturnStmt:
		return "cond_return"
	case cst.KindParameterMapAspect:
		return "param_map"
	case cst.KindCallGenericMap:
		return "subp_generic_map_call"
	}
	return ""
}

// RuleForKind returns the rules.csv rule whose construct every node of
// kind k is, if the rule is a pure function of the node kind (#7).
// Under standard S the parser reports syntax/standard with the rule's
// construct text once per such node exactly when rule.First > S.
func RuleForKind(k cst.Kind) (Rule, bool) {
	id := ruleIDForKind(k)
	if id == "" {
		return Rule{}, false
	}
	return RuleByID(id)
}

// gateKind reports the gate of a node of kind k (RuleForKind) on
// [start, end); builders call it once per node they open.
func (p *Parser) gateKind(k cst.Kind, start, end int) {
	if id := ruleIDForKind(k); id != "" {
		p.gateRule(id, start, end)
	}
}

// openGated opens a node of kind k and reports its gate on the current
// token.
func (p *Parser) openGated(k cst.Kind) {
	s, e := p.curSpan()
	p.gateKind(k, s, e)
	p.open(k)
}

// parseModeViewDecl parses `view name of subtype is elements end view
// [name];` (a declarative item).
func (p *Parser) parseModeViewDecl() bool {
	if !p.atKw(token.KwView) || !p.nthIsIdent(1) ||
		!p.nthIsKw(2, token.KwOf) {
		return false
	}
	p.openGated(cst.KindModeViewDecl)
	p.bumpAs(token.KwView)
	p.ident()
	p.bumpKw(token.KwOf)
	if !p.parseSubtypeIndication() {
		p.errorExpected("a record subtype")
	}
	p.expectKw(token.KwIs)
	for {
		if p.inactive(p.parseModeViewElement) {
			continue
		}
		if p.atEOF() || p.atKw(token.KwEnd) {
			break
		}
		if !p.parseModeViewElement() {
			p.error("expected a mode view element, found " +
				p.describeCurrent())
			p.recoverItem(endOnly)
		}
	}
	p.open(cst.KindEndClause)
	p.expectKw(token.KwEnd)
	if p.atKw(token.KwView) {
		p.bumpAs(token.KwView)
	} else {
		p.errorExpected("reserved word view")
		p.missing(token.KwView)
	}
	p.identOpt()
	p.expect(token.Semicolon)
	p.close()
	p.close()
	return true
}

// parseModeViewElement parses `name {, name} : element_mode;`.
func (p *Parser) parseModeViewElement() bool {
	if !p.atIdent() {
		return false
	}
	p.open(cst.KindModeViewElementDef)
	p.open(cst.KindIdentifierList)
	p.ident()
	for p.eat(token.Comma) {
		p.ident()
	}
	p.close()
	p.expect(token.Colon)
	p.open(cst.KindElementModeIndication)
	switch {
	case p.atKw(token.KwIn), p.atKw(token.KwOut), p.atKw(token.KwInout),
		p.atKw(token.KwBuffer), p.atKw(token.KwLinkage):
		p.bump()
	case p.atKw(token.KwView):
		p.bumpAs(token.KwView)
		if p.eat(token.LParen) {
			p.nameOrErr("a mode view name")
			p.expect(token.RParen)
		} else {
			p.nameOrErr("a mode view name")
		}
	default:
		p.errorExpected("a mode or a mode view")
	}
	p.close()
	p.expect(token.Semicolon)
	p.close()
	return true
}

func (p *Parser) nameOrErr(what string) {
	if !p.parseName() {
		p.errorExpected(what)
		p.missing(token.Identifier)
	}
}

// parseModeViewIndication parses an interface mode `view name [of
// subtype]` (ModeViewIndication) or `view (name) of subtype`
// (ArrayModeViewIndication).
func (p *Parser) parseModeViewIndication() bool {
	if !p.atKw(token.KwView) ||
		(!p.nthIsIdent(1) && p.nth(1) != token.LParen) {
		return false
	}
	if p.nth(1) == token.LParen {
		p.openGated(cst.KindArrayModeViewIndication)
		p.bumpAs(token.KwView)
		p.bump()
		p.nameOrErr("a mode view name")
		p.expect(token.RParen)
		p.expectKw(token.KwOf)
		if !p.parseSubtypeIndication() {
			p.errorExpected("an array subtype")
		}
		p.close()
		return true
	}
	p.openGated(cst.KindModeViewIndication)
	p.bumpAs(token.KwView)
	p.nameOrErr("a mode view name")
	if p.eatKw(token.KwOf) && !p.parseSubtypeIndication() {
		p.errorExpected("a record subtype")
	}
	p.close()
	return true
}

// parsePrivateVariableDecl parses `private variable ...;` inside a
// protected type declaration; the variable declaration itself is parsed
// by the decl builder.
func (p *Parser) parsePrivateVariableDecl() bool {
	if !p.atKw(token.KwPrivate) || !p.nthIsKw(1, token.KwVariable) {
		return false
	}
	p.openGated(cst.KindPrivateVariableDecl)
	p.bumpAs(token.KwPrivate)
	if !p.parseDeclarativeItem(RegionProtected) {
		p.errorExpected("a variable declaration")
	}
	p.close()
	return true
}

// parseIncompleteTypeDef parses a 2019 incomplete type definition of an
// interface type: private, <>, (<>), range <>, units <>, range <>.<>,
// array, access or file.
func (p *Parser) parseIncompleteTypeDef() bool {
	var kind cst.Kind
	switch {
	case p.atKw(token.KwPrivate):
		kind = cst.KindPrivateIncompleteTypeDef
	case p.at(token.Box):
		kind = cst.KindScalarIncompleteTypeDef
	case p.at(token.LParen) && p.nth(1) == token.Box &&
		p.nth(2) == token.RParen:
		kind = cst.KindDiscreteIncompleteTypeDef
	case p.atKw(token.KwRange) && p.nth(1) == token.Box:
		kind = cst.KindIntegerIncompleteTypeDef
		if p.nth(2) == token.Dot && p.nth(3) == token.Box {
			kind = cst.KindFloatingIncompleteTypeDef
		}
	case p.atKw(token.KwUnits) && p.nth(1) == token.Box:
		kind = cst.KindPhysicalIncompleteTypeDef
	case p.atKw(token.KwArray) && p.nth(1) == token.LParen:
		kind = cst.KindArrayIncompleteTypeDef
	case p.atKw(token.KwAccess):
		kind = cst.KindAccessIncompleteTypeDef
	case p.atKw(token.KwFile) && p.nthIsKw(1, token.KwOf):
		kind = cst.KindFileIncompleteTypeDef
	default:
		return false
	}
	p.openGated(kind)
	switch kind {
	case cst.KindPrivateIncompleteTypeDef:
		p.bumpAs(token.KwPrivate)
	case cst.KindScalarIncompleteTypeDef:
		p.bump()
	case cst.KindDiscreteIncompleteTypeDef:
		p.bump()
		p.bump()
		p.bump()
	case cst.KindIntegerIncompleteTypeDef,
		cst.KindPhysicalIncompleteTypeDef:
		p.bump()
		p.bump()
	case cst.KindFloatingIncompleteTypeDef:
		for range 4 {
			p.bump()
		}
	case cst.KindArrayIncompleteTypeDef:
		p.bump()
		p.bump()
		for {
			p.arrayIncompleteIndex()
			if !p.eat(token.Comma) {
				break
			}
		}
		p.expect(token.RParen)
		p.expectKw(token.KwOf)
		p.incompleteSubtype()
	case cst.KindAccessIncompleteTypeDef:
		p.bump()
		p.incompleteSubtype()
	case cst.KindFileIncompleteTypeDef:
		p.bump()
		p.bump()
		if !p.parseAnonymousTypeIndication() {
			p.nameOrErr("a type mark")
		}
	}
	p.close()
	return true
}

// arrayIncompleteIndex parses one index of an array incomplete type:
// `type_mark range <>`, an anonymous type indication or a discrete
// range.
func (p *Parser) arrayIncompleteIndex() {
	if p.parseAnonymousTypeIndication() {
		return
	}
	if p.atIdent() {
		m := p.mark()
		p.parseSelectedName()
		if p.atKw(token.KwRange) && p.nth(1) == token.Box {
			p.openAt(m, cst.KindIndexSubtypeDef)
			p.bump()
			p.bump()
			p.close()
		}
		return
	}
	if !p.parseDiscreteRange() {
		p.errorExpected("an index subtype")
	}
}

// incompleteSubtype parses a subtype indication or an anonymous type
// indication.
func (p *Parser) incompleteSubtype() {
	if !p.parseAnonymousTypeIndication() && !p.parseSubtypeIndication() {
		p.errorExpected("a subtype indication")
	}
}

// parseAnonymousTypeIndication parses `type is incomplete_type_def` as
// an interface subtype.
func (p *Parser) parseAnonymousTypeIndication() bool {
	if !p.atKw(token.KwType) || !p.nthIsKw(1, token.KwIs) {
		return false
	}
	p.openGated(cst.KindAnonymousTypeIndication)
	p.bumpKw(token.KwType)
	p.bumpKw(token.KwIs)
	if !p.parseIncompleteTypeDef() {
		p.errorExpected("an incomplete type definition")
	}
	p.close()
	return true
}

// parseProtectedTypeHeader parses the generic clause and generic map of
// a protected type declaration (`protected generic (...); [generic map
// (...);]`).
func (p *Parser) parseProtectedTypeHeader() bool {
	if !p.atKw(token.KwGeneric) || p.nthIsKw(1, token.KwMap) {
		return false
	}
	s, e := p.curSpan()
	p.gateRule("prot_generics", s, e)
	p.open(cst.KindProtectedTypeHeader)
	p.parseGenericClause()
	if p.atKw(token.KwGeneric) && p.nthIsKw(1, token.KwMap) {
		p.parseGenericMapAspect()
		p.expect(token.Semicolon)
	}
	p.close()
	return true
}

// parseProtectedTypeInstantiation parses `new name [generic map (...)]`
// after `type t is`.
func (p *Parser) parseProtectedTypeInstantiation() bool {
	if !p.atKw(token.KwNew) {
		return false
	}
	s, e := p.curSpan()
	p.gateRule("prot_inst", s, e)
	p.open(cst.KindProtectedTypeInstantiationDef)
	p.bumpKw(token.KwNew)
	p.nameOrErr("a protected type name")
	if p.atKw(token.KwGeneric) {
		p.parseGenericMapAspect()
	}
	p.close()
	return true
}

// parseSequentialBlock parses `[label :] block [is] decls begin stmts
// end block [label];` inside a process or subprogram.
func (p *Parser) parseSequentialBlock() bool {
	labeled := p.nthIsIdent(0) && p.nth(1) == token.Colon &&
		p.nthIsKw(2, token.KwBlock)
	if !labeled && !p.atKw(token.KwBlock) {
		return false
	}
	p.open(cst.KindSequentialBlockStmt)
	if labeled {
		p.open(cst.KindLabel)
		p.ident()
		p.bump()
		p.close()
	}
	s, e := p.curSpan()
	p.gateKind(cst.KindSequentialBlockStmt, s, e)
	p.bumpKw(token.KwBlock)
	p.eatKw(token.KwIs)
	p.declarativePart(RegionSequentialBlock, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.sequenceOfStatements(endOnly)
	p.endClause(true, token.KwBlock)
	p.close()
	return true
}
