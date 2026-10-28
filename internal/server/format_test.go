package server_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/format"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/span"
)

func TestWithFormattingFeatures(t *testing.T) {
	for _, c := range []struct {
		an   server.Analyzer
		want server.Feature
	}{
		{nil, 0},
		{server.Nop{}, 0},
		{echo{}, echo{}.Features().Set},
	} {
		f := server.WithFormatting(c.an, testVersion).Features()
		want := c.want | server.FeatureFormatting |
			server.FeatureRangeFormatting
		if f.Set != want {
			t.Errorf("%T: features %b, want %b", c.an, f.Set, want)
		}
	}
	snap := (*server.Snapshot)(nil)
	an := server.WithFormatting(echo{}, testVersion)
	// An unknown document gets no edits.
	if es := run(an.Format(snap, "file:///work/top.vhd", nil)); len(es) != 0 {
		t.Errorf("unknown document: %v", es)
	}
}

// run runs a task to the end and returns its value.
func run[T any](task server.Task[T]) T {
	for {
		s := task.Run(server.SliceBudget)
		if s.Done || s.Next == nil {
			return s.Value
		}
		task = s.Next
	}
}

type lspEdit struct {
	Range   server.Range `json:"range"`
	NewText string       `json:"newText"`
}

// fmtSession runs one in-memory session that opens text as top.vhd and
// sends a whole-file formatting request (id 2) and one range
// formatting request per range (ids 3, 4, ...). It returns the edits
// of each response, in request order.
func fmtSession(t *testing.T, text []byte, enc string,
	ranges []server.Range,
) [][]lspEdit {
	t.Helper()
	sim := simenv.New(simenv.Options{Dir: "/work"})
	got := map[int64]json.RawMessage{}
	d := server.NewDriver(server.Options{
		Analyzer: server.WithFormatting(server.Nop{}, testVersion),
		Version:  testVersion}, sim.Env(), 1,
		func(s server.Send) error {
			if r, ok := s.Msg.(*jsonrpc2.Response); ok {
				if r.Error != nil {
					t.Errorf("error response: %+v", r.Error)
				}
				if n, ok := r.ID.Int(); ok {
					got[n] = r.Result
				}
			}
			return nil
		})
	uri := "file:///work/top.vhd"
	doc := map[string]any{"uri": uri}
	opts := map[string]any{"tabSize": 2, "insertSpaces": true}
	msgs := []jsonrpc2.Message{
		&jsonrpc2.Request{ID: jsonrpc2.IntID(1), Method: "initialize",
			Params: mustJSON(map[string]any{"processId": nil,
				"rootUri": "file:///work",
				"capabilities": map[string]any{"general": map[string]any{
					"positionEncodings": []string{enc}}}})},
		&jsonrpc2.Notification{Method: "initialized",
			Params: mustJSON(struct{}{})},
		&jsonrpc2.Notification{Method: "textDocument/didOpen",
			Params: mustJSON(map[string]any{"textDocument": map[string]any{
				"uri": uri, "languageId": "vhdl", "version": 1,
				"text": string(text)}})},
		&jsonrpc2.Request{ID: jsonrpc2.IntID(2),
			Method: "textDocument/formatting",
			Params: mustJSON(map[string]any{"textDocument": doc,
				"options": opts})},
	}
	for i, r := range ranges {
		msgs = append(msgs, &jsonrpc2.Request{ID: jsonrpc2.IntID(
			int64(3 + i)), Method: "textDocument/rangeFormatting",
			Params: mustJSON(map[string]any{"textDocument": doc,
				"range": r, "options": opts})})
	}
	msgs = append(msgs,
		&jsonrpc2.Request{ID: jsonrpc2.IntID(99), Method: "shutdown"},
		&jsonrpc2.Notification{Method: "exit"})
	for _, m := range msgs {
		if err := d.Deliver(server.Send{Msg: m}.Body()); err != nil {
			t.Fatal(err)
		}
	}
	out := make([][]lspEdit, 1+len(ranges))
	for i := range out {
		raw, ok := got[int64(2+i)]
		if !ok {
			t.Fatalf("no response to request %d", 2+i)
		}
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			t.Fatalf("response %d: %v: %s", 2+i, err, raw)
		}
	}
	return out
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// lspOffset is the LSP clamping rule: a line past the end is the end
// of the text, a character past the end of its line is the line end.
func lspOffset(x *span.Index, p server.Position, enc span.Encoding) int {
	if p.Line >= x.LineCount() {
		return x.Len()
	}
	off, _ := x.Offset(span.Position{Line: p.Line, Col: p.Character}, enc)
	return off
}

// byteEdits converts LSP edits back to byte offsets, checking that each
// position names a rune boundary exactly.
func byteEdits(t *testing.T, x *span.Index, enc span.Encoding,
	es []lspEdit,
) []format.Edit {
	t.Helper()
	out := make([]format.Edit, 0, len(es))
	for _, e := range es {
		var offs [2]int
		for i, p := range []server.Position{e.Range.Start, e.Range.End} {
			off, ok := x.Offset(span.Position{Line: p.Line,
				Col: p.Character}, enc)
			back := x.Position(off, enc)
			if !ok || back.Line != p.Line || back.Col != p.Character {
				t.Fatalf("edit position %+v is not a position of the "+
					"text", p)
			}
			offs[i] = off
		}
		out = append(out, format.Edit{Start: offs[0], End: offs[1],
			NewText: e.NewText})
	}
	return out
}

