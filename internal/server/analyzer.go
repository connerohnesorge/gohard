package server

import (
	"encoding/json"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/span"
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
	// Rename returns the edits of every affected file, or why the
	// rename is refused (the server answers RequestFailed with it).
	Rename(q Query, newName string) Task[Renamed]
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
	// CodeActions returns the code actions of the range r of the
	// document q.URI (q.Offset is r.Start): the quick fixes of its
	// diagnostics (add a missing use, map an unmapped library, #25,
	// #34), then the rewrites and source actions there (instantiate
	// an entity, declare a component, complete a port map, create a
	// testbench, #57). create reports whether the client applies
	// CreateFile resource operations; without it no action creates a
	// file.
	CodeActions(q Query, r Span, create bool) Task[[]CodeAction]
	// SemanticTokens returns the semantic tokens of the whole
	// document uri, sorted by position and not overlapping (#55); a
	// token may span lines. The server splits them per line, encodes
	// them in the negotiated position encoding and answers range and
	// delta requests from them.
	SemanticTokens(snap *Snapshot, uri string) Task[[]SemToken]
	// SignatureHelp returns the signatures of the call, generic map
	// or port map around q, or nil (#56).
	SignatureHelp(q Query) Task[*SignatureHelp]
	// InlayHints returns the inlay hints of the document uri whose
	// position lies in r, of the kinds the snapshot's configuration
	// turns on (snap.Config.InlayHints, #56), sorted by position.
	InlayHints(snap *Snapshot, uri string, r Span) Task[[]InlayHint]
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
	FeatureCodeActions
	FeatureSemanticTokens
	FeatureSignatureHelp
	FeatureInlayHints
)

// Features is what an Analyzer provides.
type Features struct {
	Set Feature
	// Commands are the workspace/executeCommand names (FeatureCommands).
	Commands []string
	// CompletionTriggers are extra completion trigger characters.
	CompletionTriggers []string
	// TokenTypes and TokenModifiers are the semantic tokens legend
	// (FeatureSemanticTokens): SemToken.Type indexes TokenTypes and bit
	// i of SemToken.Mods stands for TokenModifiers[i].
	TokenTypes, TokenModifiers []string
	// SignatureTriggers are the signature help trigger characters
	// (FeatureSignatureHelp).
	SignatureTriggers []string
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

// Location is a span in any document of the snapshot, or in a
// read-only text outside it (a bundled std/ieee file, #33), whose line
// index is then Lines.
type Location struct {
	URI   string
	Span  Span
	Lines *span.Index
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

// FileEdit are the edits of one document. With Create the document
// does not exist yet: the edit creates it (a CreateFile resource
// operation, #57) and Edits insert its text at offset 0.
type FileEdit struct {
	URI    string
	Edits  []Edit
	Create bool
}

// Renamed is the result of a rename: the edits of every affected
// file, or, when Refused is set, why there are none.
type Renamed struct {
	Edits   []FileEdit
	Refused string
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

// CodeAction is a quick fix, rewrite or source action: the edits of
// every affected document, which may include gohard.yaml
// (Snapshot.ConfigDocument) and a document it creates.
type CodeAction struct {
	Title string
	// Kind is the LSP CodeActionKind ("quickfix", "refactor.rewrite"
	// or "source").
	Kind      string
	Preferred bool
	Edits     []FileEdit
}

// The code action kinds of CodeAction.Kind; the server advertises
// them in codeActionKinds (#57).
const (
	ActionQuickFix = "quickfix"
	ActionRewrite  = "refactor.rewrite"
	ActionSource   = "source"
)

// SemToken is one semantic token: Span in one document, Type an index
// into Features.TokenTypes, Mods a set of Features.TokenModifiers
// (bit i for TokenModifiers[i]).
type SemToken struct {
	Span Span
	Type int
	Mods uint32
}

// SignatureHelp is the signature help of a position: every signature
// that may apply, the active one first.
type SignatureHelp struct {
	Signatures []Signature
}

// Signature is one signature: a subprogram overload, or the generic or
// port list of an instantiated unit.
type Signature struct {
	Label string
	// Doc is markdown documentation ("" for none).
	Doc string
	// Params are the parameters as byte ranges [Start, End) of Label;
	// the server converts them to the client's position encoding.
	Params []Span
	// Active is the index into Params of the active parameter, or -1.
	Active int
}

// InlayHintKind is the LSP InlayHintKind; 0 sends none (a value).
type InlayHintKind int

// Inlay hint kinds.
const (
	HintType      InlayHintKind = 1
	HintParameter InlayHintKind = 2
)

// InlayHint is a label shown at a byte offset of a document.
type InlayHint struct {
	Offset   int
	Label    string
	Kind     InlayHintKind
	PadLeft  bool
	PadRight bool
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
func (Nop) Rename(Query, string) Task[Renamed] {
	return Done(Renamed{})
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

// CodeActions implements Analyzer.
func (Nop) CodeActions(Query, Span, bool) Task[[]CodeAction] {
	return Done[[]CodeAction](nil)
}

// SemanticTokens implements Analyzer.
func (Nop) SemanticTokens(*Snapshot, string) Task[[]SemToken] {
	return Done[[]SemToken](nil)
}

// SignatureHelp implements Analyzer.
func (Nop) SignatureHelp(Query) Task[*SignatureHelp] {
	return Done[*SignatureHelp](nil)
}

// InlayHints implements Analyzer.
func (Nop) InlayHints(*Snapshot, string, Span) Task[[]InlayHint] {
	return Done[[]InlayHint](nil)
}
