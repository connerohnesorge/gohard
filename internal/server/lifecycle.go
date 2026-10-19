package server

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/span"
)

type initializeParams struct {
	RootURI          *string `json:"rootUri"`
	RootPath         *string `json:"rootPath"`
	WorkspaceFolders []struct {
		URI  string `json:"uri"`
		Name string `json:"name"`
	} `json:"workspaceFolders"`
	InitializationOptions json.RawMessage `json:"initializationOptions"`
	Capabilities          struct {
		General struct {
			PositionEncodings []string `json:"positionEncodings"`
		} `json:"general"`
		Window struct {
			WorkDoneProgress bool `json:"workDoneProgress"`
		} `json:"window"`
	} `json:"capabilities"`
}

// Negotiate picks the position encoding (#25): utf-8 when the client
// offers it, else utf-16, which every client supports.
func Negotiate(offered []string) span.Encoding {
	if slices.Contains(offered, "utf-8") {
		return span.UTF8
	}
	return span.UTF16
}

// Folder returns the workspace folder of the initialize params: the
// first workspace folder, else rootUri, else rootPath. ok is false when
// the client sent none (a single-file session).
func (p *initializeParams) Folder() (string, bool) {
	for _, f := range p.WorkspaceFolders {
		if path, ok := URIToPath(f.URI); ok {
			return path, true
		}
	}
	if p.RootURI != nil {
		if path, ok := URIToPath(*p.RootURI); ok {
			return path, true
		}
	}
	if p.RootPath != nil && *p.RootPath != "" {
		return filepath.Clean(*p.RootPath), true
	}
	return "", false
}

// FolderOf returns the workspace folder named by an initialize request
// (the raw message). It is the same rule the server uses, so a session
// recorder and replay agree on the project root.
func FolderOf(initialize json.RawMessage) (string, bool) {
	f, ok := IsInitialize(initialize)
	return f, ok && f != ""
}

// IsInitialize reports whether body is an initialize request the
// server accepts (its params decode) and returns its workspace folder
// ("" for a single-file session).
func IsInitialize(body []byte) (folder string, ok bool) {
	msg, err := jsonrpc2.Decode(body)
	m, isReq := msg.(*jsonrpc2.Request)
	if err != nil || !isReq || m.Method != "initialize" {
		return "", false
	}
	var p initializeParams
	if len(m.Params) > 0 && json.Unmarshal(m.Params, &p) != nil {
		return "", false
	}
	folder, _ = p.Folder()
	return folder, true
}

func (s *State) initialize(o *out, m call) {
	if s.phase != phaseNew {
		s.fail(o, m.id, jsonrpc2.CodeInvalidRequest,
			"initialize was already received")
		return
	}
	var p initializeParams
	if len(m.params) > 0 && json.Unmarshal(m.params, &p) != nil {
		s.fail(o, m.id, jsonrpc2.CodeInvalidParams, "invalid initialize params")
		return
	}
	s.enc = Negotiate(p.Capabilities.General.PositionEncodings)
	s.progressOK = p.Capabilities.Window.WorkDoneProgress
	s.folder, _ = p.Folder()
	s.root = s.folder
	s.phase = phaseInitialized
	if ed, ok := editorSettings(p.InitializationOptions); ok {
		s.setEditor(o, ed)
	}
	s.respond(o, m.id, struct {
		Capabilities any `json:"capabilities"`
		ServerInfo   any `json:"serverInfo"`
	}{s.capabilities(), struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{"gohard", s.opts.Version}})
}

func (s *State) initialized(o *out) {
	if s.phase != phaseInitialized {
		return
	}
	s.phase = phaseRunning
	if s.folder == "" {
		// A single-file session: no project, defaults and open
		// documents only.
		s.loaded = true
		s.newSnapshot(o)
		s.unpark(o)
		return
	}
	o.do(LoadConfig{Folder: s.folder})
}

func (s *State) shutdown(o *out, m call) {
	s.phase = phaseShutdown
	for _, k := range sortedKeys(s.byRequest) {
		w := s.work[s.byRequest[k]]
		s.finish(o, w)
		s.fail(o, w.reqID, jsonrpc2.CodeRequestCancelled,
			"the server is shutting down")
	}
	s.dropWork(s.diagWork)
	s.respond(o, m.id, nil)
}

// configLoaded stores gohard.yaml, publishes its diagnostics, merges
// the editor settings and starts reading the workspace.
func (s *State) configLoaded(o *out, l config.Loaded) {
	if s.phase != phaseRunning {
		return
	}
	s.file = &l
	s.root = l.Root
	s.publishConfig(o, l)
	s.merge(o)
	o.do(LoadWorkspace{Root: l.Root})
}

func (s *State) workspaceLoaded(o *out, w WorkspaceLoaded) {
	if s.phase != phaseRunning || w.Root != s.root {
		return
	}
	docs := make([]*Document, 0, len(w.Sources))
	for _, src := range w.Sources {
		name := filepath.Join(s.root, filepath.FromSlash(src.Path))
		docs = append(docs, newDocument(PathToURI(name), s.root,
			src.Text))
	}
	s.disk = docs
	s.discovery = w.Diagnostics
	s.loaded = true
	// Open documents were made before the root was known.
	open := map[string]*Document{}
	for _, k := range sortedKeys(s.open) {
		d := s.open[k]
		n := newDocument(d.URI, s.root, d.text)
		n.Version, n.Open, n.LanguageID = d.Version, true, d.LanguageID
		open[k] = n
	}
	s.open = open
	s.newSnapshot(o)
	s.unpark(o)
}

