package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/ide"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/span"
)

// The quiescence half of FuzzLSPReplay (#30): the same fuzz input is
// also read as an edit script. Its first byte picks a corpus design
// (#8) and its second a VHDL file of it; every following triple
// (op, place, snippet) is one edit typed into the open document, with
// a few core steps between edits so later edits land while analysis
// slices run. After a save, once the server settles with the analyzer
// of `gohard lsp`, the published diagnostics of the file equal
// `gohard check` on the final text.

var (
	// fuzzCompared counts the diagnostics fuzzQuiescence found equal.
	fuzzCompared   int
	fuzzCorpusOnce sync.Once
	fuzzCorpus     []map[string][]byte // corpus designs under /work
)

func fuzzProjects(t tb) []map[string][]byte {
	fuzzCorpusOnce.Do(func() {
		const dir = "../../testdata/corpus"
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			if !e.IsDir() || e.Name() == "golden" {
				continue
			}
			files := map[string][]byte{}
			root := os.DirFS(filepath.Join(dir, e.Name()))
			_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry,
				err error,
			) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if p == "golden" {
						return fs.SkipDir
					}
					return nil
				}
				b, err := fs.ReadFile(root, p)
				files[path.Join(workDir, p)] = b
				return err
			})
			fuzzCorpus = append(fuzzCorpus, files)
		}
	})
	if len(fuzzCorpus) == 0 {
		t.Fatalf("no corpus designs under testdata/corpus")
	}
	return fuzzCorpus
}

// fuzzSnippets are typed by the edit script: declarations, statements,
// broken syntax and names that resolve or not.
var fuzzSnippets = []string{
	";", " ", "\n", "end", "signal zz : bit;\n", "x", "(", ")",
	"constant c9 : integer := 3;\n", "begin", "is", "--", "\"",
	"std_logic", "work.", "use work.nothing.all;\n", "  report \"r\";\n",
	"0", "'1'", " + 1", "end;", "entity e9 is end;\n", "all", "\t",
	"é", "\\ext id\\", "process begin wait; end process;\n",
}

// fuzzQuiescence runs the edit-script reading of data. On a failure
// it writes the session (project files embedded, then the client
// messages) to seed for `gohard lsp replay`.
func fuzzQuiescence(t tb, failed func() bool, data []byte, seed string) {
	if len(data) < 2 || len(data) > 600 {
		return
	}
	projects := fuzzProjects(t)
	files := projects[int(data[0])%len(projects)]
	var vhd []string
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if strings.HasSuffix(p, ".vhd") {
			vhd = append(vhd, strings.TrimPrefix(p, workDir+"/"))
		}
	}
	if len(vhd) == 0 {
		return
	}
	rel := vhd[int(data[1])%len(vhd)]
	uri := "file://" + workDir + "/" + rel
	text := files[workDir+"/"+rel]

	sim := simenv.New(simenv.Options{Files: files, Dir: workDir})
	h, err := server.SessionHeader(sim.Env(), workDir, cli.Version)
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	var client [][]byte
	var pub []string
	var d *server.Driver
	d = server.NewDriver(server.Options{Version: cli.Version,
		Analyzer: server.WithFormatting(ide.New(ide.Options{
			ToolVersion: cli.Version, Jobs: sim.Env().Jobs, Limit: 1}),
			cli.Version)}, sim.Env(), 1, func(m server.Send) error {
		n, ok := m.Msg.(*jsonrpc2.Notification)
		if !ok || n.Method != "textDocument/publishDiagnostics" {
			return nil
		}
		var p struct {
			URI         string
			Diagnostics []struct {
				Range         server.Range
				Severity      int
				Code, Message string
			}
		}
		_ = json.Unmarshal(n.Params, &p)
		if p.URI != uri {
			return nil
		}
		pub = pub[:0]
		for _, x := range p.Diagnostics {
			r := x.Range
			pub = append(pub, fmt.Sprintf("%d:%d-%d:%d %d %s %s",
				r.Start.Line, r.Start.Character, r.End.Line,
				r.End.Character, x.Severity, x.Code, x.Message))
		}
		return nil
	})
	push := func(v any) {
		b, _ := json.Marshal(v)
		client = append(client, b)
		d.Push(b)
	}
	defer func() {
		if !failed() {
			return
		}
		lines := make([]json.RawMessage, 0, len(files)+len(client))
		for _, p := range slices.Sorted(maps.Keys(files)) {
			lines = append(lines, server.FileLine(
				strings.TrimPrefix(p, workDir+"/"), files[p]))
		}
		for _, c := range client {
			lines = append(lines, c)
		}
		file, err := repro.Session{Header: h, Messages: lines}.Marshal()
		if err == nil && writeSeedFile(seed, file) == nil {
			t.Errorf("session written to %s", seed)
		}
	}()
	push(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"rootUri": "file://" + workDir,
			"capabilities": map[string]any{"general": map[string]any{
				"positionEncodings": []string{"utf-8"}}}}})
	push(map[string]any{"jsonrpc": "2.0", "method": "initialized",
		"params": map[string]any{}})
	push(map[string]any{"jsonrpc": "2.0",
		"method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri,
				"languageId": "vhdl", "version": 1,
				"text": string(text)}}})
	version := 1
	for i := 2; i+2 < len(data); i += 3 {
		op, place, snip := data[i], data[i+1], data[i+2]
		at := func(b byte) int { // a rune start in text
			off := 0
			if len(text) > 0 {
				off = int(b) * (len(text) + 1) / 256
			}
			for off > 0 && off < len(text) &&
				!utf8.RuneStart(text[off]) {
				off--
			}
			return off
		}
		a := at(place)
		b := a
		repl := fuzzSnippets[int(snip)%len(fuzzSnippets)]
		switch op % 3 {
		case 0: // delete
			repl = ""
			b = a + 1 + int(snip%12)
		case 2: // replace
			b = a + 1 + int(op/3%6)
		}
		b = min(b, len(text))
		for b > a && b < len(text) && !utf8.RuneStart(text[b]) {
			b++
		}
		x := span.NewIndex(text)
		pa, pb := x.Position(a, span.UTF8), x.Position(b, span.UTF8)
		text = slices.Concat(text[:a], []byte(repl), text[b:])
		version++
		push(map[string]any{"jsonrpc": "2.0",
			"method": "textDocument/didChange", "params": map[string]any{
				"textDocument": map[string]any{"uri": uri,
					"version": version},
				"contentChanges": []any{map[string]any{
					"range": server.Range{
						Start: server.Position{Line: pa.Line,
							Character: pa.Col},
						End: server.Position{Line: pb.Line,
							Character: pb.Col}},
					"text": repl}}}})
		// Let a few slices run before the next edit arrives.
		if err := d.RunTo(d.Steps() + uint64(op%8)); err != nil {
			t.Fatalf("step: %v", err)
		}
	}
	push(map[string]any{"jsonrpc": "2.0",
		"method": "textDocument/didSave", "params": map[string]any{
			"textDocument": map[string]any{"uri": uri}}})
	if err := d.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	// gohard check on the final text, saved.
	if err := sim.FS.WriteFile(workDir+"/"+rel, text, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := fuzzCheck(t, sim, rel)
	fuzzCompared += len(want)
	if !slices.Equal(pub, want) {
		t.Errorf("%s after %d edits: server\n%s\ncheck\n%s\ntext:\n%s",
			rel, version-1, strings.Join(pub, "\n"),
			strings.Join(want, "\n"), text)
	}
}

