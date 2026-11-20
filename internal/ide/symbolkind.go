package ide

import (
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/protocol"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// symbolKindRow is one row of the outline's kind table. stmt is the
// labelled statement's CST kind for a statement label and zero for
// every other declaration.
type symbolKindRow struct {
	decl sema.DeclKind
	stmt cst.Kind
	kind protocol.SymbolKind
}

// symbolKinds is the LSP SymbolKind of every declaration document
// symbols and workspace symbols emit (#116). Both call SymbolKind, so
// one declaration has one kind in both results. CompletionKind is a
// different enum (completion items) and is not this table.
//
// A statement label's row is the labelled statement. A label whose
// statement is not listed uses the DeclLabel row with a zero stmt: the
// label names a region (a loop, an if, a case).
//
// Kinds follow the role an editor draws. An entity or component is an
// Interface and the architecture that implements it is a Module. A
// signal or port is a Variable. An instance is an Object. A process
// is a Method. A generate or block is a Module, like an architecture.
var symbolKinds = []symbolKindRow{
	{sema.DeclLibrary, 0, protocol.SymbolKindNamespace},
	{sema.DeclEntity, 0, protocol.SymbolKindInterface},
	{sema.DeclArchitecture, 0, protocol.SymbolKindModule},
	{sema.DeclPackage, 0, protocol.SymbolKindPackage},
	{sema.DeclPackageBody, 0, protocol.SymbolKindPackage},
	{sema.DeclPackageInst, 0, protocol.SymbolKindPackage},
	{sema.DeclConfiguration, 0, protocol.SymbolKindModule},
	{sema.DeclContext, 0, protocol.SymbolKindNamespace},
	{sema.DeclVunit, 0, protocol.SymbolKindModule},
	{sema.DeclType, 0, protocol.SymbolKindStruct},
	{sema.DeclSubtype, 0, protocol.SymbolKindStruct},
	{sema.DeclConstant, 0, protocol.SymbolKindConstant},
	{sema.DeclSignal, 0, protocol.SymbolKindVariable},
	{sema.DeclVariable, 0, protocol.SymbolKindVariable},
	{sema.DeclSharedVariable, 0, protocol.SymbolKindVariable},
	{sema.DeclFile, 0, protocol.SymbolKindFile},
	{sema.DeclAlias, 0, protocol.SymbolKindProperty},
	{sema.DeclAttribute, 0, protocol.SymbolKindProperty},
	{sema.DeclComponent, 0, protocol.SymbolKindInterface},
	{sema.DeclFunction, 0, protocol.SymbolKindFunction},
	{sema.DeclProcedure, 0, protocol.SymbolKindFunction},
	{sema.DeclEnumLiteral, 0, protocol.SymbolKindEnumMember},
	{sema.DeclUnit, 0, protocol.SymbolKindEnumMember},
	{sema.DeclElement, 0, protocol.SymbolKindField},
	{sema.DeclGeneric, 0, protocol.SymbolKindConstant},
	{sema.DeclGenericType, 0, protocol.SymbolKindTypeParameter},
	{sema.DeclGenericSubprogram, 0, protocol.SymbolKindFunction},
	{sema.DeclGenericPackage, 0, protocol.SymbolKindPackage},
	{sema.DeclPort, 0, protocol.SymbolKindVariable},
	{sema.DeclParameter, 0, protocol.SymbolKindVariable},
	{sema.DeclLoopParam, 0, protocol.SymbolKindConstant},
	{sema.DeclGenerateParam, 0, protocol.SymbolKindConstant},
	{sema.DeclGroup, 0, protocol.SymbolKindObject},
	{sema.DeclGroupTemplate, 0, protocol.SymbolKindStruct},
	{sema.DeclModeView, 0, protocol.SymbolKindInterface},
	{sema.DeclPSL, 0, protocol.SymbolKindProperty},

	// Statement labels.
	{sema.DeclLabel, cst.KindProcessStmt, protocol.SymbolKindMethod},
	{sema.DeclLabel, cst.KindComponentInstantiationStmt,
		protocol.SymbolKindObject},
	{sema.DeclLabel, cst.KindBlockStmt, protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindSequentialBlockStmt,
		protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindForGenerateStmt,
		protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindIfGenerateStmt, protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindCaseGenerateStmt,
		protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindGenerateBranch,
		protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindCaseGenerateAlternative,
		protocol.SymbolKindModule},
	{sema.DeclLabel, cst.KindConcurrentProcedureCallStmt,
		protocol.SymbolKindFunction},
	{sema.DeclLabel, cst.KindProcedureCallStmt,
		protocol.SymbolKindFunction},
	// Every other label: a loop, an if, a case, an assignment.
	{sema.DeclLabel, 0, protocol.SymbolKindNamespace},
}

// SymbolKind returns the LSP SymbolKind of declaration k. stmt is the
// labelled statement when k is a statement label and is ignored
// otherwise.
func SymbolKind(k sema.DeclKind, stmt cst.Kind) int {
	if k != sema.DeclLabel {
		stmt = 0
	}
	var fallback protocol.SymbolKind
	for _, row := range symbolKinds {
		if row.decl != k {
			continue
		}
		if row.stmt == stmt {
			return int(row.kind)
		}
		if row.stmt == 0 {
			fallback = row.kind
		}
	}
	if fallback != 0 {
		return int(fallback)
	}
	return int(protocol.SymbolKindVariable)
}