// editorSettings extracts the gohard settings from initialization
// options or didChangeConfiguration settings: the value of a "gohard"
// key when there is one, else the whole object.
func editorSettings(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var wrap struct {
		Gohard json.RawMessage `json:"gohard"`
	}
	if json.Unmarshal(raw, &wrap) == nil && len(wrap.Gohard) > 0 {
		return editorSettings(wrap.Gohard)
	}
	return raw, true
}

// setEditor parses the editor settings with the gohard.yaml loader
// (JSON is YAML), logging its problems, and merges them.
func (s *State) setEditor(o *out, raw json.RawMessage) {
	c, ds := config.Parse("editor settings", raw)
	for _, d := range ds {
		if d.Code == diag.ConfigRedundantDefault {
			continue // an editor may well send every default
		}
		s.log(o, LogWarning, "editor settings: "+d.Message+" ("+
			string(d.Code)+")")
	}
	s.editor = &c
	if s.file != nil {
		s.merge(o)
		if s.loaded {
			s.newSnapshot(o)
		}
	}
}

// merge combines gohard.yaml with the editor settings; gohard.yaml
// wins and every overridden key is reported in a window/logMessage
// (#28).
func (s *State) merge(o *out) {
	if s.file == nil {
		return
	}
	s.cfg = s.file.Config
	if s.editor == nil {
		return
	}
	merged, over := config.MergeEditor(s.file.Config, *s.editor)
	s.cfg = merged
	for _, k := range over {
		s.log(o, LogInfo, config.OverrideMessage(k))
	}
}

func (s *State) didChangeConfiguration(o *out, params json.RawMessage) {
	var p struct {
		Settings json.RawMessage `json:"settings"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	if ed, ok := editorSettings(p.Settings); ok {
		s.setEditor(o, ed)
	}
}

// publishConfig publishes the gohard.yaml diagnostics; diagnostics
// that are not about a position in gohard.yaml are logged.
func (s *State) publishConfig(o *out, l config.Loaded) {
	uri := PathToURI(filepath.Join(l.Root, config.FileName))
	x := span.NewIndex(l.Source)
	var ds []lspDiagnostic
	for _, d := range l.Diagnostics {
		if d.Path != config.FileName {
			s.log(o, LogError, d.Message+" ("+string(d.Code)+")")
			continue
		}
		ds = append(ds, s.toLSPDiagnostic(x, d))
	}
	if len(ds) == 0 && !s.cfgDiags {
		return
	}
	s.cfgDiags = len(ds) > 0
	s.publish(o, uri, nil, ds)
}

// capabilities are the server capabilities: incremental text sync,
// the negotiated position encoding, and the analyzer's features.
func (s *State) capabilities() map[string]any {
	f := s.an.Features()
	c := map[string]any{
		"positionEncoding": s.enc.String(),
		"textDocumentSync": map[string]any{
			"openClose": true,
			"change":    2, // incremental
			"save":      map[string]any{"includeText": false},
		},
		"workspace": map[string]any{
			"workspaceFolders": map[string]any{"supported": true},
		},
	}
	set := func(feat Feature, key string, v any) {
		if f.Has(feat) {
			c[key] = v
		}
	}
	set(FeatureHover, "hoverProvider", true)
	set(FeatureDefinition, "definitionProvider", true)
	set(FeatureReferences, "referencesProvider", true)
	set(FeatureRename, "renameProvider",
		map[string]any{"prepareProvider": true})
	triggers := slices.Clone(f.CompletionTriggers)
	if triggers == nil {
		triggers = []string{}
	}
	set(FeatureCompletion, "completionProvider",
		map[string]any{"triggerCharacters": triggers})
	set(FeatureDocumentSymbols, "documentSymbolProvider", true)
	set(FeatureWorkspaceSymbols, "workspaceSymbolProvider", true)
	set(FeatureFormatting, "documentFormattingProvider", true)
	set(FeatureRangeFormatting, "documentRangeFormattingProvider", true)
	set(FeatureFolding, "foldingRangeProvider", true)
	set(FeatureHighlights, "documentHighlightProvider", true)
	set(FeatureCodeLens, "codeLensProvider",
		map[string]any{"resolveProvider": false})
	cmds := slices.Clone(f.Commands)
	if cmds == nil {
		cmds = []string{}
	}
	set(FeatureCommands, "executeCommandProvider",
		map[string]any{"commands": cmds})
	return c
}

func (s *State) toLSPDiagnostic(x *span.Index, d diag.Diagnostic,
) lspDiagnostic {
	r := Range{}
	if d.Line > 0 {
		start, _ := x.OffsetOf(d.Line, d.Col)
		end := start
		if d.EndLine > 0 {
			end, _ = x.OffsetOf(d.EndLine, d.EndCol)
		}
		r = toRange(x, Span{start, end}, s.enc)
	}
	sev := 1
	switch d.Severity {
	case diag.Warning:
		sev = 2
	case diag.Hint:
		sev = 4
	}
	return lspDiagnostic{Range: r, Severity: sev, Code: string(d.Code),
		Source: "gohard", Message: d.Message}
}

type lspDiagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}