// fuzzCheck returns `gohard check --format json` on the project for
// rel, in the server's form (zero-based UTF-8 range, LSP severity).
func fuzzCheck(t tb, sim *simenv.Sim, rel string) []string {
	e := sim.Env()
	var out, errb bytes.Buffer
	e.Transport.Stdin = simenv.NewScript(nil)
	e.Transport.Stdout, e.Transport.Stderr = &out, &errb
	cli.Run(context.Background(), e, []string{"check", "--format",
		"json"})
	var r struct {
		Diagnostics []struct {
			Path                    string
			Line, Col               int
			EndLine                 int `json:"end_line"`
			EndCol                  int `json:"end_col"`
			Severity, Code, Message string
		}
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("check: %v\n%s%s", err, out.Bytes(), errb.Bytes())
	}
	sev := map[string]int{"error": 1, "warning": 2, "hint": 4}
	var got []string
	for _, d := range r.Diagnostics {
		if d.Path != rel {
			continue
		}
		el, ec := d.EndLine, d.EndCol
		if el == 0 {
			el, ec = d.Line, d.Col
		}
		got = append(got, fmt.Sprintf("%d:%d-%d:%d %d %s %s", d.Line-1,
			d.Col-1, el-1, ec-1, sev[d.Severity], d.Code, d.Message))
	}
	return got
}

// TestFuzzQuiescenceScripts runs the edit-script half of FuzzLSPReplay
// on fixed pseudo-random scripts over every corpus design, so plain go
// test checks the property beyond the saved inputs.
func TestFuzzQuiescenceScripts(t *testing.T) {
	n := len(fuzzProjects(t))
	rng := env.SplitMix64{State: 25}
	fuzzCompared = 0
	for i := range 4 * n {
		data := []byte{byte(i % n), byte(rng.Uint64())}
		for range 3 * (1 + int(rng.Uint64()%8)) {
			data = append(data, byte(rng.Uint64()))
		}
		seed := filepath.Join(t.TempDir(), "failure.jsonl")
		fuzzQuiescence(t, t.Failed, data, seed)
		if t.Failed() {
			t.Fatalf("script %d: %x", i, data)
		}
	}
	// The edits must reach the diagnostics.
	if fuzzCompared < 20 {
		t.Fatalf("only %d diagnostics compared", fuzzCompared)
	}
	t.Logf("%d diagnostics compared", fuzzCompared)
}
