package parser

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// DeclRegion says which declarative part a declarative item is in, so
// parseDeclarativeItem can accept exactly the items that region allows
// (and the parser-gated rules can tell a nested package from a library
// unit).
type DeclRegion uint8

// Declarative regions.
const (
	RegionEntity DeclRegion = iota + 1
	RegionArchitecture
	RegionPackage
	RegionPackageBody
	RegionConfiguration
	RegionBlock
	RegionGenerate
	RegionProcess
	RegionSubprogram
	RegionProtected
	RegionProtectedBody
	RegionSequentialBlock
	RegionPSLUnit
)

// InterfaceKind says which interface list is being parsed.
type InterfaceKind uint8

// Interface list kinds.
const (
	InterfaceGeneric InterfaceKind = iota + 1
	InterfacePort
	InterfaceParameter
)

// Common stop sets.
var (
	endOnly    = token.NewSet(token.KwEnd)
	beginOrEnd = token.NewSet(token.KwBegin, token.KwEnd)
)

// declarativePart parses a DeclarativePart node: declarative items until
// a token in stop (not consumed) or the end of input.
func (p *Parser) declarativePart(r DeclRegion, stop token.Set) {
	p.open(cst.KindDeclarativePart)
	// Every nested region (package, subprogram or protected body,
	// process, block, statement) goes through one of the list loops,
	// so the depth limit holds on every recursion path (#31).
	if !p.enter() {
		p.tooDeep()
		p.leave()
		p.close()
		return
	}
	defer p.leave()
	item := func() bool { return p.parseDeclarativeItem(r) }
	for {
		if p.inactive(item) {
			continue
		}
		if p.atEOF() || p.atAny(stop) {
			break
		}
		if !item() {
			p.error("expected a declaration, found " + p.describeCurrent())
			p.recoverItem(stop.Union(declFirst))
		}
	}
	p.close()
}

// statementPart parses a StatementPart node: concurrent statements
// until a token in stop or the end of input.
func (p *Parser) statementPart(stop token.Set) {
	p.open(cst.KindStatementPart)
	// The depth limit, as in declarativePart.
	if !p.enter() {
		p.tooDeep()
		p.leave()
		p.close()
		return
	}
	defer p.leave()
	for {
		if p.inactive(p.parseConcurrentStatement) {
			continue
		}
		if p.atEOF() || p.atAny(stop) {
			break
		}
		if !p.parseConcurrentStatement() {
			p.error("expected a concurrent statement, found " +
				p.describeCurrent())
			p.recoverItem(stop.Union(concFirst))
		}
	}
	p.close()
}

// sequenceOfStatements parses a SequenceOfStatements node: sequential
// statements until a token in stop or the end of input.
func (p *Parser) sequenceOfStatements(stop token.Set) {
	p.open(cst.KindSequenceOfStatements)
	// The depth limit, as in declarativePart.
	if !p.enter() {
		p.tooDeep()
		p.leave()
		p.close()
		return
	}
	defer p.leave()
	for {
		if p.inactive(p.parseSequentialStatement) {
			continue
		}
		if p.atEOF() || p.atAny(stop) {
			break
		}
		if !p.parseSequentialStatement() {
			p.error("expected a sequential statement, found " +
				p.describeCurrent())
			p.recoverItem(stop.Union(seqFirst))
		}
	}
	p.close()
}

// recoverItem skips a broken list item into an ErrorNode: tokens up to
// and including the next `;`, or up to a token in sync. It always
// consumes at least one token unless at the end of input.
func (p *Parser) recoverItem(sync token.Set) {
	if p.atEOF() {
		return
	}
	p.open(cst.KindErrorNode)
	p.bump()
	for !p.atEOF() && !p.atAny(sync) {
		if p.eat(token.Semicolon) {
			break
		}
		p.bump()
	}
	p.close()
}