// corpusFiles are the parse rule files and the oracle corpus.
func corpusFiles(t *testing.T) []string {
	var out []string
	for _, root := range []string{"../../testdata/parse",
		"../../testdata/corpus"} {
		err := filepath.WalkDir(root, func(p string, e fs.DirEntry,
			err error,
		) error {
			if err != nil {
				return err
			}
			if !e.IsDir() && (strings.HasSuffix(p, ".vhd") ||
				strings.HasSuffix(p, ".vhdl")) {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		t.Fatal("no corpus files")
	}
	return out
}

// TestRangeFormattingStaysInRange is the property of #32 over the LSP:
// for random ranges (reversed, empty, past the end of a line or of the
// text, inside surrogate pairs) over every corpus file, in utf-8 and
// utf-16 sessions, every range formatting edit lies inside the
// requested range, and the edits are exactly format.Range's on the
// same bytes. The whole-file edits give format.Source's text, the
// function `gohard fmt` writes, and edits never overlap.
func TestRangeFormattingStaysInRange(t *testing.T) {
	popts := parser.FromConfig(config.Defaults(), testVersion)
	opts := format.FromConfig(config.Defaults())
	encs := []struct {
		name string
		enc  span.Encoding
	}{{"utf-8", span.UTF8}, {"utf-16", span.UTF16}}
	files, checked, edits := corpusFiles(t), 0, 0
	for fi, p := range files {
		text := mustRead(t, p)
		if !utf8.Valid(text) {
			continue // JSON cannot carry the bytes as they are
		}
		res := parser.Parse(text, popts)
		x := span.NewIndex(text)
		rng := &env.SplitMix64{State: uint64(fi) + 1}
		e := encs[fi%2]
		var ranges []server.Range
		var want [][2]int
		for range 8 {
			var r server.Range
			for _, pos := range []*server.Position{&r.Start, &r.End} {
				line := env.Intn(rng, x.LineCount()+2)
				n := 1
				if line < x.LineCount() {
					n = x.LineEnd(line) - x.LineStart(line) + 1
				}
				pos.Line = line
				if env.Intn(rng, 3) > 0 { // most ranges are whole lines
					pos.Character = env.Intn(rng, n+3)
				}
			}
			a, b := lspOffset(x, r.Start, e.enc), lspOffset(x, r.End, e.enc)
			ranges = append(ranges, r)
			want = append(want, [2]int{min(a, b), max(a, b)})
		}
		got := fmtSession(t, text, e.name, ranges)
		whole, err := format.Source(text, popts, opts)
		if !format.Parses(res) {
			for i, es := range got {
				if len(es) != 0 {
					t.Errorf("%s: request %d: edits for a file that "+
						"does not parse", p, 2+i)
				}
			}
			continue
		}
		checked++
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		all := byteEdits(t, x, e.enc, got[0])
		if out := format.Apply(text, all); !bytes.Equal(out, whole.Text) {
			t.Errorf("%s: whole-file edits do not give gohard fmt's text",
				p)
		}
		for i, es := range got[1:] {
			be := byteEdits(t, x, e.enc, es)
			start, end := want[i][0], want[i][1]
			for j, ed := range be {
				if ed.Start < start || ed.End > end ||
					j > 0 && ed.Start < be[j-1].End {
					t.Errorf("%s (%s): range %+v = [%d,%d): edit %+v "+
						"outside the range or overlapping", p, e.name,
						ranges[i], start, end, ed)
				}
			}
			if exp := format.Range(res, opts, start, end); !slices.Equal(
				be, exp) {
				t.Errorf("%s (%s): range [%d,%d): edits %v, want "+
					"format.Range's %v", p, e.name, start, end, be, exp)
			}
			edits += len(be)
		}
	}
	if checked == 0 || edits == 0 {
		t.Fatalf("checked %d files, %d range edits: the property is "+
			"vacuous", checked, edits)
	}
	t.Logf("%d files, %d range edits", checked, edits)
}

// TestRangeFormattingNoEditsOutside is the LSP test of #32's Done
// when: formatting lines 2 and 3 of a file where every line needs
// formatting changes those lines only.
func TestRangeFormattingNoEditsOutside(t *testing.T) {
	text := []byte("ENTITY   top IS\nport(a:in bit;\nb:out bit);\n" +
		"END;\narchitecture rtl of top is\nbegin\nb<=a;\nend;\n")
	r := server.Range{Start: server.Position{Line: 1},
		End: server.Position{Line: 3}}
	for _, enc := range []string{"utf-8", "utf-16"} {
		got := fmtSession(t, text, enc, []server.Range{r})
		if len(got[0]) == 0 {
			t.Fatal("the whole file needs no formatting")
		}
		lines := map[int]bool{}
		for _, e := range got[0] {
			lines[e.Range.Start.Line] = true
		}
		if len(lines) < 5 {
			t.Fatalf("whole-file edits touch only lines %v", lines)
		}
		if len(got[1]) == 0 {
			t.Fatalf("%s: no edits inside the range", enc)
		}
		for _, e := range got[1] {
			if e.Range.Start.Line < 1 || e.Range.End.Line > 2 {
				t.Errorf("%s: edit %+v outside lines 1-2", enc, e)
			}
		}
		x := span.NewIndex(text)
		out := format.Apply(text, byteEdits(t, x, span.UTF8, got[1]))
		ol := strings.SplitAfter(string(out), "\n")
		tl := strings.SplitAfter(string(text), "\n")
		if ol[0] != tl[0] || !slices.Equal(ol[3:], tl[3:]) {
			t.Errorf("%s: lines outside the range changed:\n%s", enc, out)
		}
	}
}
