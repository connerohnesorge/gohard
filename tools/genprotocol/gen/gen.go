// Package gen turns the LSP metaModel.json into the Go types of
// internal/protocol (#25). It is a pure function of the model bytes:
// it walks the type closure of the methods in [Methods] (and the types
// in [Extra]) in a fixed order and emits every declaration sorted by
// name, so the same model always gives the same file.
//
// # Mapping
//
//   - base types: integer is int32, uinteger uint32, decimal float64,
//     boolean bool, string string; DocumentUri and URI are the string
//     types DocumentURI and URI.
//   - structures become structs. extends and mixins are flattened in
//     order (extends, mixins, own properties; a redefined property
//     keeps its first position), so field order, and with it the JSON
//     key order, is fixed.
//   - optional properties are omitted when empty. Optional numbers and
//     booleans, optional enums that have a zero member, structures and
//     nullable values are pointers, so absent and zero differ. A
//     required array or map that is nil is written as an empty one,
//     never as null, by a generated MarshalJSON.
//   - "X | null" is a pointer (or a nil slice, map or raw value). An
//     or of several types is a struct with one field, Value, holding a
//     value of one alternative type; it writes Value and reads the
//     alternative whose JSON kind matches, choosing between object
//     shapes by required and known keys (see internal/protocol).
//   - literal types become structs named after where they appear
//     (parent and property), with a few hand-picked names ([Names]).
//   - stringLiteral is string, tuple a Go array, map a Go map;
//     LSPAny is json.RawMessage, so any value round-trips unchanged.
//   - enumerations become defined types with one constant per member,
//     named type plus member.
//
// Proposed (post-3.17) structures, properties and members are left
// out. A type kind the mapping does not handle is an error, not a
// guess.
package gen

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Methods are the LSP methods GoHard's server handles or sends (#11,
// #25). Their parameter, result and registration types are the roots
// of the generated closure.
var Methods = []string{
	// Lifecycle and general messages.
	"initialize", "initialized", "shutdown", "exit",
	"$/cancelRequest", "$/progress", "$/setTrace", "$/logTrace",
	"client/registerCapability", "client/unregisterCapability",
	// Window.
	"window/logMessage", "window/showMessage",
	"window/showMessageRequest", "window/workDoneProgress/create",
	"window/workDoneProgress/cancel",
	// Workspace.
	"workspace/configuration", "workspace/didChangeConfiguration",
	"workspace/didChangeWatchedFiles",
	"workspace/didChangeWorkspaceFolders", "workspace/workspaceFolders",
	"workspace/symbol", "workspace/executeCommand", "workspace/applyEdit",
	"workspace/codeLens/refresh", "workspace/inlayHint/refresh",
	// Text synchronization and diagnostics.
	"textDocument/didOpen", "textDocument/didChange",
	"textDocument/didClose", "textDocument/didSave",
	"textDocument/publishDiagnostics",
	// Language features.
	"textDocument/hover", "textDocument/definition",
	"textDocument/references", "textDocument/prepareRename",
	"textDocument/rename", "textDocument/completion",
	"completionItem/resolve", "textDocument/documentSymbol",
	"textDocument/formatting", "textDocument/rangeFormatting",
	"textDocument/foldingRange", "textDocument/documentHighlight",
	"textDocument/codeLens", "codeLens/resolve",
	"textDocument/codeAction", "textDocument/signatureHelp",
	"textDocument/inlayHint",
}

// Extra are types needed beyond the closure of Methods: the
// $/progress values (sent as LSPAny) and the error code tables.
var Extra = []string{
	"WorkDoneProgressBegin", "WorkDoneProgressReport",
	"WorkDoneProgressEnd", "ErrorCodes", "LSPErrorCodes",
}

