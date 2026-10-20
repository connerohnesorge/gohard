package ide

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/server"
)

// session runs a whole server session with this package's analyzer
// over the project p in a client offering encodings (nil: utf-16 only)
// and returns the result of request id 2.
func session(t *testing.T, p project, encs []string, method string,
	params map[string]any,
) json.RawMessage {
	t.Helper()
	files := map[string][]byte{}
	for k, v := range p {
		files["/work/"+k] = v
	}
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	var got json.RawMessage
	d := server.NewDriver(server.Options{Analyzer: New(Options{})},
		sim.Env(), 1, func(s server.Send) error {
			if r, ok := s.Msg.(*jsonrpc2.Response); ok {
				if n, ok := r.ID.Int(); ok && n == 2 {
					got = r.Result
				}
			}
			return nil
		})
	caps := map[string]any{}
	if encs != nil {
		caps["general"] = map[string]any{"positionEncodings": encs}
	}
	for _, m := range []map[string]any{
		{"id": 1, "method": "initialize", "params": map[string]any{
			"processId": nil, "rootUri": "file:///work",
			"capabilities": caps}},
		{"method": "initialized", "params": map[string]any{}},
		{"id": 2, "method": method, "params": params},
	} {
		m["jsonrpc"] = "2.0"
		b, _ := json.Marshal(m)
		if err := d.Deliver(b); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

// TestPositionEncodings checks that completion positions are UTF-8
// when the client offers it and UTF-16 otherwise (#25): a comment with
// a character outside the BMP precedes the cursor on its line.
func TestPositionEncodings(t *testing.T) {
	top := strings.Replace(featTop, "    n <= p.first;",
		"    /* \U0001F600 */ n <= p.first;", 1)
	p := project{"defs.vhd": []byte(featPkg), "top.vhd": []byte(top)}
	line := strings.Count(top[:strings.Index(top, "p.first")], "\n")
	lineStart := strings.LastIndex(top[:strings.Index(top, "p.first")],
		"\n") + 1
	col8 := strings.Index(top, "p.first") + 2 - lineStart
	for _, c := range []struct {
		encs []string
		col  int
	}{{[]string{"utf-8"}, col8}, {nil, col8 - 2}} {
		raw := session(t, p, c.encs, "textDocument/completion",
			map[string]any{"textDocument": map[string]any{
				"uri": uri("top.vhd")}, "position": map[string]any{
				"line": line, "character": c.col}})
		var res struct {
			Items []struct {
				Label    string `json:"label"`
				TextEdit struct {
					Range server.Range `json:"range"`
				} `json:"textEdit"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &res); err != nil ||
			len(res.Items) != 2 || res.Items[0].Label != "first" {
			t.Fatalf("%v: %s", c.encs, raw)
		}
		if r := res.Items[0].TextEdit.Range; r.Start.Character != c.col ||
			r.Start.Line != line {
			t.Fatalf("%v: range %+v, want column %d", c.encs, r, c.col)
		}
	}
}
