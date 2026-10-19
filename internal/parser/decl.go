package parser

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Declarations and interface lists (builder: decl): every declarative
// item of every region (1076-2019 §4-§6), subprogram specifications,
// bodies and instantiations, and generic, port and parameter lists.
//
// Region rules: parseDeclarativeItem parses every item in every region
// (the tree shape never depends on the region) and reports syntax/error
// when the region does not allow the item (regionAllows).

// declFirst is the set of tokens that can start a declarative item. The
// list loops use it to resynchronize after an error.
var declFirst = token.NewSet(token.KwType, token.KwSubtype,
	token.KwConstant, token.KwSignal, token.KwVariable, token.KwShared,
	token.KwFile, token.KwAlias, token.KwAttribute, token.KwComponent,
	token.KwFor, token.KwDisconnect, token.KwGroup, token.KwFunction,
	token.KwProcedure, token.KwPure, token.KwImpure, token.KwPackage,
	token.KwUse, token.KwView, token.KwPrivate, token.KwDefault,
	token.KwProperty, token.KwSequence)

// declClass classifies declarative items for the region rules.
type declClass uint8

const (
	dcType declClass = iota // type and subtype declarations
	dcConstant
	dcSignal
	dcSharedVariable
	dcVariable
	dcFile
	dcAlias
	dcAttributeDecl
	dcAttributeSpec
	dcComponent
	dcConfigSpec
	dcDisconnect
	dcUse
	dcGroup
	dcSubprogramDecl
	dcSubprogramBody
	dcSubprogramInst
	dcPackageDecl
	dcPackageBody
	dcPackageInst
	dcModeView
	dcPrivate
	dcPSL
	numDeclClasses
)

var declClassNames = [numDeclClasses]string{
	dcType:           "a type or subtype declaration",
	dcConstant:       "a constant declaration",
	dcSignal:         "a signal declaration",
	dcSharedVariable: "a shared variable declaration",
	dcVariable:       "a non-shared variable declaration",
	dcFile:           "a file declaration",
	dcAlias:          "an alias declaration",
	dcAttributeDecl:  "an attribute declaration",
	dcAttributeSpec:  "an attribute specification",
	dcComponent:      "a component declaration",
	dcConfigSpec:     "a configuration specification",
	dcDisconnect:     "a disconnection specification",
	dcUse:            "a use clause",
	dcGroup:          "a group or group template declaration",
	dcSubprogramDecl: "a subprogram declaration",
	dcSubprogramBody: "a subprogram body",
	dcSubprogramInst: "a subprogram instantiation",
	dcPackageDecl:    "a package declaration",
	dcPackageBody:    "a package body",
	dcPackageInst:    "a package instantiation",
	dcModeView:       "a mode view declaration",
	dcPrivate:        "a private variable declaration",
	dcPSL:            "a PSL declaration",
}

// classSet is a set of declClass values.
type classSet uint32

func classes(cs ...declClass) classSet {
	var s classSet
	for _, c := range cs {
		s |= 1 << c
	}
	return s
}

func (s classSet) has(c declClass) bool { return s&(1<<c) != 0 }

// The allowed items per region (1076-2008 and 2019 grammar of each
// declarative part).
var (
	commonItems = classes(dcType, dcConstant, dcFile, dcAlias,
		dcAttributeDecl, dcAttributeSpec, dcUse, dcGroup, dcSubprogramDecl,
		dcSubprogramBody, dcSubprogramInst, dcPackageDecl, dcPackageBody,
		dcPackageInst, dcModeView)
	concurrentItems = commonItems | classes(dcSignal, dcSharedVariable,
		dcDisconnect, dcPSL)
	sequentialItems = commonItems&^classes(dcModeView) |
		classes(dcVariable)
	blockItems   = concurrentItems | classes(dcComponent, dcConfigSpec)
	packageItems = (concurrentItems | classes(dcComponent)) &^
		classes(dcSubprogramBody, dcPackageBody)
)