// Names gives hand-picked names to literal types, keyed by
// the name the generator would derive (parent plus property, plus an
// index among the literals of one or type). Most follow the names LSP
// 3.18 gave these types.
var Names = map[string]string{
	"TextDocumentContentChangeEvent1":     "TextDocumentContentChangePartial",
	"TextDocumentContentChangeEvent2":     "TextDocumentContentChangeWhole",
	"MarkedString":                        "MarkedStringWithLanguage",
	"PrepareRenameResult1":                "PrepareRenamePlaceholder",
	"PrepareRenameResult2":                "PrepareRenameDefaultBehavior",
	"TextDocumentFilter1":                 "TextDocumentFilterLanguage",
	"TextDocumentFilter2":                 "TextDocumentFilterScheme",
	"TextDocumentFilter3":                 "TextDocumentFilterPattern",
	"NotebookDocumentFilter1":             "NotebookDocumentFilterNotebookType",
	"NotebookDocumentFilter2":             "NotebookDocumentFilterScheme",
	"NotebookDocumentFilter3":             "NotebookDocumentFilterPattern",
	"CompletionListItemDefaultsEditRange": "EditRangeWithInsertReplace",
	"CompletionOptionsCompletionItem":     "ServerCompletionItemOptions",
	"CompletionRegistrationOptionsCompletionItem": "" +
		"CompletionRegistrationItem",
	"CodeActionClientCapabilitiesResolveSupport": "" +
		"ClientCodeActionResolveOptions",
	"CodeActionClientCapabilitiesCodeActionLiteralSupport": "" +
		"ClientCodeActionLiteralOptions",
	"ClientCodeActionLiteralOptionsCodeActionKind": "" +
		"ClientCodeActionKindOptions",
	"CompletionClientCapabilitiesCompletionItem": "" +
		"ClientCompletionItemOptions",
	"ClientCompletionItemOptionsTagSupport": "CompletionItemTagOptions",
	"ClientCompletionItemOptionsResolveSupport": "" +
		"ClientCompletionItemResolveOptions",
	"ClientCompletionItemOptionsInsertTextModeSupport": "" +
		"ClientInsertTextModeOptions",
	"CompletionClientCapabilitiesCompletionItemKind": "" +
		"ClientCompletionItemKindOptions",
	"CompletionClientCapabilitiesCompletionList": "" +
		"CompletionListCapabilities",
	"DocumentSymbolClientCapabilitiesSymbolKind": "" +
		"DocumentSymbolKindOptions",
	"DocumentSymbolClientCapabilitiesTagSupport": "" +
		"DocumentSymbolTagOptions",
	"WorkspaceSymbolClientCapabilitiesSymbolKind": "" +
		"WorkspaceSymbolKindOptions",
	"WorkspaceSymbolClientCapabilitiesTagSupport": "" +
		"WorkspaceSymbolTagOptions",
	"WorkspaceSymbolClientCapabilitiesResolveSupport": "" +
		"ClientSymbolResolveOptions",
	"FoldingRangeClientCapabilitiesFoldingRange": "" +
		"ClientFoldingRangeOptions",
	"FoldingRangeClientCapabilitiesFoldingRangeKind": "" +
		"ClientFoldingRangeKindOptions",
	"GeneralClientCapabilitiesStaleRequestSupport": "" +
		"StaleRequestSupportOptions",
	"PublishDiagnosticsClientCapabilitiesTagSupport": "" +
		"ClientDiagnosticsTagOptions",
	"ShowMessageRequestClientCapabilitiesMessageActionItem": "" +
		"ClientShowMessageActionItemOptions",
	"WorkspaceEditClientCapabilitiesChangeAnnotationSupport": "" +
		"ChangeAnnotationsSupportOptions",
}

// Skip are properties left out ("Parent.property", Parent being the
// Go name of the structure or literal): the client and server
// capabilities of features GoHard does not have (#11: no notebooks,
// pull diagnostics, hierarchies and the like; semantic tokens came in
// v2, #55, signature help and inlay hints in #56). Decoding ignores
// unknown keys, so a client that sends them is still read correctly.
var Skip = map[string]bool{}

func init() {
	notV1 := []string{"declaration", "typeDefinition", "implementation",
		"documentLink", "colorProvider",
		"onTypeFormatting", "selectionRange", "linkedEditingRange",
		"callHierarchy", "moniker", "typeHierarchy",
		"inlineValue", "diagnostic"}
	for _, f := range notV1 {
		Skip["TextDocumentClientCapabilities."+f] = true
		p := f
		if p != "colorProvider" {
			p += "Provider"
		}
		if f == "onTypeFormatting" {
			p = "documentOnTypeFormattingProvider"
		}
		Skip["ServerCapabilities."+p] = true
	}
	for _, f := range []string{"ClientCapabilities.notebookDocument",
		"ServerCapabilities.notebookDocumentSync",
		"WorkspaceClientCapabilities.inlineValue",
		"WorkspaceClientCapabilities.diagnostics",
		"WorkspaceClientCapabilities.fileOperations",
		"ServerCapabilitiesWorkspace.fileOperations",
	} {
		Skip[f] = true
	}
}

// raw is the Go type of LSPAny and its relatives.
var rawAliases = map[string]string{
	"LSPAny":    "json.RawMessage",
	"LSPObject": "map[string]LSPAny",
	"LSPArray":  "[]LSPAny",
}

