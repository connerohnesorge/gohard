package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// zedCapabilities are the client capabilities Zed sends in initialize
// (default_initialize_params in crates/lsp/src/lsp.rs, zed-industries/zed
// main at 37faef8, October 2026, with pull diagnostics on, Zed's
// default). Zed offers only utf-16 positions.
const zedCapabilities = `{
 "general": {"positionEncodings": ["utf-16"]},
 "workspace": {
  "configuration": true,
  "didChangeWatchedFiles": {"dynamicRegistration": true,
   "relativePatternSupport": true},
  "didChangeConfiguration": {"dynamicRegistration": true},
  "workspaceFolders": true,
  "symbol": {"dynamicRegistration": true},
  "inlayHint": {"refreshSupport": true},
  "diagnostics": {"refreshSupport": true},
  "codeLens": {"refreshSupport": true},
  "workspaceEdit": {"resourceOperations": ["create", "rename", "delete"],
   "documentChanges": true, "snippetEditSupport": true},
  "fileOperations": {"dynamicRegistration": true, "didRename": true,
   "willRename": true},
  "applyEdit": true,
  "executeCommand": {"dynamicRegistration": true},
  "semanticTokens": {"refreshSupport": true}
 },
 "textDocument": {
  "definition": {"linkSupport": true, "dynamicRegistration": true},
  "documentHighlight": {"dynamicRegistration": true},
  "codeAction": {"codeActionLiteralSupport": {"codeActionKind": {
    "valueSet": ["refactor", "quickfix", "source"]}},
   "dataSupport": true,
   "resolveSupport": {"properties": ["kind", "diagnostics",
    "isPreferred", "disabled", "edit", "command"]},
   "dynamicRegistration": true},
  "completion": {"completionItem": {"snippetSupport": true,
    "resolveSupport": {"properties": ["additionalTextEdits", "command",
     "detail", "documentation"]},
    "deprecatedSupport": true, "tagSupport": {"valueSet": [1]},
    "insertReplaceSupport": true, "labelDetailsSupport": true,
    "insertTextModeSupport": {"valueSet": [1, 2]},
    "documentationFormat": ["markdown", "plaintext"]},
   "insertTextMode": 2,
   "completionList": {"itemDefaults": ["commitCharacters", "editRange",
    "insertTextMode", "insertTextFormat", "data"]},
   "contextSupport": true, "dynamicRegistration": true},
  "rename": {"prepareSupport": true, "prepareSupportDefaultBehavior": 1,
   "dynamicRegistration": true},
  "hover": {"contentFormat": ["markdown"], "dynamicRegistration": true},
  "inlayHint": {"resolveSupport": {"properties": ["textEdits", "tooltip",
    "label.tooltip", "label.location", "label.command"]},
   "dynamicRegistration": true},
  "semanticTokens": {"dynamicRegistration": true,
   "requests": {"full": {"delta": true}},
   "tokenTypes": ["namespace", "class", "enum", "interface", "struct",
    "typeParameter", "type", "parameter", "variable", "property",
    "enumMember", "decorator", "function", "method", "macro", "label",
    "comment", "string", "keyword", "number", "regexp", "operator",
    "modifier", "event", "lifetime"],
   "tokenModifiers": ["declaration", "definition", "readonly", "static",
    "deprecated", "abstract", "async", "modification", "documentation",
    "defaultLibrary", "constant"],
   "formats": ["relative"], "overlappingTokenSupport": true,
   "multilineTokenSupport": true, "serverCancelSupport": true,
   "augmentsSyntaxTokens": true},
  "publishDiagnostics": {"relatedInformation": true,
   "versionSupport": true, "dataSupport": true,
   "tagSupport": {"valueSet": [1, 2]}, "codeDescriptionSupport": true},
  "formatting": {"dynamicRegistration": true},
  "rangeFormatting": {"dynamicRegistration": true},
  "onTypeFormatting": {"dynamicRegistration": true},
  "signatureHelp": {"signatureInformation": {
    "documentationFormat": ["markdown", "plaintext"],
    "parameterInformation": {"labelOffsetSupport": true},
    "activeParameterSupport": true},
   "dynamicRegistration": true},
  "synchronization": {"didSave": true, "dynamicRegistration": true},
  "codeLens": {"dynamicRegistration": true},
  "callHierarchy": {"dynamicRegistration": true},
  "documentSymbol": {"hierarchicalDocumentSymbolSupport": true,
   "dynamicRegistration": true},
  "diagnostic": {"dynamicRegistration": true,
   "relatedDocumentSupport": true, "markupMessageSupport": true},
  "colorProvider": {"dynamicRegistration": true},
  "documentLink": {"dynamicRegistration": true, "tooltipSupport": true},
  "foldingRange": {"dynamicRegistration": true, "lineFoldingOnly": false,
   "foldingRange": {"collapsedText": true},
   "foldingRangeKind": {"valueSet": ["comment", "region", "imports"]}},
  "filters": {"relativePatternSupport": true}
 },
 "experimental": {"serverStatusNotification": true, "localDocs": true},
 "window": {"workDoneProgress": true,
  "showMessage": {"messageActionItem": {
   "additionalPropertiesSupport": true}},
  "showDocument": {"support": true}}
}`

