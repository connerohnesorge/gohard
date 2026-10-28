package parser

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/lexer"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// The design-file skeleton (#31): design file, context clause, design
// units, configurations and the PSL verification-unit shells. The
// declarations, statements, expressions and PSL inside them are parsed
// by the builders' files through the stubs they define.

// unitFirst is the set of tokens that can start a design unit.
var unitFirst = token.NewSet(token.KwLibrary, token.KwUse,
	token.KwContext, token.KwEntity, token.KwArchitecture,
	token.KwPackage, token.KwConfiguration, token.KwVunit, token.KwVmode,
	token.KwVprop)

// parseDesignFile parses the whole input into a DesignFile node.
func (p *Parser) parseDesignFile() {
	p.open(cst.KindDesignFile)
	for {
		if p.inactive(p.parseDesignFileItem) {
			continue
		}
		if p.atEOF() {
			break
		}
		if !p.parseDesignUnit() {
			p.error("expected a design unit (entity, architecture, " +
				"package, configuration, context or a PSL verification " +
				"unit), found " + p.describeCurrent())
			before := p.emitted
			p.recover(unitFirst)
			if p.emitted == before {
				p.skipOne("unexpected " + p.describeCurrent())
			}
		}
	}
	p.finish()
	p.close()
}

// parseDesignFileItem is the item parser of an inactive branch at the
// design-file level: a design unit or a lone context item.
func (p *Parser) parseDesignFileItem() bool { return p.parseDesignUnit() }

// atUnitStart reports whether the current token starts a design unit.
func (p *Parser) atUnitStart() bool {
	for _, k := range []token.Kind{token.KwLibrary, token.KwUse,
		token.KwContext, token.KwEntity, token.KwArchitecture,
		token.KwPackage, token.KwConfiguration} {
		if p.atKw(k) {
			return true
		}
	}
	return p.atPSLUnit()
}

// atPSLUnit reports whether a vunit, vmode or vprop starts here.
func (p *Parser) atPSLUnit() bool {
	for _, k := range []token.Kind{token.KwVunit, token.KwVmode,
		token.KwVprop} {
		if p.atKw(k) && p.nthIsIdent(1) {
			return true
		}
	}
	return false
}

// parseDesignUnit parses context clause + library unit.
func (p *Parser) parseDesignUnit() bool {
	if !p.atUnitStart() {
		return false
	}
	p.open(cst.KindDesignUnit)
	p.open(cst.KindContextClause)
	for {
		if p.inactive(p.parseContextItem) {
			continue
		}
		if !p.parseContextItem() {
			break
		}
	}
	p.close()
	if !p.parseLibraryUnit() && !(p.sub != 0 && p.atEOF()) {
		p.errorExpected("a library unit")
	}
	p.close()
	return true
}

// parseContextItem parses a library clause, use clause or context
// reference.
func (p *Parser) parseContextItem() bool {
	switch {
	case p.atKw(token.KwLibrary):
		p.open(cst.KindLibraryClause)
		p.bumpKw(token.KwLibrary)
		p.simpleNameList()
		p.expect(token.Semicolon)
		p.close()
	case p.atKw(token.KwUse):
		p.parseUseClause()
	case p.atKw(token.KwContext) && !p.nthIsKw(2, token.KwIs):
		p.open(cst.KindContextReference)
		p.bumpKw(token.KwContext)
		p.selectedNameList()
		p.expect(token.Semicolon)
		p.close()
	default:
		return false
	}
	return true
}

// parseUseClause parses `use selected_name {, selected_name};`. Also a
// declarative item.
func (p *Parser) parseUseClause() {
	p.open(cst.KindUseClause)
	p.bumpKw(token.KwUse)
	p.selectedNameList()
	p.expect(token.Semicolon)
	p.close()
}

func (p *Parser) simpleNameList() {
	for {
		p.open(cst.KindSimpleName)
		p.ident()
		p.close()
		if !p.eat(token.Comma) {
			return
		}
	}
}

func (p *Parser) selectedNameList() {
	for {
		p.parseSelectedName()
		if !p.eat(token.Comma) {
			return
		}
	}
}

// parseSelectedName parses a restricted name: identifier { . suffix }
// where suffix is an identifier, character literal, operator symbol or
// `all`. It builds SimpleName and SelectedName nodes and never consumes
// parentheses, so callers can parse `entity e(arch)` themselves.
func (p *Parser) parseSelectedName() bool {
	m := p.mark()
	p.open(cst.KindSimpleName)
	p.ident()
	p.close()
	for p.at(token.Dot) {
		p.openAt(m, cst.KindSelectedName)
		p.bump()
		switch {
		case p.atIdent():
			p.ident()
		case p.at(token.KwAll), p.at(token.CharacterLiteral),
			p.at(token.StringLiteral):
			p.bump()
		default:
			p.errorExpected("an identifier, character literal, " +
				"operator symbol or all")
			p.missing(token.Identifier)
		}
		p.close()
	}
	return true
}