var regionAllows = [...]classSet{
	RegionEntity:        concurrentItems,
	RegionArchitecture:  blockItems,
	RegionPackage:       packageItems,
	RegionPackageBody:   commonItems | classes(dcSharedVariable),
	RegionConfiguration: classes(dcUse, dcAttributeSpec, dcGroup),
	RegionBlock:         blockItems,
	RegionGenerate:      blockItems,
	RegionProcess:       sequentialItems,
	RegionSubprogram:    sequentialItems,
	RegionProtected: classes(dcSubprogramDecl, dcSubprogramInst,
		dcAttributeSpec, dcUse, dcPrivate, dcAlias),
	RegionProtectedBody:   sequentialItems,
	RegionSequentialBlock: sequentialItems,
	RegionPSLUnit:         ^classSet(0),
}

var regionNames = [...]string{
	RegionEntity:          "an entity declarative part",
	RegionArchitecture:    "an architecture declarative part",
	RegionPackage:         "a package declaration",
	RegionPackageBody:     "a package body",
	RegionConfiguration:   "a configuration declarative part",
	RegionBlock:           "a block declarative part",
	RegionGenerate:        "a generate statement body",
	RegionProcess:         "a process declarative part",
	RegionSubprogram:      "a subprogram declarative part",
	RegionProtected:       "a protected type declaration",
	RegionProtectedBody:   "a protected type body",
	RegionSequentialBlock: "a sequential block declarative part",
	RegionPSLUnit:         "a verification unit",
}

// allows reports whether region r allows items of class c; unknown
// regions allow everything.
func (r DeclRegion) allows(c declClass) bool {
	if int(r) >= len(regionAllows) || regionAllows[r] == 0 {
		return true
	}
	return regionAllows[r].has(c)
}

// kwAt returns the keyword the nth visible token spells (see atKw), or
// its kind when it spells none.
func (p *Parser) kwAt(n int) token.Kind {
	r := p.peek(n)
	if r == nil {
		return token.EOF
	}
	if r.Kind == token.Identifier && r.Word != 0 {
		return r.Word
	}
	return r.Kind
}

// declClassAt classifies the declarative item starting at the current
// token; false if none starts here. Subprogram bodies are classified as
// declarations until their `is` is seen.
func (p *Parser) declClassAt() (declClass, bool) {
	switch p.kwAt(0) {
	case token.KwType, token.KwSubtype:
		return dcType, true
	case token.KwConstant:
		return dcConstant, true
	case token.KwSignal:
		return dcSignal, true
	case token.KwShared:
		return dcSharedVariable, true
	case token.KwVariable:
		return dcVariable, true
	case token.KwFile:
		return dcFile, true
	case token.KwAlias:
		return dcAlias, true
	case token.KwAttribute:
		if p.nth(2) == token.Colon {
			return dcAttributeDecl, true
		}
		return dcAttributeSpec, true
	case token.KwComponent:
		return dcComponent, true
	case token.KwFor:
		return dcConfigSpec, true
	case token.KwDisconnect:
		return dcDisconnect, true
	case token.KwUse:
		return dcUse, true
	case token.KwGroup:
		return dcGroup, true
	case token.KwFunction, token.KwProcedure, token.KwPure,
		token.KwImpure:
		if p.atSubprogramInst() {
			return dcSubprogramInst, true
		}
		return dcSubprogramDecl, true
	case token.KwPackage:
		switch {
		case p.nthIsKw(1, token.KwBody):
			return dcPackageBody, true
		case p.nthIsKw(3, token.KwNew):
			return dcPackageInst, true
		}
		return dcPackageDecl, true
	case token.KwView:
		return dcModeView, true
	case token.KwPrivate:
		return dcPrivate, true
	case token.KwProperty, token.KwSequence, token.KwDefault:
		if p.atPSLTrigger() {
			return dcPSL, true
		}
	}
	return 0, false
}

