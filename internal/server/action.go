package server

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// Action is one `gohard lsp <action> --at PATH:LINE:COL` (#26). It
// names the LSP request the action sends; RunAction sends it through a
// whole in-memory session, so the action and the editor run the same
// code.
type Action struct {
	Name    string
	Method  string
	Summary string
	// Args is the synopsis of extra positional arguments, if any.
	Args string
	// MethodOf, when set, picks the method from the extra arguments
	// (semantic-tokens sends a range request for `range L:C-L:C`).
	MethodOf func(args []string) string
	// Params builds the request params for the document uri and the
	// zero-based UTF-8 position pos, with the extra arguments.
	Params func(uri string, pos Position, args []string) (any, error)
}

var actions []Action

// RegisterAction adds an action (later stages register theirs, for
// example `gohard lsp sim`, #35). Registering a name twice panics.
func RegisterAction(a Action) {
	if _, ok := LookupAction(a.Name); ok {
		panic("server: action " + a.Name + " registered twice")
	}
	i, _ := slices.BinarySearchFunc(actions, a.Name,
		func(x Action, n string) int { return strings.Compare(x.Name, n) })
	actions = slices.Insert(actions, i, a)
}

// LookupAction returns the action called name.
func LookupAction(name string) (Action, bool) {
	for _, a := range actions {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}

// Actions returns every action sorted by name.
func Actions() []Action { return slices.Clone(actions) }

type textPos struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
}

func atParams(uri string, pos Position) textPos {
	var p textPos
	p.TextDocument.URI, p.Position = uri, pos
	return p
}

func noArgs(name string, args []string) error {
	if len(args) > 0 {
		return errors.New("gohard lsp " + name + " takes no arguments")
	}
	return nil
}

func positional(name, method, summary string) Action {
	return Action{Name: name, Method: method, Summary: summary,
		Params: func(uri string, pos Position, args []string) (any,
			error,
		) {
			return atParams(uri, pos), noArgs(name, args)
		}}
}

func document(name, method, summary string) Action {
	return Action{Name: name, Method: method, Summary: summary,
		Params: func(uri string, _ Position, args []string) (any,
			error,
		) {
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Options any `json:"options,omitempty"`
			}
			p.TextDocument.URI = uri
			if method == "textDocument/formatting" {
				p.Options = map[string]any{"tabSize": 2,
					"insertSpaces": true}
			}
			return p, noArgs(name, args)
		}}
}

func init() {
	for _, a := range []Action{
		positional("hover", "textDocument/hover", "hover text"),
		positional("definition", "textDocument/definition",
			"go to definition"),
		positional("highlight", "textDocument/documentHighlight",
			"in-file highlights"),
		positional("completion", "textDocument/completion",
			"completion items"),
		document("symbols", "textDocument/documentSymbol",
			"document symbols"),
		document("format", "textDocument/formatting",
			"whole-file formatting edits"),
		document("folding", "textDocument/foldingRange",
			"folding ranges"),
		document("codelens", "textDocument/codeLens", "code lenses"),
		{
			Name: "workspace-symbols", Method: "workspace/symbol",
			Summary: "workspace symbols matching QUERY (all without)",
			Args:    "[QUERY]",
			Params: func(_ string, _ Position, args []string) (any,
				error,
			) {
				if len(args) > 1 {
					return nil, errors.New("gohard lsp " +
						"workspace-symbols takes at most one argument")
				}
				q := ""
				if len(args) == 1 {
					q = args[0]
				}
				return struct {
					Query string `json:"query"`
				}{q}, nil
			},
		},
		{
			Name: "codeaction", Method: "textDocument/codeAction",
			Summary: "quick fixes for the diagnostics at the position",
			Params: func(uri string, pos Position, args []string) (any,
				error,
			) {
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
					Range   Range `json:"range"`
					Context struct {
						Diagnostics []any `json:"diagnostics"`
					} `json:"context"`
				}
				p.TextDocument.URI = uri
				p.Range = Range{Start: pos, End: pos}
				p.Context.Diagnostics = []any{}
				return p, noArgs("codeaction", args)
			},
		},
		{
			Name: "diagnostics", Method: publishMethod,
			Summary: "the diagnostics published for the file",
			Params: func(uri string, _ Position, args []string) (any,
				error,
			) {
				return nil, noArgs("diagnostics", args)
			},
		},
		{
			Name: "references", Method: "textDocument/references",
			Summary: "references, including the declaration",
			Params: func(uri string, pos Position, args []string) (any,
				error,
			) {
				return struct {
					textPos
					Context struct {
						IncludeDeclaration bool `json:"includeDeclaration"`
					} `json:"context"`
				}{textPos: atParams(uri, pos), Context: struct {
					IncludeDeclaration bool `json:"includeDeclaration"`
				}{true}}, noArgs("references", args)
			},
		},
		{
			Name: "rename", Method: "textDocument/rename",
			Summary: "rename; with --dry-run print the edit plan",
			Args:    "NEW_NAME",
			Params: func(uri string, pos Position, args []string) (any,
				error,
			) {
				if len(args) != 1 {
					return nil, errors.New("gohard lsp rename needs " +
						"one argument, the new name")
				}
				return struct {
					textPos
					NewName string `json:"newName"`
				}{atParams(uri, pos), args[0]}, nil
			},
		},
	} {
		RegisterAction(a)
	}
}