// parseLibraryUnit parses one library unit.
func (p *Parser) parseLibraryUnit() bool {
	switch {
	case p.atKw(token.KwEntity):
		p.parseEntity()
	case p.atKw(token.KwArchitecture):
		p.parseArchitecture()
	case p.atKw(token.KwPackage):
		switch {
		case p.nthIsKw(1, token.KwBody):
			p.parsePackageBody()
		case p.nthIsKw(3, token.KwNew):
			s, e := p.curSpan()
			p.gateRule("pkg_inst", s, e)
			p.parsePackageInstantiation()
		default:
			p.parsePackage()
		}
	case p.atKw(token.KwConfiguration):
		p.parseConfiguration()
	case p.atKw(token.KwContext):
		p.parseContextDecl()
	case p.atPSLUnit():
		p.parsePSLUnit()
	default:
		return false
	}
	return true
}

// unitName consumes the identifier after a unit keyword.
func (p *Parser) unitName() { p.ident() }

func (p *Parser) parseEntity() {
	p.open(cst.KindEntityDecl)
	p.bumpKw(token.KwEntity)
	p.unitName()
	p.expectKw(token.KwIs)
	if p.atKw(token.KwGeneric) {
		p.parseGenericClause()
	}
	if p.atKw(token.KwPort) {
		p.parsePortClause()
	}
	p.declarativePart(RegionEntity, beginOrEnd)
	if p.eatKw(token.KwBegin) {
		p.statementPart(endOnly)
	}
	p.endClause(false, token.KwEntity)
	p.close()
}

