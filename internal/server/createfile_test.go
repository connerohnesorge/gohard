package server_test

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/server"
)

// creating offers one code action, which creates a file, when the
// client applies CreateFile resource operations.
type creating struct{ server.Nop }

func (creating) Features() server.Features {
	return server.Features{Set: server.FeatureCodeActions}
}

func (creating) CodeActions(_ server.Query, _ server.Span, create bool,
) server.Task[[]server.CodeAction] {
	if !create {
		return server.Done[[]server.CodeAction](nil)
	}
	return server.Done([]server.CodeAction{{Title: "Create new.vhd",
		Kind: server.ActionSource, Edits: []server.FileEdit{{
			URI: "file:///work/new.vhd", Create: true,
			Edits: []server.Edit{{NewText: "entity n is end;\n"}},
		}},
	}})
}

// TestCodeActionCreatesFile: the analyzer learns from initialize
// whether the client announces the create resource operation, and an
// action that creates a file is sent as documentChanges, the CreateFile
// operation before the file's text (#57).
func TestCodeActionCreatesFile(t *testing.T) {
	text := []byte("entity top is end;\n")
	a, ok := server.LookupAction("codeaction")
	if !ok {
		t.Fatal("no codeaction action")
	}
	for _, c := range []struct {
		client server.ActionClient
		want   string
	}{
		{server.ActionClient{}, `[{"title":"Create new.vhd",` +
			`"kind":"source","edit":{"documentChanges":[` +
			`{"kind":"create","uri":"file:///work/new.vhd"},` +
			`{"textDocument":{"uri":"file:///work/new.vhd",` +
			`"version":null},"edits":[{"range":{"start":{"line":0,` +
			`"character":0},"end":{"line":0,"character":0}},` +
			`"newText":"entity n is end;\n"}]}]}}]`},
		{server.ActionClient{NoResourceOperations: true}, `[]`},
	} {
		sim := simenv.New(simenv.Options{Dir: workDir,
			Files: map[string][]byte{"/work/top.vhd": text}})
		resp, err := server.RunAction(sim.Env(), server.Options{
			Analyzer: creating{}}, 1, workDir, a, "/work/top.vhd", text,
			server.Position{}, nil, c.client)
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Result) != c.want {
			t.Errorf("%+v: got\n%s\nwant\n%s", c.client, resp.Result,
				c.want)
		}
	}
}