type field struct {
	goName, jsonName, goType, tag string
	doc, deprecated               string
	empty                         string // value for a nil required list
}

type structDecl struct {
	name, doc, deprecated string
	fields                []field
}

type orDecl struct {
	name, doc string
	items     []*Type
	itemCtx   []string // literal name for each literal item
	key       string   // canonical JSON of items, for collisions
}

type aliasDecl struct {
	name, goType, doc, deprecated string
}

type generator struct {
	m       *Model
	structs map[string]*Structure
	enums   map[string]*Enumeration
	aliases map[string]*TypeAlias
	calls   map[string]*Call

	emitted  map[string]bool // every declared Go name
	decls    map[string]*structDecl
	ors      map[string]*orDecl
	alias    map[string]*aliasDecl
	enumUse  map[string]bool
	shapes   map[string][]Property // struct or literal name -> props
	literals map[string]bool
	queue    []string
}

// Generate returns the gofmt-formatted Go source of internal/protocol's
// generated file for the metaModel.json bytes.
func Generate(model []byte) ([]byte, error) {
	var m Model
	if err := json.Unmarshal(model, &m); err != nil {
		return nil, fmt.Errorf("metaModel.json: %w", err)
	}
	g := &generator{
		m:        &m,
		structs:  map[string]*Structure{},
		enums:    map[string]*Enumeration{},
		aliases:  map[string]*TypeAlias{},
		calls:    map[string]*Call{},
		emitted:  map[string]bool{},
		decls:    map[string]*structDecl{},
		ors:      map[string]*orDecl{},
		alias:    map[string]*aliasDecl{},
		enumUse:  map[string]bool{},
		shapes:   map[string][]Property{},
		literals: map[string]bool{},
	}
	for i := range m.Structures {
		if s := &m.Structures[i]; !s.Proposed {
			g.structs[s.Name] = s
		}
	}
	for i := range m.Enumerations {
		if e := &m.Enumerations[i]; !e.Proposed {
			g.enums[e.Name] = e
		}
	}
	for i := range m.TypeAliases {
		if a := &m.TypeAliases[i]; !a.Proposed {
			g.aliases[a.Name] = a
		}
	}
	for _, list := range [][]Call{m.Requests, m.Notifications} {
		for i := range list {
			if c := &list[i]; !c.Proposed {
				g.calls[c.Method] = c
			}
		}
	}
	src, err := g.generate()
	if err != nil {
		return nil, err
	}
	out, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("gofmt of generated code: %w\n%s", err, src)
	}
	return out, nil
}

func (g *generator) generate() ([]byte, error) {
	var consts bytes.Buffer
	for _, method := range Methods {
		c, ok := g.calls[method]
		if !ok {
			return nil, fmt.Errorf("method %q is not in the model", method)
		}
		if err := g.method(&consts, c); err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
	}
	for _, name := range Extra {
		t := &Type{Kind: "reference", Name: name}
		if _, err := g.goType(t, name); err != nil {
			return nil, fmt.Errorf("extra type %s: %w", name, err)
		}
	}
	for len(g.queue) > 0 {
		name := g.queue[0]
		g.queue = g.queue[1:]
		if err := g.structure(g.structs[name]); err != nil {
			return nil, fmt.Errorf("structure %s: %w", name, err)
		}
	}
	return g.emit(consts.Bytes())
}

// method writes the constant for one method and walks its types.
func (g *generator) method(w *bytes.Buffer, c *Call) error {
	name := "Method" + methodName(c.Method)
	if err := g.declare(name); err != nil {
		return err
	}
	kind := "notification"
	if slices.ContainsFunc(g.m.Requests, func(r Call) bool {
		return r.Method == c.Method
	}) {
		kind = "request"
	}
	dir := map[string]string{
		"clientToServer": "client to server",
		"serverToClient": "server to client",
		"both":           "both directions",
	}[c.MessageDirection]
	var info []string
	params, err := c.params()
	if err != nil {
		return err
	}
	for _, p := range params {
		s, err := g.describe(p, name+"Params")
		if err != nil {
			return err
		}
		info = append(info, "Params: "+s+".")
	}
	if c.Result != nil {
		s, err := g.describe(c.Result, name+"Result")
		if err != nil {
			return err
		}
		info = append(info, "Result: "+s+".")
	}
	for _, t := range []*Type{c.PartialResult, c.RegistrationOptions} {
		if t != nil {
			if _, err := g.goType(t, name+"Options"); err != nil {
				return err
			}
		}
	}
	text := fmt.Sprintf("%s is the %q %s (%s).", name, c.Method, kind,
		dir)
	if len(info) > 0 {
		text += " " + strings.Join(info, " ")
	}
	comment(w, "", text)
	line := fmt.Sprintf("const %s = %q", name, c.Method)
	if !fits(line) {
		line = "const " + name + " = " + split(strconv.Quote(c.Method),
			"\t")
	}
	w.WriteString(line + "\n\n")
	return nil
}