// atSubprogramInst reports whether `[pure|impure] function|procedure
// designator is new` starts here.
func (p *Parser) atSubprogramInst() bool {
	d := 1
	if p.atKw(token.KwPure) || p.atKw(token.KwImpure) {
		d = 2
	}
	return p.nthIsKw(d+1, token.KwIs) && p.nthIsKw(d+2, token.KwNew)
}

// parseDeclarativeItem parses one declarative item allowed in region r
// (including nested packages and package bodies via parsePackage,
// parsePackageBody and parsePackageInstantiation, use clauses via
// parseUseClause, mode views via parseModeViewDecl, and PSL declarations
// via parsePSLDeclaration). It returns false, consuming nothing, when the
// current token cannot start a declarative item. An item the region does
// not allow is parsed and reported.
func (p *Parser) parseDeclarativeItem(r DeclRegion) bool {
	c, ok := p.declClassAt()
	if !ok {
		return false
	}
	s, e := p.curSpan()
	priv := c == dcVariable && p.afterPrivate()
	switch c {
	case dcType:
		if p.atKw(token.KwType) {
			p.parseTypeDecl()
		} else {
			p.parseSubtypeDecl()
		}
	case dcConstant, dcSignal, dcSharedVariable, dcVariable, dcFile:
		p.parseObjectDecl()
	case dcAlias:
		p.parseAliasDecl()
	case dcAttributeDecl:
		if r == RegionPackageBody {
			p.gateRule("pkg_body_attr_decl", s, e)
		}
		p.parseAttributeDecl()
	case dcAttributeSpec:
		p.parseAttributeSpec()
	case dcComponent:
		p.parseComponentDecl()
	case dcConfigSpec:
		p.parseConfigurationSpec()
	case dcDisconnect:
		p.parseDisconnectionSpec()
	case dcUse:
		p.parseUseClause()
	case dcGroup:
		p.parseGroup()
	case dcSubprogramDecl, dcSubprogramInst:
		c = p.parseSubprogram(s, e)
	case dcPackageDecl:
		p.gateRule("nested_pkg", s, e)
		p.parsePackage()
	case dcPackageBody:
		p.gateRule("nested_pkg", s, e)
		p.parsePackageBody()
	case dcPackageInst:
		p.gateRule("pkg_inst", s, e)
		p.parsePackageInstantiation()
	case dcModeView:
		if !p.parseModeViewDecl() {
			p.unparsedItem()
			return true
		}
	case dcPrivate:
		if !p.parsePrivateVariableDecl() {
			p.unparsedItem()
			return true
		}
	case dcPSL:
		if !p.parsePSLDeclaration() {
			p.unparsedItem()
			return true
		}
	}
	if !r.allows(c) && !(c == dcVariable && priv) {
		msg := declClassNames[c] + " is not allowed in " + regionNames[r]
		if c == dcVariable && r.allows(dcSharedVariable) {
			msg += " (declare it shared)"
		}
		p.problemAt(s, e, diag.SyntaxError, msg)
	}
	return true
}

// afterPrivate reports whether the last consumed token is the word
// private: the variable of a 2019 private variable declaration, which
// parsePrivateVariableDecl parses through parseDeclarativeItem.
func (p *Parser) afterPrivate() bool {
	const w = "private"
	end := p.lastEnd
	return end >= len(w) && strings.EqualFold(p.text[end-len(w):end], w)
}

// unparsedItem skips an item a construct parser declined although its
// first word announced it.
func (p *Parser) unparsedItem() {
	p.error("expected a declaration, found " + p.describeCurrent())
	p.recoverItem(declFirst.Union(beginOrEnd))
}

// declIdentList parses `identifier {, identifier}` into an
// IdentifierList.
func (p *Parser) declIdentList() {
	p.open(cst.KindIdentifierList)
	for {
		p.ident()
		if !p.eat(token.Comma) {
			break
		}
	}
	p.close()
}