// ErrNotProvided is returned by RunAction when the analyzer does not
// provide the action's feature.
var ErrNotProvided = errors.New("the analyzer does not provide it")

// publishMethod is the notification the diagnostics action reports.
const publishMethod = "textDocument/publishDiagnostics"

// RunAction runs a at the zero-based UTF-8 position pos of the file
// path (an OS path) on the server's own code path: an in-memory
// session in folder that negotiates utf-8, opens the file with text,
// sends the request, shuts down and exits. It returns the response.
// The diagnostics action sends no request: its result is the last
// textDocument/publishDiagnostics for the file once the server is idle
// after the open (the quiescent diagnostics, equal to `gohard check`).
func RunAction(e env.Env, o Options, jobs int, folder string, a Action,
	path string, text []byte, pos Position, args []string,
) (*jsonrpc2.Response, error) {
	uri := PathToURI(path)
	params, err := a.Params(uri, pos, args)
	if err != nil {
		return nil, err
	}
	method := a.Method
	if a.MethodOf != nil {
		method = a.MethodOf(args)
	}
	var got *jsonrpc2.Response
	var published json.RawMessage
	d := NewDriver(o, e, jobs, func(s Send) error {
		if r, ok := s.Msg.(*jsonrpc2.Response); ok {
			if n, ok := r.ID.Int(); ok && n == 2 {
				got = r
			}
		}
		if n, ok := s.Msg.(*jsonrpc2.Notification); ok &&
			n.Method == publishMethod {
			var p struct {
				URI string `json:"uri"`
			}
			if json.Unmarshal(n.Params, &p) == nil && p.URI == uri {
				published = n.Params
			}
		}
		return nil
	})
	init := map[string]any{
		"processId": nil,
		"rootUri":   PathToURI(folder),
		"capabilities": map[string]any{"general": map[string]any{
			"positionEncodings": []string{"utf-8"}}},
	}
	open := map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "vhdl", "version": 1,
		"text": string(text)}}
	msgs := []jsonrpc2.Message{
		&jsonrpc2.Request{ID: jsonrpc2.IntID(1), Method: "initialize",
			Params: marshal(init)},
		&jsonrpc2.Notification{Method: "initialized",
			Params: marshal(struct{}{})},
		&jsonrpc2.Notification{Method: "textDocument/didOpen",
			Params: marshal(open)},
		&jsonrpc2.Request{ID: jsonrpc2.IntID(2), Method: method,
			Params: marshal(params)},
		&jsonrpc2.Request{ID: jsonrpc2.IntID(3), Method: "shutdown"},
		&jsonrpc2.Notification{Method: "exit"},
	}
	diags := method == publishMethod
	if diags {
		if o.Analyzer == nil ||
			!o.Analyzer.Features().Has(FeatureDiagnostics) {
			return nil, ErrNotProvided
		}
		msgs = slices.Delete(msgs, 3, 4)
	}
	for i, m := range msgs {
		if err := d.Deliver(Send{m}.Body()); err != nil {
			return nil, err
		}
		if diags && i == 2 {
			got = diagnosticsResponse(published, uri)
		}
	}
	if got == nil {
		return nil, errors.New("no response to " + method)
	}
	if got.Error != nil && got.Error.Code == jsonrpc2.CodeMethodNotFound {
		return got, ErrNotProvided
	}
	return got, nil
}

// diagnosticsResponse is the result of the diagnostics action: the
// publishDiagnostics params, or an empty list when none was published.
func diagnosticsResponse(params json.RawMessage, uri string,
) *jsonrpc2.Response {
	if params == nil {
		params = marshal(struct {
			URI         string `json:"uri"`
			Diagnostics []any  `json:"diagnostics"`
		}{uri, []any{}})
	}
	return &jsonrpc2.Response{ID: jsonrpc2.IntID(2), Result: params}
}