// describe returns the Go type of a parameter or result for a method
// comment, with "| null" spelled out.
func (g *generator) describe(t *Type, ctx string) (string, error) {
	nonNull, nullable := stripNull(t)
	if nonNull.Kind == "base" && nonNull.Name == "null" {
		return "null", nil
	}
	s, err := g.goType(nonNull, ctx)
	if err != nil {
		return "", err
	}
	if nullable {
		s += " or null"
	}
	return s, nil
}

// stripNull removes null from an or type. It returns the remaining
// type and whether null was among the items.
func stripNull(t *Type) (*Type, bool) {
	if t.Kind != "or" {
		return t, false
	}
	var items []*Type
	for _, it := range t.Items {
		if !(it.Kind == "base" && it.Name == "null") {
			items = append(items, it)
		}
	}
	switch {
	case len(items) == len(t.Items):
		return t, false
	case len(items) == 1:
		return items[0], true
	}
	return &Type{Kind: "or", Items: items}, true
}

func (g *generator) declare(name string) error {
	if g.emitted[name] {
		return fmt.Errorf("Go name %s declared twice", name)
	}
	g.emitted[name] = true
	return nil
}

// goType returns the Go type for t, registering every declaration it
// needs. ctx names a literal type found at this place.
func (g *generator) goType(t *Type, ctx string) (string, error) {
	switch t.Kind {
	case "base":
		return baseType(t.Name)
	case "reference":
		return g.reference(t.Name)
	case "array":
		if t.Element == nil {
			return "", errors.New("array without element")
		}
		el, err := g.goType(t.Element, ctx)
		return "[]" + el, err
	case "map":
		if t.Key == nil {
			return "", errors.New("map without key")
		}
		k, err := g.goType(t.Key, ctx)
		if err != nil {
			return "", err
		}
		vt, err := t.mapValue()
		if err != nil {
			return "", err
		}
		v, err := g.goType(vt, ctx)
		return "map[" + k + "]" + v, err
	case "or":
		nonNull, nullable := stripNull(t)
		if nullable && nonNull.Kind != "or" {
			return g.goType(nonNull, ctx)
		}
		return g.or(nonNull.Items, ctx, "")
	case "literal":
		return g.literal(t, ctx)
	case "stringLiteral":
		return "string", nil
	case "booleanLiteral":
		return "bool", nil
	case "integerLiteral":
		return "int32", nil
	case "tuple":
		if len(t.Items) == 0 {
			return "", errors.New("empty tuple")
		}
		el, err := g.goType(t.Items[0], ctx)
		if err != nil {
			return "", err
		}
		for _, it := range t.Items[1:] {
			if o, err := g.goType(it, ctx); err != nil || o != el {
				return "", errors.New("tuple of mixed types")
			}
		}
		return "[" + strconv.Itoa(len(t.Items)) + "]" + el, nil
	}
	return "", fmt.Errorf("type kind %q is not supported", t.Kind)
}

func baseType(name string) (string, error) {
	switch name {
	case "integer":
		return "int32", nil
	case "uinteger":
		return "uint32", nil
	case "decimal":
		return "float64", nil
	case "boolean":
		return "bool", nil
	case "string":
		return "string", nil
	case "DocumentUri":
		return "DocumentURI", nil
	case "URI":
		return "URI", nil
	}
	return "", fmt.Errorf("base type %q is not supported", name)
}