// declSubtype parses a required subtype indication.
func (p *Parser) declSubtype() {
	if !p.parseSubtypeIndication() {
		p.errorExpected("a subtype indication")
	}
}

// declExpr parses a required expression.
func (p *Parser) declExpr() {
	if !p.parseExpression() {
		p.errorExpected("an expression")
	}
}

// declValue parses a required initial or default value: an
// expression or (2019) a conditional expression.
func (p *Parser) declValue() {
	if !p.parseConditionalExpression() {
		p.errorExpected("an expression")
	}
}

// declName parses a required name (operator symbols and character
// literals included).
func (p *Parser) declName() {
	if !p.parseName() {
		p.errorExpected("a name")
		p.missing(token.Identifier)
	}
}

// --------------------------------------------------------- types

// parseTypeDecl parses `type t is definition;` or the incomplete
// `type t;`.
func (p *Parser) parseTypeDecl() {
	m := p.mark()
	p.bumpKw(token.KwType)
	p.ident()
	if p.at(token.Semicolon) {
		p.openAt(m, cst.KindIncompleteTypeDecl)
		p.bump()
		p.close()
		return
	}
	p.openAt(m, cst.KindTypeDecl)
	p.expectKw(token.KwIs)
	ok, ended := p.typeDefinition()
	if !ok {
		p.errorExpected("a type definition")
		p.recover(token.NewSet(token.Semicolon).Union(declFirst).
			Union(beginOrEnd))
	}
	if !ended {
		p.expect(token.Semicolon)
	}
	p.close()
}

// parseSubtypeDecl parses `subtype s is subtype_indication;`.
func (p *Parser) parseSubtypeDecl() {
	p.open(cst.KindSubtypeDecl)
	p.bumpKw(token.KwSubtype)
	p.ident()
	p.expectKw(token.KwIs)
	p.declSubtype()
	p.expect(token.Semicolon)
	p.close()
}

// ------------------------------------------------------- objects

// parseObjectDecl parses a constant, signal, [shared] variable or file
// declaration.
func (p *Parser) parseObjectDecl() {
	switch p.kwAt(0) {
	case token.KwConstant:
		p.open(cst.KindConstantDecl)
	case token.KwSignal:
		p.open(cst.KindSignalDecl)
	case token.KwFile:
		p.open(cst.KindFileDecl)
	default:
		p.open(cst.KindVariableDecl)
		p.eatKw(token.KwShared)
		p.expectKw(token.KwVariable)
		p.objectRest()
		p.close()
		return
	}
	file := p.atKw(token.KwFile)
	p.bump()
	if !file {
		p.objectRest()
		p.close()
		return
	}
	p.declIdentList()
	p.expect(token.Colon)
	p.declSubtype()
	if p.atKw(token.KwOpen) || p.atKw(token.KwIs) {
		p.open(cst.KindFileOpenInformation)
		if p.eatKw(token.KwOpen) {
			p.declExpr()
		}
		p.expectKw(token.KwIs)
		// VHDL-87 file mode, kept for old sources.
		if p.atKw(token.KwIn) || p.atKw(token.KwOut) {
			p.bump()
		}
		p.declExpr()
		p.close()
	}
	p.expect(token.Semicolon)
	p.close()
}

// objectRest parses `names : subtype [register|bus] [:= expr];` after
// the object class keyword.
func (p *Parser) objectRest() {
	p.declIdentList()
	p.expect(token.Colon)
	p.declSubtype()
	if p.atKw(token.KwRegister) || p.atKw(token.KwBus) {
		p.bump()
	}
	if p.eat(token.VarAssign) {
		p.declValue()
	}
	p.expect(token.Semicolon)
}

