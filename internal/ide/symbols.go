package ide

import "github.com/connerohnesorge/gohard/internal/server"

func init() {
	provide(server.FeatureDocumentSymbols | server.FeatureWorkspaceSymbols)
}

// DocumentSymbols implements server.Analyzer: the declarations of the
// document as a tree (Analysis.DocumentSymbols).
func (a *Analyzer) DocumentSymbols(snap *server.Snapshot, uri string,
) server.Task[[]server.Symbol] {
	return Query(a, snap, func(an *Analysis) []server.Symbol {
		return an.DocumentSymbols(uri)
	})
}

// WorkspaceSymbols implements server.Analyzer: the design units and
// package declarations of the whole project mapped by gohard.yaml whose
// name contains query (Analysis.WorkspaceSymbols, #25).
func (a *Analyzer) WorkspaceSymbols(snap *server.Snapshot, query string,
) server.Task[[]server.Symbol] {
	return Query(a, snap, func(an *Analysis) []server.Symbol {
		return an.WorkspaceSymbols(query)
	})
}