func (g *generator) reference(name string) (string, error) {
	if s, ok := g.structs[name]; ok {
		if _, done := g.decls[name]; !done && !g.emitted[name] {
			if err := g.declare(name); err != nil {
				return "", err
			}
			g.decls[name] = &structDecl{name: name, doc: s.Documentation,
				deprecated: s.Deprecated}
			g.queue = append(g.queue, name)
		}
		return name, nil
	}
	if _, ok := g.enums[name]; ok {
		if !g.enumUse[name] {
			if err := g.declare(name); err != nil {
				return "", err
			}
			g.enumUse[name] = true
		}
		return name, nil
	}
	a, ok := g.aliases[name]
	if !ok {
		return "", fmt.Errorf("unknown or proposed type %s", name)
	}
	if _, done := g.alias[name]; done || g.ors[name] != nil {
		return name, nil
	}
	if goT, ok := rawAliases[name]; ok {
		if err := g.declare(name); err != nil {
			return "", err
		}
		g.alias[name] = &aliasDecl{name: name, goType: goT,
			doc: a.Documentation}
		if name != "LSPAny" {
			if _, err := g.reference("LSPAny"); err != nil {
				return "", err
			}
		}
		return name, nil
	}
	if nonNull, _ := stripNull(a.Type); nonNull.Kind == "or" {
		return g.or(nonNull.Items, name, name, a.Documentation)
	}
	if err := g.declare(name); err != nil {
		return "", err
	}
	d := &aliasDecl{name: name, doc: a.Documentation,
		deprecated: a.Deprecated}
	g.alias[name] = d
	goT, err := g.goType(a.Type, name)
	d.goType = goT
	return name, err
}

// literal registers the struct for a literal type named ctx.
func (g *generator) literal(t *Type, ctx string) (string, error) {
	name := cmp.Or(Names[ctx], ctx)
	if g.literals[name] {
		return name, nil // seen again, e.g. while emitting its or type
	}
	props, err := t.literal()
	if err != nil {
		return "", err
	}
	if err := g.declare(name); err != nil {
		return "", err
	}
	g.literals[name] = true
	d := &structDecl{name: name}
	g.decls[name] = d
	d.fields, err = g.fields(name, props)
	g.shapes[name] = props
	return name, err
}

// or registers the or type for items. ctx names literal items; name,
// when set, is the type's name (an alias), else it is derived from the
// items.
func (g *generator) or(items []*Type, ctx, name string,
	doc ...string) (string, error) {
	var itemCtx []string
	lits := 0
	for _, it := range items {
		if it.Kind == "literal" {
			lits++
		}
	}
	n := 0
	for _, it := range items {
		c := ctx
		if it.Kind == "literal" && lits > 1 {
			n++
			c = ctx + strconv.Itoa(n)
		}
		itemCtx = append(itemCtx, c)
	}
	if name == "" {
		var parts []string
		for i, it := range items {
			parts = append(parts, itemName(it, itemCtx[i]))
		}
		name = strings.Join(parts, "Or")
	}
	keyB, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	key := string(keyB)
	if o, ok := g.ors[name]; ok {
		if o.key != key {
			return "", fmt.Errorf("or type name %s used for two types",
				name)
		}
		return name, nil
	}
	if err := g.declare(name); err != nil {
		return "", err
	}
	o := &orDecl{name: name, items: items, itemCtx: itemCtx, key: key}
	if len(doc) > 0 {
		o.doc = doc[0]
	}
	g.ors[name] = o
	for i, it := range items {
		if _, err := g.goType(it, itemCtx[i]); err != nil {
			return "", err
		}
	}
	return name, nil
}

// itemName is the part of an or type's name for one item.
func itemName(t *Type, ctx string) string {
	switch t.Kind {
	case "base":
		return map[string]string{
			"integer": "Integer", "uinteger": "UInteger",
			"decimal": "Decimal", "boolean": "Bool", "string": "String",
			"DocumentUri": "DocumentURI", "URI": "URI",
		}[t.Name]
	case "reference":
		return t.Name
	case "array":
		if t.Element != nil {
			return itemName(t.Element, ctx) + "s"
		}
	case "literal":
		return cmp.Or(Names[ctx], ctx)
	case "tuple":
		return "Tuple"
	case "map":
		return "Map"
	}
	return "Literal"
}

// structure computes the flattened fields of a named structure.
func (g *generator) structure(s *Structure) error {
	props, err := g.props(s, 0)
	if err != nil {
		return err
	}
	d := g.decls[s.Name]
	d.fields, err = g.fields(s.Name, props)
	g.shapes[s.Name] = props
	return err
}