// zedExtension returns the server ID and arguments of the Zed
// extension (editors/zed/src/lib.rs) and the language ID it sends for
// VHDL (editors/zed/extension.toml). It fails the test if the
// extension starts sending initialization options of its own, which
// the test would have to send too.
func zedExtension(t *testing.T) (server string, args []string,
	languageID string,
) {
	t.Helper()
	ext := filepath.Join("..", "..", "editors", "zed")
	src, err := os.ReadFile(filepath.Join(ext, "src", "lib.rs"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^const SERVER: &str = ("[^"]+");$`).
		FindSubmatch(src)
	if m == nil {
		t.Fatal("lib.rs has no `const SERVER: &str = \"...\";`")
	}
	if server, err = strconv.Unquote(string(m[1])); err != nil {
		t.Fatal(err)
	}
	m = regexp.MustCompile(`(?m)^const ARGS: &\[&str\] = &\[([^\]]*)\];$`).
		FindSubmatch(src)
	if m == nil {
		t.Fatal("lib.rs has no `const ARGS: &[&str] = &[...];`")
	}
	for a := range strings.SplitSeq(string(m[1]), ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		u, err := strconv.Unquote(a)
		if err != nil {
			t.Fatalf("argument %s: %v", a, err)
		}
		args = append(args, u)
	}
	if strings.Contains(string(src),
		"fn language_server_initialization_options") {
		t.Error("the extension sends initialization options; send " +
			"them in the test too")
	}
	manifest, err := os.ReadFile(filepath.Join(ext, "extension.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest),
		"\n[language_servers."+server+"]\n") {
		t.Errorf("extension.toml has no [language_servers.%s]", server)
	}
	m = regexp.MustCompile(`(?m)^language_ids = \{ "VHDL" = ("[^"]+") \}$`).
		FindSubmatch(manifest)
	if m == nil {
		t.Fatal("extension.toml maps no language ID for VHDL")
	}
	if languageID, err = strconv.Unquote(string(m[1])); err != nil {
		t.Fatal(err)
	}
	return server, args, languageID
}

// TestZedStdio is the Zed connection test of #65, in the shape of
// TestHelixStdio: the built gohard binary runs with the arguments of
// the Zed extension (editors/zed) over real OS pipes, and a scripted
// client runs Zed's handshake. Zed sends the worktree as root and
// workspace folder, Zed's client capabilities (utf-16 positions only),
// no initialization options (the extension has none and the user set
// none), then after initialized a workspace/didChangeConfiguration
// with the extension's workspace configuration: {} when the user set
// no lsp.gohard.settings. The diagnostics of the opened file must equal
// `gohard check` on the same project. Zed itself is not run.
func TestZedStdio(t *testing.T) {
	server, args, languageID := zedExtension(t)
	if server != "gohard" {
		t.Fatalf("the Zed extension runs %q, want gohard", server)
	}
	bin := buildGohard(t)
	var caps any
	if err := json.Unmarshal([]byte(zedCapabilities), &caps); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "lsp",
		"diagnostics", "cross-file", "in"), dir)
	root := fileURI(dir)
	runStdio(t, bin, stdioSession{
		args: args, dir: dir,
		initialize: map[string]any{
			"processId": os.Getpid(), "rootPath": dir, "rootUri": root,
			"capabilities": caps,
			"workspaceFolders": []any{map[string]any{"uri": root,
				"name": filepath.Base(dir)}},
			"clientInfo": map[string]any{"name": "Zed",
				"version": "1.25.3"}},
		settings:   json.RawMessage(`{}`),
		open:       filepath.Join(dir, "top.vhd"),
		languageID: languageID, encoding: "utf-16",
	})
}
