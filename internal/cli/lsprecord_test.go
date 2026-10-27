package cli

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/server"
)

// TestRecordScansEachBodyOnce: while the session folder is not known
// (an initialize with no folder, then only an untitled document), each
// arrival costs the same however many lines wait before the header: the
// recording scans the new body only, not every buffered one (#48).
func TestRecordScansEachBodyOnce(t *testing.T) {
	r := &recording{}
	add := func(body string) {
		if err := r.record(server.Arrival{Body: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}
	add(`{"jsonrpc":"2.0","id":1,"method":"initialize",` +
		`"params":{"capabilities":{}}}`)
	add(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	add(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":` +
		`{"textDocument":{"uri":"untitled:Untitled-1",` +
		`"languageId":"vhdl","version":1,"text":""}}}`)
	change := `{"jsonrpc":"2.0","method":"textDocument/didChange",` +
		`"params":{"textDocument":{"uri":"untitled:Untitled-1",` +
		`"version":2},"contentChanges":[{"range":{"start":` +
		`{"line":0,"character":0},"end":{"line":0,"character":0}},` +
		`"text":"x"}]}}`
	for range 5000 {
		add(change)
	}
	allocs := testing.AllocsPerRun(100, func() { add(change) })
	if allocs > 100 {
		t.Errorf("one arrival after 5000 waiting lines: %v allocations",
			allocs)
	}
	if r.r != nil {
		t.Error("header written before the session folder is known")
	}
}