// props returns the properties of s with extends and mixins flattened.
func (g *generator) props(s *Structure, depth int) ([]Property, error) {
	if depth > 32 {
		return nil, errors.New("extends cycle")
	}
	var out []Property
	index := map[string]int{}
	add := func(p Property) {
		if p.Proposed {
			return
		}
		if i, ok := index[p.Name]; ok {
			out[i] = p
			return
		}
		index[p.Name] = len(out)
		out = append(out, p)
	}
	for _, e := range slices.Concat(s.Extends, s.Mixins) {
		base, ok := g.structs[e.Name]
		if e.Kind != "reference" || !ok {
			return nil, fmt.Errorf("cannot flatten %s %q", e.Kind, e.Name)
		}
		inherited, err := g.props(base, depth+1)
		if err != nil {
			return nil, err
		}
		for _, p := range inherited {
			add(p)
		}
	}
	for _, p := range s.Properties {
		add(p)
	}
	return out, nil
}

func (g *generator) fields(parent string, props []Property) ([]field,
	error) {
	var out []field
	for _, p := range props {
		if Skip[parent+"."+p.Name] {
			continue
		}
		f, err := g.field(parent, p)
		if err != nil {
			return nil, fmt.Errorf("property %s: %w", p.Name, err)
		}
		out = append(out, f)
	}
	return out, nil
}

// field maps one property; see the package documentation for the
// pointer and omit rules.
func (g *generator) field(parent string, p Property) (field, error) {
	f := field{goName: fieldName(p.Name), jsonName: p.Name,
		doc: p.Documentation, deprecated: p.Deprecated}
	t, nullable := stripNull(p.Type)
	goT, err := g.goType(t, parent+f.goName)
	if err != nil {
		return f, err
	}
	ptr, omit := false, ""
	switch u := g.underlying(t); u.Kind {
	case "or", "any":
		omit = "omitzero"
		if u.Kind == "any" {
			omit = "omitempty"
		}
	case "array", "map":
		omit = "omitempty"
		if !p.Optional && !nullable {
			f.empty = goT + "{}"
		}
	case "base":
		switch u.Name {
		case "string", "DocumentUri", "URI":
			ptr, omit = nullable, "omitempty"
		default:
			ptr, omit = p.Optional || nullable, "omitempty"
		}
	case "struct", "tuple":
		ptr, omit = p.Optional || nullable, "omitempty"
	case "enum":
		ptr = nullable || (p.Optional && g.enumHasZero(u.Name))
		omit = "omitempty"
	default: // string and boolean literals
		ptr, omit = nullable, "omitempty"
	}
	if ptr {
		goT = "*" + goT
	}
	f.goType = goT
	f.tag = p.Name
	if p.Optional {
		if ptr {
			omit = "omitempty"
		}
		f.tag += "," + omit
	}
	return f, nil
}

// underlying classifies t after looking through aliases: its Kind is
// "or", "any" (LSPAny), "array", "map", "base" (Name is the base
// name), "struct", "enum" (Name is the enum), "tuple" or a literal
// kind.
func (g *generator) underlying(t *Type) *Type {
	for range 32 {
		switch t.Kind {
		case "literal":
			return &Type{Kind: "struct"}
		case "reference":
		default:
			return t
		}
		switch {
		case t.Name == "LSPAny":
			return &Type{Kind: "any"}
		case t.Name == "LSPObject":
			return &Type{Kind: "map"}
		case t.Name == "LSPArray":
			return &Type{Kind: "array"}
		case g.structs[t.Name] != nil:
			return &Type{Kind: "struct", Name: t.Name}
		case g.enums[t.Name] != nil:
			return &Type{Kind: "enum", Name: t.Name}
		case g.aliases[t.Name] != nil:
			nonNull, _ := stripNull(g.aliases[t.Name].Type)
			t = nonNull
		default:
			return &Type{Kind: "unknown"}
		}
	}
	return &Type{Kind: "unknown"}
}

func (g *generator) enumHasZero(name string) bool {
	for _, v := range g.enums[name].Values {
		if s := string(v.Value); s == "0" || s == `""` {
			return true
		}
	}
	return false
}

// methodName turns "textDocument/didOpen" into "TextDocumentDidOpen"
// and "$/cancelRequest" into "CancelRequest".
func methodName(m string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(m, "/") {
		if part == "$" {
			continue
		}
		b.WriteString(upperFirst(part))
	}
	return b.String()
}

// fieldName exports a property name and spells URI and ID in capitals.
func fieldName(name string) string {
	s := upperFirst(name)
	switch {
	case s == "Uri" || s == "Id":
		return strings.ToUpper(s)
	case strings.HasSuffix(s, "Uri"):
		return s[:len(s)-3] + "URI"
	case strings.HasSuffix(s, "Id") && len(s) > 2 &&
		unicode.IsLower(rune(s[len(s)-3])):
		return s[:len(s)-2] + "ID"
	}
	return s
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