// parseAliasDecl parses `alias designator [: subtype] is name
// [signature];`.
func (p *Parser) parseAliasDecl() {
	p.open(cst.KindAliasDecl)
	p.bumpKw(token.KwAlias)
	if p.at(token.CharacterLiteral) || p.at(token.StringLiteral) {
		p.bump()
	} else {
		p.ident()
	}
	if p.eat(token.Colon) {
		p.declSubtype()
	}
	p.expectKw(token.KwIs)
	p.declName()
	if p.at(token.LBracket) {
		p.parseSignature()
	}
	p.expect(token.Semicolon)
	p.close()
}

// ---------------------------------------------------- attributes

// parseAttributeDecl parses `attribute a : type_mark;`.
func (p *Parser) parseAttributeDecl() {
	p.open(cst.KindAttributeDecl)
	p.bumpKw(token.KwAttribute)
	p.ident()
	p.expect(token.Colon)
	p.declTypeMark()
	p.expect(token.Semicolon)
	p.close()
}

// entityClasses are the entity class keywords (2008 adds property and
// sequence, 2019 view).
var entityClasses = token.NewSet(token.KwEntity, token.KwArchitecture,
	token.KwConfiguration, token.KwProcedure, token.KwFunction,
	token.KwPackage, token.KwType, token.KwSubtype, token.KwConstant,
	token.KwSignal, token.KwVariable, token.KwComponent, token.KwLabel,
	token.KwLiteral, token.KwUnits, token.KwGroup, token.KwFile,
	token.KwProperty, token.KwSequence, token.KwView)

// entityClass consumes an entity class keyword or reports it missing.
func (p *Parser) entityClass() {
	if k := p.kwAt(0); entityClasses.Has(k) {
		p.bumpKw(k)
		return
	}
	p.errorExpected("an entity class")
	p.missing(token.KwSignal)
}

// parseAttributeSpec parses `attribute a of entity_specification is
// expression;`.
func (p *Parser) parseAttributeSpec() {
	p.open(cst.KindAttributeSpec)
	p.bumpKw(token.KwAttribute)
	p.ident()
	p.expectKw(token.KwOf)
	p.open(cst.KindEntitySpecification)
	p.open(cst.KindEntityNameList)
	if p.atKw(token.KwOthers) || p.atKw(token.KwAll) {
		p.bump()
	} else {
		for {
			p.open(cst.KindEntityDesignator)
			if p.at(token.CharacterLiteral) || p.at(token.StringLiteral) {
				p.bump()
			} else {
				p.ident()
			}
			if p.at(token.LBracket) {
				p.parseSignature()
			}
			p.close()
			if !p.eat(token.Comma) {
				break
			}
		}
	}
	p.close()
	p.expect(token.Colon)
	p.entityClass()
	p.close()
	p.expectKw(token.KwIs)
	p.declValue()
	p.expect(token.Semicolon)
	p.close()
}

// ------------------------------------------- components and specs

// parseComponentDecl parses `component c [is] [generic clause] [port
// clause] end [component] [c];` (`end` alone is 2019).
func (p *Parser) parseComponentDecl() {
	p.open(cst.KindComponentDecl)
	p.bumpKw(token.KwComponent)
	p.ident()
	p.eatKw(token.KwIs)
	item := func() bool {
		switch {
		case p.atKw(token.KwGeneric):
			p.parseGenericClause()
		case p.atKw(token.KwPort):
			p.parsePortClause()
		default:
			return false
		}
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
			p.error("expected a generic clause, port clause or end, " +
				"found " + p.describeCurrent())
			p.recoverItem(token.NewSet(token.KwEnd, token.KwGeneric,
				token.KwPort))
		}
	}
	if p.atKw(token.KwEnd) && !p.nthIsKw(1, token.KwComponent) {
		s, e := p.curSpan()
		p.gateRule("end_component", s, e)
	}
	p.endClause(false, token.KwComponent)
	p.close()
}

