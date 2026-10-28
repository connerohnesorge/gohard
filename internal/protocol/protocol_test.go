package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/tools/genprotocol/gen"
)

// TestGeneratedUpToDate: protocol_gen.go is exactly what the generator
// makes from the vendored metaModel.json (the gen-check gate, #30).
func TestGeneratedUpToDate(t *testing.T) {
	model, err := os.ReadFile("../../tools/genprotocol/metaModel.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := gen.Generate(model)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("protocol_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("protocol_gen.go is stale; run go generate " +
			"./internal/protocol")
	}
}

// samples are messages as clients and servers send them. Each decodes
// into its type and encodes back to the same JSON value.
var samples = []struct {
	name string
	new  func() any
	json string
}{
	{"initialize", func() any { return new(InitializeParams) }, `{
		"processId": null, "rootUri": "file:///w",
		"clientInfo": {"name": "Neovim", "version": "0.11.0"},
		"capabilities": {
			"general": {"positionEncodings": ["utf-8", "utf-16"]},
			"window": {"workDoneProgress": true},
			"textDocument": {
				"synchronization": {"didSave": true,
					"dynamicRegistration": false},
				"hover": {"contentFormat": ["markdown", "plaintext"]},
				"completion": {"completionItem": {
					"snippetSupport": false,
					"resolveSupport": {"properties": ["documentation"]},
					"tagSupport": {"valueSet": [1]}}},
				"rename": {"prepareSupport": true},
				"publishDiagnostics": {"relatedInformation": true}
			},
			"workspace": {"configuration": true,
				"workspaceFolders": true, "applyEdit": true}
		},
		"trace": "off",
		"workspaceFolders": [{"uri": "file:///w", "name": "w"}],
		"workDoneToken": 7
	}`},
	{"initialize result", func() any { return new(InitializeResult) }, `{
		"capabilities": {
			"positionEncoding": "utf-8",
			"textDocumentSync": {"openClose": true, "change": 2,
				"save": {"includeText": false}},
			"completionProvider": {"triggerCharacters": ["."]},
			"hoverProvider": true,
			"definitionProvider": true,
			"referencesProvider": {"workDoneProgress": true},
			"renameProvider": {"prepareProvider": true},
			"documentFormattingProvider": true,
			"foldingRangeProvider": true,
			"codeLensProvider": {"resolveProvider": false},
			"executeCommandProvider": {"commands": ["gohard.run"]},
			"workspace": {"workspaceFolders": {"supported": true,
				"changeNotifications": "id"}}
		},
		"serverInfo": {"name": "gohard", "version": "1.0.0"}
	}`},
	{"didChange", func() any { return new(DidChangeTextDocumentParams) },
		`{"textDocument": {"uri": "file:///a.vhd", "version": 3},
		"contentChanges": [
			{"range": {"start": {"line": 0, "character": 1},
				"end": {"line": 0, "character": 2}},
				"rangeLength": 1, "text": "x"},
			{"text": "whole"}
		]}`},
	{"publishDiagnostics", func() any {
		return new(PublishDiagnosticsParams)
	}, `{"uri": "file:///a.vhd", "version": 0, "diagnostics": [
		{"range": {"start": {"line": 1, "character": 0},
			"end": {"line": 1, "character": 4}},
		"severity": 1, "code": "syntax/error", "source": "gohard",
		"message": "expected ;", "tags": [1],
		"relatedInformation": [{"location": {"uri": "file:///b.vhd",
			"range": {"start": {"line": 0, "character": 0},
			"end": {"line": 0, "character": 0}}}, "message": "here"}],
		"data": {"snapshot": 4, "x": [1, "two", null]}},
		{"range": {"start": {"line": 2, "character": 0},
			"end": {"line": 2, "character": 0}}, "code": 12,
		"message": "m"}
	]}`},
	{"hover markup", func() any { return new(Hover) },
		`{"contents": {"kind": "markdown", "value": "**x**"}}`},
	{"hover marked", func() any { return new(Hover) },
		`{"contents": [{"language": "vhdl", "value": "signal s"}, "doc"],
		"range": {"start": {"line": 0, "character": 0},
		"end": {"line": 0, "character": 1}}}`},
	{"hover string", func() any { return new(Hover) },
		`{"contents": "plain"}`},
	{"workspace edit", func() any { return new(WorkspaceEdit) }, `{
		"changes": {"file:///b": [], "file:///a": [{"range": {
			"start": {"line": 0, "character": 0},
			"end": {"line": 0, "character": 0}}, "newText": "y"}]},
		"documentChanges": [
			{"kind": "create", "uri": "file:///n"},
			{"kind": "rename", "oldUri": "file:///n",
				"newUri": "file:///m"},
			{"kind": "delete", "uri": "file:///m",
				"options": {"recursive": true}},
			{"textDocument": {"uri": "file:///a", "version": null},
				"edits": []}
		]}`},
	{"registration", func() any { return new(RegistrationParams) },
		`{"registrations": [{"id": "1",
		"method": "workspace/didChangeWatchedFiles",
		"registerOptions": {"watchers": [{"globPattern": "**/*.vhd"},
			{"globPattern": {"baseUri": "file:///w", "pattern": "*.yaml"},
			"kind": 7}]}}]}`},
	{"completion item", func() any { return new(CompletionItem) },
		`{"label": "signal", "kind": 14, "insertTextFormat": 1,
		"textEdit": {"insert": {"start": {"line": 0, "character": 0},
			"end": {"line": 0, "character": 1}},
			"replace": {"start": {"line": 0, "character": 0},
			"end": {"line": 0, "character": 2}}, "newText": "signal"},
		"documentation": {"kind": "plaintext", "value": "d"}}`},
	{"progress", func() any { return new(ProgressParams) },
		`{"token": "sim-1", "value": {"kind": "begin", "title": "Run",
		"percentage": 0}}`},
	{"document symbol", func() any { return new(DocumentSymbol) },
		`{"name": "e", "kind": 2, "range": {"start": {"line": 0,
		"character": 0}, "end": {"line": 9, "character": 0}},
		"selectionRange": {"start": {"line": 0, "character": 7},
		"end": {"line": 0, "character": 8}}, "children": [{"name": "p",
		"kind": 12, "range": {"start": {"line": 1, "character": 0},
		"end": {"line": 2, "character": 0}}, "selectionRange": {"start":
		{"line": 1, "character": 0}, "end": {"line": 1, "character": 1}}}]}`},
}

