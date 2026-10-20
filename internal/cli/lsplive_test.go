package cli_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/ide"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/span"
)

// TestLSPLivenessRealAnalyzer is the liveness workload of #25 and #30
// with the analyzer of `gohard lsp`: a session on a corpus project
// that types into two files and asks hover, definition and references
// for 60 s of virtual time. While work is pending the server never
// goes server.NoProgress of virtual time without sending something,
// every request gets exactly one response, and once the edits stop
// the published diagnostics equal `gohard check` on the final text.
func TestLSPLivenessRealAnalyzer(t *testing.T) {
	files := corpusProjects(t)["hier_generate"]
	if len(files) == 0 {
		t.Fatal("no hier_generate corpus design")
	}
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	an := server.WithFormatting(ide.New(ide.Options{
		ToolVersion: cli.Version, Jobs: sim.Env().Jobs, Limit: 2}),
		cli.Version)
	var d *server.Driver
	var last time.Duration // virtual time of the latest message sent
	answers := map[string]int{}
	pubs := map[string][]lspDiag{}
	recv := func(m server.Send) error {
		last = d.State.Now()
		switch m := m.Msg.(type) {
		case *jsonrpc2.Response:
			answers[m.ID.String()]++
		case *jsonrpc2.Notification:
			if m.Method == "textDocument/publishDiagnostics" {
				rel, ds := decodePublish(t, m.Params)
				pubs[rel] = ds
				if len(ds) == 0 {
					delete(pubs, rel)
				}
			}
		}
		return nil
	}
	d = server.NewDriver(server.Options{Analyzer: an,
		Version: cli.Version}, sim.Env(), 2, recv)
	deliver := func(body string) {
		if err := d.Deliver([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	deliver(`{"jsonrpc":"2.0","id":"init","method":"initialize",` +
		`"params":{"rootUri":"file:///work","capabilities":` +
		`{"general":{"positionEncodings":["utf-8"]}}}}`)
	deliver(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	var vhd []string
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if strings.HasSuffix(p, ".vhd") {
			vhd = append(vhd, strings.TrimPrefix(p, "/work/"))
		}
	}
	texts := map[string][]byte{}
	versions := map[string]int{}
	for _, rel := range vhd[:2] {
		texts[rel] = files["/work/"+rel]
		versions[rel] = 1
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0",
			"method": "textDocument/didOpen", "params": map[string]any{
				"textDocument": map[string]any{
					"uri": "file:///work/" + rel, "languageId": "vhdl",
					"version": 1, "text": string(texts[rel])}}})
		deliver(string(b))
	}

	rng := &env.SplitMix64{State: 60}
	pick := func(n int) int { return int(rng.Uint64() % uint64(n)) }
	start := d.State.Now()
	end := start + 60*time.Second
	id := 0
	var lastAt time.Duration
	methods := []string{"textDocument/hover", "textDocument/definition",
		"textDocument/references"}
	for at := start; at < end; at += time.Duration(5+pick(80)) *
		time.Millisecond {
		rel := vhd[pick(2)]
		text := texts[rel]
		x := span.NewIndex(text)
		var msg []byte
		if pick(3) > 0 {
			// Type one snippet at a line start: the text stays mostly
			// valid VHDL, so the analysis does real work.
			off := 0
			if n := strings.Count(string(text), "\n"); n > 0 {
				line := pick(n)
				off, _ = x.Offset(span.Position{Line: line}, span.UTF8)
			}
			s := snippets[pick(len(snippets))]
			texts[rel] = slices.Concat(text[:off], []byte(s), text[off:])
			versions[rel]++
			p := x.Position(off, span.UTF8)
			pos := server.Position{Line: p.Line, Character: p.Col}
			msg, _ = json.Marshal(map[string]any{"jsonrpc": "2.0",
				"method": "textDocument/didChange",
				"params": map[string]any{
					"textDocument": map[string]any{
						"uri":     "file:///work/" + rel,
						"version": versions[rel]},
					"contentChanges": []any{map[string]any{
						"range": server.Range{Start: pos, End: pos},
						"text":  s}}}})
		} else {
			id++
			p := x.Position(pick(len(text)+1), span.UTF8)
			params := map[string]any{
				"textDocument": map[string]any{
					"uri": "file:///work/" + rel},
				"position": server.Position{Line: p.Line,
					Character: p.Col}}
			m := methods[pick(len(methods))]
			if m == "textDocument/references" {
				params["context"] = map[string]any{
					"includeDeclaration": true}
			}
			msg, _ = json.Marshal(map[string]any{"jsonrpc": "2.0",
				"id": id, "method": m, "params": params})
		}
		d.State = d.State.PushAt(at, server.Message{Data: msg},
			server.PriorityClient)
		lastAt = at
	}
	last = d.State.Now()
	steps := 0
	for !d.Idle() {
		if err := d.Step(); err != nil {
			t.Fatal(err)
		}
		steps++
		if !d.State.Busy() {
			last = d.State.Now()
			continue
		}
		if d.State.Now()-last > server.NoProgress {
			t.Fatalf("no progress from %s to %s", last, d.State.Now())
		}
	}
	if lastAt < end-time.Second || d.State.Now() < lastAt {
		t.Fatalf("the workload ended at %s, before %s", d.State.Now(),
			lastAt)
	}
	for i := 1; i <= id; i++ {
		if n := answers[fmt.Sprint(i)]; n != 1 {
			t.Fatalf("request %d: %d responses", i, n)
		}
	}
	// Quiescence after the workload: the edited text on disk, checked.
	for _, rel := range slices.Sorted(maps.Keys(texts)) {
		if err := sim.FS.WriteFile("/work/"+rel, texts[rel],
			0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := checkDiags(t, sim)
	for _, rel := range vhd[:2] {
		if !slices.Equal(want[rel], pubs[rel]) {
			t.Fatalf("%s: check %v\nserver %v", rel, want[rel], pubs[rel])
		}
	}
	t.Logf("%d requests, %d steps, ended at %s", id, steps,
		d.State.Now())
}

// decodePublish returns the path under /work and the diagnostics of a
// textDocument/publishDiagnostics notification.
func decodePublish(t *testing.T, params json.RawMessage) (string,
	[]lspDiag,
) {
	t.Helper()
	var p struct {
		URI         string
		Diagnostics []struct {
			Range         server.Range
			Severity      int
			Code, Message string
		}
	}
	if err := json.Unmarshal(params, &p); err != nil {
		t.Fatal(err)
	}
	var ds []lspDiag
	for _, d := range p.Diagnostics {
		r := d.Range
		ds = append(ds, lspDiag{r.Start.Line, r.Start.Character,
			r.End.Line, r.End.Character, d.Severity, d.Code, d.Message})
	}
	return strings.TrimPrefix(p.URI, "file:///work/"), ds
}