// parseConfigurationSpec parses `for component_specification
// binding_indication; [end for;]`.
func (p *Parser) parseConfigurationSpec() {
	p.open(cst.KindConfigurationSpec)
	p.bumpKw(token.KwFor)
	p.parseComponentSpecification()
	p.parseBindingIndication()
	p.expect(token.Semicolon)
	for p.atVunitBinding() {
		p.parseVunitBinding()
	}
	if p.atKw(token.KwEnd) && p.nthIsKw(1, token.KwFor) {
		p.endClause(true, token.KwFor)
	}
	p.close()
}

// parseDisconnectionSpec parses `disconnect signal_list : type_mark
// after time_expression;`.
func (p *Parser) parseDisconnectionSpec() {
	p.open(cst.KindDisconnectionSpec)
	p.bumpKw(token.KwDisconnect)
	if p.atKw(token.KwOthers) || p.atKw(token.KwAll) {
		p.bump()
	} else {
		for {
			p.declName()
			if !p.eat(token.Comma) {
				break
			}
		}
	}
	p.expect(token.Colon)
	p.declTypeMark()
	p.expectKw(token.KwAfter)
	p.declExpr()
	p.expect(token.Semicolon)
	p.close()
}

// parseGroup parses a group template declaration `group t is
// (entity_class [<>] {, ...});` or a group declaration `group g : t
// (constituent {, constituent});`.
func (p *Parser) parseGroup() {
	if p.nth(2) == token.Colon {
		p.open(cst.KindGroupDecl)
		p.bumpKw(token.KwGroup)
		p.ident()
		p.bump()
		p.declTypeMark()
		p.expect(token.LParen)
		for {
			p.declName()
			if !p.eat(token.Comma) {
				break
			}
		}
		p.expect(token.RParen)
		p.expect(token.Semicolon)
		p.close()
		return
	}
	p.open(cst.KindGroupTemplateDecl)
	p.bumpKw(token.KwGroup)
	p.ident()
	p.expectKw(token.KwIs)
	p.expect(token.LParen)
	for {
		p.open(cst.KindEntityClassEntry)
		p.entityClass()
		p.eat(token.Box)
		p.close()
		if !p.eat(token.Comma) {
			break
		}
	}
	p.expect(token.RParen)
	p.expect(token.Semicolon)
	p.close()
}

// --------------------------------------------------- subprograms

// parseSubprogram parses a subprogram declaration, body or (2008)
// instantiation starting at [pure|impure] function|procedure; s, e is
// the span of its first token. It returns the item's class.
func (p *Parser) parseSubprogram(s, e int) declClass {
	if p.atSubprogramInst() {
		p.open(cst.KindSubprogramInstantiation)
		p.gateRule("subp_inst", s, e)
		if p.atKw(token.KwPure) || p.atKw(token.KwImpure) {
			p.error("pure and impure are not allowed in a subprogram " +
				"instantiation")
			p.bump()
		}
		p.bump()
		p.designator()
		p.bumpKw(token.KwIs)
		p.bumpKw(token.KwNew)
		p.declTypeMark()
		if p.at(token.LBracket) {
			p.parseSignature()
		}
		if p.atKw(token.KwGeneric) {
			p.parseGenericMapAspect()
		}
		p.expect(token.Semicolon)
		p.close()
		return dcSubprogramInst
	}
	m := p.mark()
	fn := p.parseSubprogramSpec()
	if !p.atKw(token.KwIs) {
		p.openAt(m, cst.KindSubprogramDecl)
		p.expect(token.Semicolon)
		p.close()
		return dcSubprogramDecl
	}
	p.openAt(m, cst.KindSubprogramBody)
	p.bumpKw(token.KwIs)
	p.declarativePart(RegionSubprogram, beginOrEnd)
	p.expectKw(token.KwBegin)
	p.sequenceOfStatements(endOnly)
	if fn {
		p.endClause(false, token.KwFunction)
	} else {
		p.endClause(false, token.KwProcedure)
	}
	p.close()
	return dcSubprogramBody
}

