// Package ide is the analysis bridge of the language server (#25): it
// implements server.Analyzer on top of sema and resolve (#34).
//
// An Analyzer analyzes each server.Snapshot as a whole workspace (the
// project files under the root, open buffers over the files on disk,
// std/ieee from the prebuilt bundle, #33) and keeps the result in
// memory; there is no on-disk cache. The analysis is a server.Task:
// one slice plans the work, then the files are parsed in bounded
// slices, then one slice runs sema.Analyze and the per-instance checks
// of internal/elab/hier, exactly as `gohard check` does. Parse trees
// and whole results are reused across snapshots (an edit re-parses
// only the edited file, and a snapshot with the same content reuses
// the result), but reuse only saves wall time: the number of slices
// and every value depend only on the snapshot (P1, P3).
//
// Analysis is the shared query layer of the features: position to
// occurrence to declaration, declaration to references across the
// workspace, the names visible at a position, and document and
// workspace symbols, all in server types (URIs and byte spans).
// Features take the analysis of their snapshot with Query.
package ide
