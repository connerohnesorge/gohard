package server

import (
	"encoding/json"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// Analyzer computes the language features from a Snapshot. Later
// stages implement it (sema and resolve #34, format #32, sim #35); Nop
// is the default until they land.
//
// Every method is a pure function of its arguments and returns a Task,
// so the event loop can slice the work, cancel it at a slice boundary
// and drop results computed for an older snapshot (P3). Positions are
// byte offsets into the text of a Document of the snapshot; the server
// converts them to and from the client's position encoding, so an
// analyzer never sees UTF-16.
//
// Queries are workspace queries: a Snapshot holds every project file
// (open documents over the files on disk), so hover, definition,
// references and rename resolve across files and return Locations in
// any document of the snapshot.
type Analyzer interface {
	// Features reports what the analyzer provides. The server
	// advertises exactly these capabilities and answers MethodNotFound
	// for the others.
	Features() Features
	// Diagnostics checks the whole snapshot. Once the server is idle
	// the result equals `gohard check` on the same text (#25).
	Diagnostics(snap *Snapshot) Task[[]FileDiagnostics]
	Hover(q Query) Task[*Hover]
	Definition(q Query) Task[[]Location]
	References(q Query, includeDeclaration bool) Task[[]Location]
	// PrepareRename returns the span of the name at q, or nil when
	// there is nothing to rename there.
	PrepareRename(q Query) Task[*Span]
	// Rename returns the edits of every affected file.
	Rename(q Query, newName string) Task[[]FileEdit]
	Completion(q Query) Task[[]CompletionItem]
	DocumentSymbols(snap *Snapshot, uri string) Task[[]Symbol]
	WorkspaceSymbols(snap *Snapshot, query string) Task[[]Symbol]
	// Format formats the whole document (r nil) or only r; edits stay
	// inside r (#32).
	Format(snap *Snapshot, uri string, r *Span) Task[[]Edit]
	Folding(snap *Snapshot, uri string) Task[[]Fold]
	Highlights(q Query) Task[[]Highlight]
	CodeLens(snap *Snapshot, uri string) Task[[]Lens]
	// Command runs workspace/executeCommand for one of
	// Features.Commands (for example running a testbench, #35).
	Command(snap *Snapshot, name string,
		args []json.RawMessage) Task[json.RawMessage]
}

// Feature is one optional language feature.
type Feature uint32

// The features of #25.
const (
	FeatureDiagnostics Feature = 1 << iota
	FeatureHover
	FeatureDefinition
	FeatureReferences
	FeatureRename
	FeatureCompletion
	FeatureDocumentSymbols
	FeatureWorkspaceSymbols
	FeatureFormatting
	FeatureRangeFormatting
	FeatureFolding
	FeatureHighlights
	FeatureCodeLens
	FeatureCommands
)

// Features is what an Analyzer provides.
type Features struct {
	Set Feature
	// Commands are the workspace/executeCommand names (FeatureCommands).
	Commands []string
	// CompletionTriggers are extra completion trigger characters.
	CompletionTriggers []string
}

// Has reports whether f includes every feature in want.
func (f Features) Has(want Feature) bool { return f.Set&want == want }

// Query is a position in a document of a snapshot.
type Query struct {
	Snap   *Snapshot
	URI    string
	Offset int // byte offset into the document's text
}

// Span is a half-open byte range [Start, End) of one document.
type Span struct {
	Start int
	End   int
}

// Location is a span in any document of the snapshot.
type Location struct {
	URI  string
	Span Span
}

// FileDiagnostics are the diagnostics of one document. Line and Col are
// one-based UTF-8 positions in that document, like `gohard check`
// prints them (#20); a zero Line covers the whole file.
type FileDiagnostics struct {
	URI         string
	Diagnostics []diag.Diagnostic
}

// Hover is markdown shown for a span.
type Hover struct {
	Markdown string
	Span     *Span
}

// FileEdit are the edits of one document.
type FileEdit struct {
	URI   string
	Edits []Edit
}

// Edit replaces Span with NewText.
type Edit struct {
	Span    Span
	NewText string
}

// CompletionItem is one completion proposal. Kind is the LSP
// CompletionItemKind.
type CompletionItem struct {
	Label  string
	Kind   int
	Detail string
	// Edit, when set, replaces a span; otherwise Label is inserted.
	Edit *Edit
}

// Symbol is a document or workspace symbol. Kind is the LSP SymbolKind.
// Document symbols nest through Children; workspace symbols are flat.
type Symbol struct {
	Name      string
	Kind      int
	Detail    string
	URI       string
	Span      Span
	Selection Span
	Children  []Symbol
}

// Fold is a folding range. Kind is "", "comment", "imports" or
// "region".
type Fold struct {
	Span Span
	Kind string
}

// Highlight is an in-file highlight. Kind is the LSP
// DocumentHighlightKind (1 text, 2 read, 3 write).
type Highlight struct {
	Span Span
	Kind int
}

// Lens is a code lens with its command (for example "▶ Run tb_top").
type Lens struct {
	Span    Span
	Title   string
	Command string
	Args    []json.RawMessage
}

// Nop is the Analyzer of a server without analysis: it provides no
// features and every task finishes at once with an empty result.
type Nop struct{}

var _ Analyzer = Nop{}

// Features implements Analyzer.
func (Nop) Features() Features { return Features{} }

// Diagnostics implements Analyzer.
func (Nop) Diagnostics(*Snapshot) Task[[]FileDiagnostics] {
	return Done[[]FileDiagnostics](nil)
}

// Hover implements Analyzer.
func (Nop) Hover(Query) Task[*Hover] { return Done[*Hover](nil) }

// Definition implements Analyzer.
func (Nop) Definition(Query) Task[[]Location] {
	return Done[[]Location](nil)
}

// References implements Analyzer.
func (Nop) References(Query, bool) Task[[]Location] {
	return Done[[]Location](nil)
}

// PrepareRename implements Analyzer.
func (Nop) PrepareRename(Query) Task[*Span] { return Done[*Span](nil) }

// Rename implements Analyzer.
func (Nop) Rename(Query, string) Task[[]FileEdit] {
	return Done[[]FileEdit](nil)
}

// Completion implements Analyzer.
func (Nop) Completion(Query) Task[[]CompletionItem] {
	return Done[[]CompletionItem](nil)
}

// DocumentSymbols implements Analyzer.
func (Nop) DocumentSymbols(*Snapshot, string) Task[[]Symbol] {
	return Done[[]Symbol](nil)
}

// WorkspaceSymbols implements Analyzer.
func (Nop) WorkspaceSymbols(*Snapshot, string) Task[[]Symbol] {
	return Done[[]Symbol](nil)
}

// Format implements Analyzer.
func (Nop) Format(*Snapshot, string, *Span) Task[[]Edit] {
	return Done[[]Edit](nil)
}

// Folding implements Analyzer.
func (Nop) Folding(*Snapshot, string) Task[[]Fold] {
	return Done[[]Fold](nil)
}

// Highlights implements Analyzer.
func (Nop) Highlights(Query) Task[[]Highlight] {
	return Done[[]Highlight](nil)
}

// CodeLens implements Analyzer.
func (Nop) CodeLens(*Snapshot, string) Task[[]Lens] {
	return Done[[]Lens](nil)
}

// Command implements Analyzer.
func (Nop) Command(*Snapshot, string,
	[]json.RawMessage,
) Task[json.RawMessage] {
	return Done[json.RawMessage](nil)
}