// designator consumes a subprogram designator: an identifier or an
// operator symbol.
func (p *Parser) designator() {
	if p.at(token.StringLiteral) {
		p.bump()
		return
	}
	p.ident()
}

// parseSubprogramSpec parses `[pure|impure] function designator
// [generic header] [[parameter] (params)] return [id of] type_mark` or
// `procedure designator [generic header] [[parameter] (params)]` into a
// FunctionSpec or ProcedureSpec. It reports whether it was a function.
func (p *Parser) parseSubprogramSpec() bool {
	fn := !p.atKw(token.KwProcedure)
	if fn {
		p.open(cst.KindFunctionSpec)
		if p.atKw(token.KwPure) || p.atKw(token.KwImpure) {
			p.bump()
		}
		p.expectKw(token.KwFunction)
	} else {
		p.open(cst.KindProcedureSpec)
		p.bumpKw(token.KwProcedure)
	}
	p.designator()
	if p.atKw(token.KwGeneric) {
		p.open(cst.KindSubprogramHeader)
		s, e := p.curSpan()
		p.gateRule("generic_on_subp", s, e)
		p.open(cst.KindGenericClause)
		p.bumpKw(token.KwGeneric)
		p.parseInterfaceList(InterfaceGeneric)
		p.close()
		if p.atKw(token.KwGeneric) && p.nthIsKw(1, token.KwMap) {
			p.parseGenericMapAspect()
		}
		p.close()
	}
	if p.at(token.LParen) ||
		(p.atKw(token.KwParameter) && p.nth(1) == token.LParen) {
		p.open(cst.KindParameterList)
		p.eatKw(token.KwParameter)
		p.parseInterfaceList(InterfaceParameter)
		p.close()
	}
	if fn {
		if p.atKw(token.KwReturn) {
			p.open(cst.KindReturnSpec)
			p.bumpKw(token.KwReturn)
			if p.atIdent() && p.nthIsKw(1, token.KwOf) {
				// 2019: return identifier of type_mark.
				p.ident()
				p.bumpKw(token.KwOf)
			}
			p.declTypeMark()
			p.close()
		} else {
			p.expectKw(token.KwReturn)
		}
	}
	p.close()
	return fn
}

// ----------------------------------------------- interface lists

// interfaceBreak ends an interface list whose `)` is missing.
var interfaceBreak = token.NewSet(token.KwEnd, token.KwBegin,
	token.KwPort)

var semiOrRParen = token.NewSet(token.Semicolon, token.RParen)

// parseInterfaceList parses `( interface_element {; interface_element}
// [;] )` into an InterfaceList node (generic, port or parameter list).
// The `(` and `)` are part of the node; the trailing `;` is 2019.
func (p *Parser) parseInterfaceList(k InterfaceKind) {
	p.open(cst.KindInterfaceList)
	if !p.expect(token.LParen) {
		p.close()
		return
	}
	item := func() bool {
		if !p.parseInterfaceElement(k) {
			return false
		}
		p.eat(token.Semicolon)
		return true
	}
	stop := semiOrRParen.Union(interfaceBreak)
	for {
		if p.inactive(item) {
			continue
		}
		if p.atEOF() || p.at(token.RParen) || p.atAny(interfaceBreak) {
			break
		}
		if !p.parseInterfaceElement(k) {
			p.error("expected an interface declaration, found " +
				p.describeCurrent())
			p.recover(stop)
		}
		if p.at(token.Semicolon) {
			s, e := p.curSpan()
			p.bump()
			if p.at(token.RParen) {
				p.gateRule("trailing_semi", s, e)
			}
			continue
		}
		if p.atEOF() || p.at(token.RParen) || p.atAny(interfaceBreak) {
			break
		}
		p.errorExpected("';' or ')'")
		p.recover(stop)
	}
	p.expect(token.RParen)
	p.close()
}