// canon re-encodes JSON through any, which sorts object keys.
func canon(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestSamplesRoundTrip(t *testing.T) {
	for _, s := range samples {
		v := s.new()
		if err := json.Unmarshal([]byte(s.json), v); err != nil {
			t.Errorf("%s: decode: %v", s.name, err)
			continue
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Errorf("%s: encode: %v", s.name, err)
			continue
		}
		if got, want := canon(t, out), canon(t, []byte(s.json)); got != want {
			t.Errorf("%s: round trip\n got %s\nwant %s", s.name, got, want)
		}
		// Encoding is a fixed point and deterministic.
		w := s.new()
		if err := json.Unmarshal(out, w); err != nil {
			t.Fatalf("%s: re-decode: %v", s.name, err)
		}
		again, _ := json.Marshal(w)
		if !bytes.Equal(again, out) {
			t.Errorf("%s: not a fixed point:\n%s\n%s", s.name, out, again)
		}
	}
}

// TestOrAlternatives: or types decode into the right Go types.
func TestOrAlternatives(t *testing.T) {
	var p DidChangeTextDocumentParams
	mustDecode(t, samples[2].json, &p)
	changes := p.ContentChanges
	if _, ok := changes[0].Value.(TextDocumentContentChangePartial); !ok {
		t.Errorf("change 0 is %T", changes[0].Value)
	}
	if _, ok := changes[1].Value.(TextDocumentContentChangeWhole); !ok {
		t.Errorf("change 1 is %T", changes[1].Value)
	}
	var e WorkspaceEdit
	mustDecode(t, samples[7].json, &e)
	want := []string{"CreateFile", "RenameFile", "DeleteFile",
		"TextDocumentEdit"}
	for i, c := range e.DocumentChanges {
		if got := reflect.TypeOf(c.Value).Name(); got != want[i] {
			t.Errorf("documentChanges[%d] is %s, want %s", i, got, want[i])
		}
	}
	var d PublishDiagnosticsParams
	mustDecode(t, samples[3].json, &d)
	if d.Diagnostics[0].Code.Value != "syntax/error" ||
		d.Diagnostics[1].Code.Value != int32(12) ||
		d.Version == nil || *d.Version != 0 {
		t.Errorf("diagnostic codes %#v %#v, version %v",
			d.Diagnostics[0].Code.Value, d.Diagnostics[1].Code.Value,
			d.Version)
	}
	var defs DefinitionOrDefinitionLinks
	mustDecode(t, `[{"originSelectionRange": null, "targetUri": "u",
		"targetRange": {"start": {"line": 0, "character": 0},
		"end": {"line": 0, "character": 0}}, "targetSelectionRange":
		{"start": {"line": 0, "character": 0},
		"end": {"line": 0, "character": 0}}}]`, &defs)
	if _, ok := defs.Value.([]DefinitionLink); !ok {
		t.Errorf("definition links decoded as %T", defs.Value)
	}
	mustDecode(t, `{"uri": "u", "range": {"start": {"line": 0,
		"character": 0}, "end": {"line": 0, "character": 0}}}`, &defs)
	if _, ok := defs.Value.(Definition); !ok {
		t.Errorf("single location decoded as %T", defs.Value)
	}
	var sync TextDocumentSyncOptionsOrTextDocumentSyncKind
	mustDecode(t, `2`, &sync)
	if sync.Value != TextDocumentSyncKindIncremental {
		t.Errorf("sync kind %#v", sync.Value)
	}
	for _, bad := range []string{`"x"`, `true`, `[1]`} {
		if err := json.Unmarshal([]byte(bad), &sync); err == nil {
			t.Errorf("%s decoded as %#v", bad, sync.Value)
		}
	}
	mustDecode(t, `null`, &sync)
	if sync.Value != nil {
		t.Errorf("null decoded as %#v", sync.Value)
	}
}

func mustDecode(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
}

// TestEncodingRules pins the omit, pointer, null and order rules.
func TestEncodingRules(t *testing.T) {
	tests := []struct {
		v    any
		want string
	}{
		{ServerCapabilities{}, `{}`},
		{PublishDiagnosticsParams{URI: "file:///a"},
			`{"uri":"file:///a","diagnostics":[]}`},
		{PublishDiagnosticsParams{URI: "u", Version: Ptr[int32](0)},
			`{"uri":"u","version":0,"diagnostics":[]}`},
		{InitializeParams{},
			`{"processId":null,"rootUri":null,"capabilities":{}}`},
		{ServerCapabilities{HoverProvider: BoolOrHoverOptions{true},
			PositionEncoding: PositionEncodingKindUTF8},
			`{"positionEncoding":"utf-8","hoverProvider":true}`},
		{Diagnostic{Message: "m", Severity: DiagnosticSeverityHint,
			Code: IntegerOrString{"lsp/x"}},
			`{"range":{"start":{"line":0,"character":0},"end":{"line":0,` +
				`"character":0}},"severity":4,"code":"lsp/x","message":"m"}`},
		{Hover{Contents: MarkupContentOrMarkedStringOrMarkedStrings{
			MarkupContent{Kind: MarkupKindMarkdown, Value: "v"}}},
			`{"contents":{"kind":"markdown","value":"v"}}`},
		{TextDocumentSyncOptions{Change: Ptr(TextDocumentSyncKindNone)},
			`{"change":0}`},
		{WorkspaceEdit{Changes: map[DocumentURI][]TextEdit{
			"b": nil, "a": {}}},
			`{"changes":{"a":[],"b":null}}`},
	}
	for _, tt := range tests {
		got, err := json.Marshal(tt.v)
		if err != nil || string(got) != tt.want {
			t.Errorf("%T: got %s, %v\nwant %s", tt.v, got, err, tt.want)
		}
	}
}

// TestDecodeNeverPanics mutates the samples at seeded points and
// decodes them into their types: errors are fine, panics are not, and
// whatever decodes encodes again.
func TestDecodeNeverPanics(t *testing.T) {
	r := &env.SplitMix64{State: 1}
	for round := range 4000 {
		s := samples[round%len(samples)]
		b := []byte(s.json)
		for range 1 + env.Intn(r, 3) {
			i := env.Intn(r, len(b))
			switch env.Intn(r, 3) {
			case 0:
				b[i] = `{}[]",:0n-tx`[env.Intn(r, 12)]
			case 1:
				b = append(b[:i:i], b[min(len(b), i+1+env.Intn(r, 8)):]...)
			default:
				b = append(b[:i:i], append([]byte(`null,`), b[i:]...)...)
			}
		}
		v := s.new()
		if json.Unmarshal(b, v) == nil {
			if _, err := json.Marshal(v); err != nil {
				t.Fatalf("round %d: %s decoded but does not encode: %v",
					round, b, err)
			}
		}
	}
}

// TestMethodConstants spot-checks the method constants, including one
// whose string the generator split across lines.
func TestMethodConstants(t *testing.T) {
	if MethodTextDocumentDidChange != "textDocument/didChange" ||
		MethodCancelRequest != "$/cancelRequest" ||
		MethodWorkspaceDidChangeWorkspaceFolders !=
			"workspace/didChangeWorkspaceFolders" ||
		FailureHandlingKindTextOnlyTransactional !=
			"textOnlyTransactional" {
		t.Error("method constants are wrong")
	}
}