func (p *Parser) parseArchitecture() {
	p.open(cst.KindArchitectureBody)
	p.bumpKw(token.KwArchitecture)
	p.unitName()
	p.expectKw(token.KwOf)
	p.parseSelectedName()
	p.expectKw(token.KwIs)
	p.declarativePart(RegionArchitecture, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.statementPart(endOnly)
	p.endClause(false, token.KwArchitecture)
	p.close()
}

// parsePackage parses a package declaration; also used for nested
// (2008) package declarations in declarative parts.
func (p *Parser) parsePackage() {
	p.open(cst.KindPackageDecl)
	p.bumpKw(token.KwPackage)
	p.unitName()
	p.expectKw(token.KwIs)
	if p.atKw(token.KwGeneric) && !p.nthIsKw(1, token.KwMap) {
		p.parseGenericClause()
		if p.atKw(token.KwGeneric) && p.nthIsKw(1, token.KwMap) {
			p.parseGenericMapAspect()
			p.expect(token.Semicolon)
		}
	}
	p.declarativePart(RegionPackage, endOnly)
	p.endClause(false, token.KwPackage)
	p.close()
}

// parsePackageBody parses a package body; also used for nested ones.
func (p *Parser) parsePackageBody() {
	p.open(cst.KindPackageBody)
	p.bumpKw(token.KwPackage)
	p.bumpKw(token.KwBody)
	p.unitName()
	p.expectKw(token.KwIs)
	p.declarativePart(RegionPackageBody, endOnly)
	p.endClause(false, token.KwPackage, token.KwBody)
	p.close()
}

// parsePackageInstantiation parses `package p is new q [generic map];`
// (2008); also a declarative item.
func (p *Parser) parsePackageInstantiation() {
	p.open(cst.KindPackageInstantiation)
	p.bumpKw(token.KwPackage)
	p.unitName()
	p.expectKw(token.KwIs)
	p.expectKw(token.KwNew)
	p.parseSelectedName()
	if p.atKw(token.KwGeneric) {
		p.parseGenericMapAspect()
	}
	p.expect(token.Semicolon)
	p.close()
}

func (p *Parser) parseContextDecl() {
	p.open(cst.KindContextDecl)
	p.bumpKw(token.KwContext)
	p.unitName()
	p.expectKw(token.KwIs)
	p.open(cst.KindContextClause)
	for {
		if p.inactive(p.parseContextItem) {
			continue
		}
		if !p.parseContextItem() {
			break
		}
	}
	p.close()
	p.endClause(false, token.KwContext)
	p.close()
}

// endClause parses `end [kws...] [designator] ;` into an EndClause. With
// required the keywords must be present (end for, end units).
func (p *Parser) endClause(required bool, kws ...token.Kind) {
	p.open(cst.KindEndClause)
	p.expectKw(token.KwEnd)
	if len(kws) > 0 && (required || p.atKw(kws[0])) {
		for _, k := range kws {
			p.expectKw(k)
		}
	}
	switch {
	case p.atIdent():
		p.identOpt()
	case p.at(token.StringLiteral), p.at(token.CharacterLiteral):
		p.bump()
	}
	p.expect(token.Semicolon)
	p.close()
}

// parseGenericClause parses `generic ( interface_list ) ;`.
func (p *Parser) parseGenericClause() {
	p.open(cst.KindGenericClause)
	p.bumpKw(token.KwGeneric)
	p.parseInterfaceList(InterfaceGeneric)
	p.expect(token.Semicolon)
	p.close()
}

// parsePortClause parses `port ( interface_list ) ;`.
func (p *Parser) parsePortClause() {
	p.open(cst.KindPortClause)
	p.bumpKw(token.KwPort)
	p.parseInterfaceList(InterfacePort)
	p.expect(token.Semicolon)
	p.close()
}

// parseGenericMapAspect parses `generic map ( association_list )`.
func (p *Parser) parseGenericMapAspect() {
	p.open(cst.KindGenericMapAspect)
	p.bumpKw(token.KwGeneric)
	p.expectKw(token.KwMap)
	p.parseAssociationList()
	p.close()
}

// parsePortMapAspect parses `port map ( association_list )`.
func (p *Parser) parsePortMapAspect() {
	p.open(cst.KindPortMapAspect)
	p.bumpKw(token.KwPort)
	p.expectKw(token.KwMap)
	p.parseAssociationList()
	p.close()
}

// ----------------------------------------------------- configurations

func (p *Parser) parseConfiguration() {
	p.open(cst.KindConfigurationDecl)
	p.bumpKw(token.KwConfiguration)
	p.unitName()
	p.expectKw(token.KwOf)
	p.parseSelectedName()
	p.expectKw(token.KwIs)
	p.open(cst.KindDeclarativePart)
	item := func() bool {
		switch {
		case p.atVunitBinding() || p.atKw(token.KwFor):
			return false
		case p.atKw(token.KwUse):
			p.parseUseClause()
			return true
		}
		return p.parseDeclarativeItem(RegionConfiguration)
	}
	for {
		if p.inactive(item) {
			continue
		}
		if p.atEOF() || p.atKw(token.KwFor) || p.atKw(token.KwEnd) ||
			p.atVunitBinding() {
			break
		}
		if !item() {
			p.error("expected a use clause, attribute specification " +
				"or group declaration, found " + p.describeCurrent())
			p.recoverItem(token.NewSet(token.KwFor, token.KwEnd))
		}
	}
	p.close()
	for p.atVunitBinding() {
		p.parseVunitBinding()
	}
	if p.atKw(token.KwFor) {
		p.parseBlockConfiguration()
	} else {
		p.errorExpected("a block configuration (for ...)")
	}
	p.endClause(false, token.KwConfiguration)
	p.close()
}

// atVunitBinding reports whether `use vunit` starts here.
func (p *Parser) atVunitBinding() bool {
	return p.atKw(token.KwUse) && p.nthIsKw(1, token.KwVunit)
}

// parseVunitBinding parses `use vunit name {, name};` (2008).
func (p *Parser) parseVunitBinding() {
	p.open(cst.KindVerificationUnitBinding)
	s, e := p.curSpan()
	p.gate("a verification unit binding", token.Std2008, s, e)
	p.bumpKw(token.KwUse)
	p.bumpKw(token.KwVunit)
	p.selectedNameList()
	p.expect(token.Semicolon)
	p.close()
}

// parseBlockConfiguration parses `for block_spec {use_clause}
// {configuration_item} end for;`.
func (p *Parser) parseBlockConfiguration() {
	if !p.enter() {
		p.tooDeep()
		p.leave()
		return
	}
	defer p.leave()
	p.open(cst.KindBlockConfiguration)
	p.bumpKw(token.KwFor)
	p.parseSelectedName()
	if p.at(token.LParen) {
		// A generate index or range: for gen(3) / for gen(1 to 4).
		p.open(cst.KindAssociationList)
		p.bump()
		p.parseDiscreteRange()
		p.expect(token.RParen)
		p.close()
	}
	for {
		if p.inactive(p.parseBlockConfigItem) {
			continue
		}
		if !p.parseBlockConfigItem() {
			break
		}
	}
	p.endClause(true, token.KwFor)
	p.close()
}

func (p *Parser) parseBlockConfigItem() bool {
	switch {
	case p.atKw(token.KwUse) && !p.nthIsKw(1, token.KwVunit):
		p.parseUseClause()
	case p.atKw(token.KwFor):
		if p.atComponentSpec() {
			p.parseComponentConfiguration()
		} else {
			p.parseBlockConfiguration()
		}
	default:
		return false
	}
	return true
}

// atComponentSpec reports whether `for` starts a component configuration
// (`for l1, l2 : comp`, `for all : comp`, `for others : comp`) rather
// than a block configuration.
func (p *Parser) atComponentSpec() bool {
	for i := 1; ; i += 2 {
		if !p.nthIsIdent(i) && !p.nthIsKw(i, token.KwAll) &&
			!p.nthIsKw(i, token.KwOthers) {
			return false
		}
		switch p.nth(i + 1) {
		case token.Colon:
			return true
		case token.Comma:
		default:
			return false
		}
	}
}

// parseComponentSpecification parses `instantiation_list : name`.
func (p *Parser) parseComponentSpecification() {
	p.open(cst.KindComponentSpecification)
	p.open(cst.KindInstantiationList)
	if p.atKw(token.KwAll) || p.atKw(token.KwOthers) {
		p.bump()
	} else {
		for {
			p.ident()
			if !p.eat(token.Comma) {
				break
			}
		}
	}
	p.close()
	p.expect(token.Colon)
	p.parseSelectedName()
	p.close()
}

func (p *Parser) parseComponentConfiguration() {
	p.open(cst.KindComponentConfiguration)
	p.bumpKw(token.KwFor)
	p.parseComponentSpecification()
	if p.atBinding() {
		p.parseBindingIndication()
		p.expect(token.Semicolon)
	}
	for p.atVunitBinding() {
		p.parseVunitBinding()
	}
	if p.atKw(token.KwFor) {
		p.parseBlockConfiguration()
	}
	p.endClause(true, token.KwFor)
	p.close()
}

// atBinding reports whether a binding indication starts here.
func (p *Parser) atBinding() bool {
	return (p.atKw(token.KwUse) && !p.nthIsKw(1, token.KwVunit)) ||
		p.atKw(token.KwGeneric) || p.atKw(token.KwPort)
}

// parseBindingIndication parses `[use entity_aspect] [generic map]
// [port map]` (no `;`). Also used by configuration specifications.
func (p *Parser) parseBindingIndication() {
	p.open(cst.KindBindingIndication)
	if p.eatKw(token.KwUse) {
		p.open(cst.KindEntityAspect)
		switch {
		case p.eatKw(token.KwEntity):
			p.parseSelectedName()
			if p.at(token.LParen) {
				p.bump()
				p.ident()
				p.expect(token.RParen)
			}
		case p.eatKw(token.KwConfiguration):
			p.parseSelectedName()
		case p.eatKw(token.KwOpen):
		default:
			p.errorExpected("entity, configuration or open")
		}
		p.close()
	}
	if p.atKw(token.KwGeneric) {
		p.parseGenericMapAspect()
	}
	if p.atKw(token.KwPort) {
		p.parsePortMapAspect()
	}
	p.close()
}

// ------------------------------------------------- PSL unit shells

// parsePSLUnit parses `vunit|vmode|vprop name [(binding)] { items }`.
// The whole unit is PSL (#6): the lexer is in PSL mode from the unit
// keyword to the closing brace.
func (p *Parser) parsePSLUnit() {
	p.setMode(lexer.PSL)
	kind, kw := cst.KindPSLVunit, token.KwVunit
	switch {
	case p.atKw(token.KwVmode):
		kind, kw = cst.KindPSLVmode, token.KwVmode
	case p.atKw(token.KwVprop):
		kind, kw = cst.KindPSLVprop, token.KwVprop
	}
	p.open(kind)
	s, e := p.curSpan()
	p.gate("a PSL verification unit", token.Std2008, s, e)
	p.bumpKw(kw)
	p.ident()
	if p.at(token.LParen) {
		p.open(cst.KindPSLHierarchicalName)
		p.bump()
		p.parseSelectedName()
		if p.at(token.LParen) {
			p.bump()
			p.ident()
			p.expect(token.RParen)
		}
		p.expect(token.RParen)
		p.close()
	}
	p.expect(token.LBrace)
	for {
		if p.inactive(p.parsePSLUnitItem) {
			continue
		}
		if p.atEOF() || p.at(token.RBrace) {
			break
		}
		if !p.parsePSLUnitItem() {
			p.error("expected a PSL verification unit item, found " +
				p.describeCurrent())
			p.recoverItem(token.NewSet(token.RBrace))
		}
	}
	p.expect(token.RBrace)
	p.close()
	p.setMode(lexer.VHDL)
}