// parseInterfaceElement parses one interface declaration: an object, or
// (2008, generic lists) a type, subprogram or package. False, consuming
// nothing, if none starts here.
func (p *Parser) parseInterfaceElement(k InterfaceKind) bool {
	_ = k
	s, e := p.curSpan()
	switch p.nth(0) {
	case token.KwType:
		p.gateRule("generic_type", s, e)
		p.open(cst.KindInterfaceTypeDecl)
		p.bump()
		p.ident()
		if p.eatKw(token.KwIs) && !p.parseIncompleteTypeDef() {
			p.errorExpected("an incomplete type definition")
			p.recover(semiOrRParen)
		}
		p.close()
	case token.KwFunction, token.KwProcedure, token.KwPure,
		token.KwImpure:
		p.gateRule("generic_subp", s, e)
		p.open(cst.KindInterfaceSubprogramDecl)
		p.parseSubprogramSpec()
		if p.atKw(token.KwIs) {
			p.open(cst.KindInterfaceSubprogramDefault)
			p.bumpKw(token.KwIs)
			if !p.eat(token.Box) {
				p.declName()
			}
			p.close()
		}
		p.close()
	case token.KwPackage:
		p.gateRule("generic_pkg", s, e)
		p.open(cst.KindInterfacePackageDecl)
		p.bump()
		p.ident()
		p.expectKw(token.KwIs)
		p.expectKw(token.KwNew)
		p.declTypeMark()
		if p.atKw(token.KwGeneric) {
			p.parseGenericMapAspect()
		} else {
			p.errorExpected("generic map")
		}
		p.close()
	case token.KwConstant, token.KwSignal, token.KwVariable,
		token.KwFile:
		p.parseInterfaceObject()
	default:
		if !p.atIdent() {
			return false
		}
		p.parseInterfaceObject()
	}
	return true
}

// interfaceModes are the simple interface modes.
var interfaceModes = token.NewSet(token.KwIn, token.KwOut,
	token.KwInout, token.KwBuffer, token.KwLinkage)

// parseInterfaceObject parses `[class] names : [mode] subtype [bus] [:=
// default]`; the mode may be a 2019 mode view and the subtype a 2019
// anonymous type.
func (p *Parser) parseInterfaceObject() {
	p.open(cst.KindInterfaceObjectDecl)
	switch p.nth(0) {
	case token.KwConstant, token.KwSignal, token.KwVariable,
		token.KwFile:
		p.bump()
	}
	p.declIdentList()
	p.expect(token.Colon)
	switch {
	case interfaceModes.Has(p.nth(0)):
		p.open(cst.KindSimpleModeIndication)
		p.bump()
		p.close()
		p.interfaceSubtype()
	case p.atModeView():
		if !p.parseModeViewIndication() {
			p.errorExpected("a mode view indication")
			p.recover(semiOrRParen)
		}
	default:
		p.interfaceSubtype()
	}
	if p.atKw(token.KwBus) {
		p.bump()
	}
	if p.eat(token.VarAssign) {
		p.declValue()
	}
	p.close()
}

// atModeView reports whether a 2019 mode view indication starts here:
// the reserved word view, or (before 2019) an identifier spelling view
// followed by a name or `(`.
func (p *Parser) atModeView() bool {
	switch {
	case p.at(token.KwView):
		return true
	case p.atKw(token.KwView):
		return p.nthIsIdent(1) || p.nth(1) == token.LParen
	}
	return false
}

// interfaceSubtype parses the subtype of an interface object: a subtype
// indication or a 2019 anonymous type indication (`type is ...`).
func (p *Parser) interfaceSubtype() {
	if p.at(token.KwType) {
		if !p.parseAnonymousTypeIndication() {
			p.errorExpected("an anonymous type indication")
			p.recover(semiOrRParen)
		}
		return
	}
	p.declSubtype()
}
