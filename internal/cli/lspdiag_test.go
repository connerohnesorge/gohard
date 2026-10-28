package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/ide"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// lspDiag is one diagnostic in the common form of check and the
// server: zero-based UTF-8 range, LSP severity, code and message.
type lspDiag struct {
	L, C, EL, EC, Sev int
	Code, Msg         string
}

func (d lspDiag) String() string {
	return fmt.Sprintf("%d:%d-%d:%d %d %s %s", d.L+1, d.C+1, d.EL+1,
		d.EC+1, d.Sev, d.Code, d.Msg)
}

// checkDiags runs `gohard check --format json` in /work and returns
// its diagnostics by path, in the server's form.
func checkDiags(t *testing.T, sim *simenv.Sim) map[string][]lspDiag {
	t.Helper()
	_, out, stderr := runIn(sim, nil, "check", "--format", "json")
	var r struct {
		Diagnostics []struct {
			Path                    string
			Line, Col               int
			EndLine                 int `json:"end_line"`
			EndCol                  int `json:"end_col"`
			Severity, Code, Message string
		}
	}
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("check: %v\n%s%s", err, out, stderr)
	}
	sev := map[string]int{"error": 1, "warning": 2, "hint": 4}
	got := map[string][]lspDiag{}
	for _, d := range r.Diagnostics {
		if d.Path == "" || d.Path == "gohard.yaml" {
			continue
		}
		el, ec := d.EndLine, d.EndCol
		if el == 0 {
			el, ec = d.Line, d.Col
		}
		got[d.Path] = append(got[d.Path], lspDiag{d.Line - 1, d.Col - 1,
			el - 1, ec - 1, sev[d.Severity], d.Code, d.Message})
	}
	return got
}

// session is an in-memory editor session with the analyzer of
// `gohard lsp` on the server's own code path (server.Driver).
type session struct {
	t     *testing.T
	d     *server.Driver
	pubs  map[string][]lspDiag // latest publish by path
	texts map[string][]byte    // open documents by path
	ver   int
}

func newSession(t *testing.T, sim *simenv.Sim, jobs int) *session {
	s := &session{t: t, pubs: map[string][]lspDiag{},
		texts: map[string][]byte{}}
	an := server.WithFormatting(ide.New(ide.Options{
		ToolVersion: cli.Version, Jobs: sim.Env().Jobs, Limit: jobs}),
		cli.Version)
	s.d = server.NewDriver(server.Options{Analyzer: an,
		Version: cli.Version}, sim.Env(), jobs, s.recv)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"processId":null,"rootUri":"file:///work","capabilities":` +
		`{"general":{"positionEncodings":["utf-8"]}}}}`)
	s.send(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	return s
}

func (s *session) recv(m server.Send) error {
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
	if err := json.Unmarshal(n.Params, &p); err != nil {
		s.t.Fatal(err)
	}
	rel := strings.TrimPrefix(p.URI, "file:///work/")
	var ds []lspDiag
	for _, d := range p.Diagnostics {
		r := d.Range
		ds = append(ds, lspDiag{r.Start.Line, r.Start.Character,
			r.End.Line, r.End.Character, d.Severity, d.Code, d.Message})
	}
	if len(ds) == 0 {
		delete(s.pubs, rel)
	} else {
		s.pubs[rel] = ds
	}
	return nil
}

func (s *session) send(body string) {
	s.t.Helper()
	if err := s.d.Deliver([]byte(body)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) open(rel string, text []byte) {
	s.texts[rel] = text
	s.ver = 1
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0",
		"method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": "file:///work/" + rel,
				"languageId": "vhdl", "version": 1,
				"text": string(text)}}})
	s.send(string(b))
}

// edit replaces [a, b) of the open document rel with repl.
func (s *session) edit(rel string, a, b int, repl string) {
	old := s.texts[rel]
	x := span.NewIndex(old)
	pa, pb := x.Position(a, span.UTF8), x.Position(b, span.UTF8)
	s.texts[rel] = slices.Concat(old[:a], []byte(repl), old[b:])
	s.ver++
	b2, _ := json.Marshal(map[string]any{"jsonrpc": "2.0",
		"method": "textDocument/didChange", "params": map[string]any{
			"textDocument": map[string]any{"uri": "file:///work/" + rel,
				"version": s.ver},
			"contentChanges": []any{map[string]any{
				"range": server.Range{
					Start: server.Position{Line: pa.Line,
						Character: pa.Col},
					End: server.Position{Line: pb.Line,
						Character: pb.Col}},
				"text": repl}}}})
	s.send(string(b2))
}

// corpusProjects returns the oracle corpus designs (#8) as project
// trees under /work: every file of testdata/corpus/<name>/ except the
// goldens.
func corpusProjects(t *testing.T) map[string]map[string][]byte {
	t.Helper()
	const dir = "../../testdata/corpus"
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string][]byte{}
	for _, e := range es {
		if !e.IsDir() || e.Name() == "golden" {
			continue
		}
		files := map[string][]byte{}
		root := os.DirFS(filepath.Join(dir, e.Name()))
		err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry,
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
			files[path.Join("/work", p)] = b
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = files
	}
	return out
}

// snippets are inserted by the random edits: whole declarations,
// statements, broken syntax and names that resolve or not.
var snippets = []string{
	";", " ", "\n", "end", "signal zz : bit;\n", "x", "(", ")",
	"constant c9 : integer := 3;\n", "begin", "is", "--", "\"",
	"std_logic", "work.", "use work.nothing.all;\n", "  report \"r\";\n",
	"0", "'1'", " + 1", "end;", "entity e9 is end;\n", "all",
}

// randomEdit applies one random edit to the open document rel.
func randomEdit(s *session, rng *env.SplitMix64, rel string) {
	text := s.texts[rel]
	pick := func(n int) int {
		if n <= 0 {
			return 0
		}
		return int(rng.Uint64() % uint64(n))
	}
	start := func(off int) int { // snap to a rune start
		for off > 0 && off < len(text) && !utf8.RuneStart(text[off]) {
			off--
		}
		return off
	}
	a := start(pick(len(text) + 1))
	b := a
	switch pick(3) {
	case 0: // delete
		b = start(min(len(text), a+1+pick(12)))
		s.edit(rel, a, b, "")
	case 1: // insert
		s.edit(rel, a, a, snippets[pick(len(snippets))])
	default: // replace
		b = start(min(len(text), a+1+pick(6)))
		s.edit(rel, a, b, snippets[pick(len(snippets))])
	}
}

// TestLSPQuiescentDiagnosticsEqualCheck is the quiescence invariant of
// #25 as a property: random edit sequences over every corpus design,
// typed into open documents through the server, then once the server
// is idle the published diagnostics of every file equal `gohard check`
// on the same text (written to disk).
func TestLSPQuiescentDiagnosticsEqualCheck(t *testing.T) {
	projects := corpusProjects(t)
	compared = 0
	for _, name := range slices.Sorted(maps.Keys(projects)) {
		files := projects[name]
		var vhd []string
		for _, p := range slices.Sorted(maps.Keys(files)) {
			if strings.HasSuffix(p, ".vhd") {
				vhd = append(vhd, strings.TrimPrefix(p, "/work/"))
			}
		}
		for seed := uint64(1); seed <= 3; seed++ {
			t.Run(fmt.Sprintf("%s/seed%d", name, seed), func(t *testing.T) {
				quiescence(t, files, vhd, seed)
			})
		}
	}
	// The edits must reach the diagnostics: most rounds break a file.
	if compared < 50 {
		t.Fatalf("only %d diagnostics compared", compared)
	}
	t.Logf("%d diagnostics compared", compared)
}

// compared counts the diagnostics compareQuiet found equal.
var compared int

func quiescence(t *testing.T, files map[string][]byte, vhd []string,
	seed uint64,
) {
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	s := newSession(t, sim, 1+int(seed%4))
	compareQuiet(t, s, sim, "initial")
	rng := &env.SplitMix64{State: seed * 0x9e3779b97f4a7c15}
	for round := range 2 {
		rel := vhd[int(rng.Uint64()%uint64(len(vhd)))]
		if _, ok := s.texts[rel]; !ok {
			s.open(rel, files["/work/"+rel])
		}
		for range 1 + int(rng.Uint64()%6) {
			randomEdit(s, rng, rel)
		}
		for _, p := range slices.Sorted(maps.Keys(s.texts)) {
			err := sim.FS.WriteFile("/work/"+p, s.texts[p], 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}
		compareQuiet(t, s, sim, fmt.Sprintf("round %d", round))
	}
}

func compareQuiet(t *testing.T, s *session, sim *simenv.Sim, when string) {
	t.Helper()
	want := checkDiags(t, sim)
	paths := slices.Sorted(maps.Keys(want))
	for _, p := range slices.Sorted(maps.Keys(s.pubs)) {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	for _, p := range paths {
		compared += len(want[p])
		if !slices.Equal(want[p], s.pubs[p]) {
			var w, g bytes.Buffer
			for _, d := range want[p] {
				fmt.Fprintln(&w, d)
			}
			for _, d := range s.pubs[p] {
				fmt.Fprintln(&g, d)
			}
			t.Fatalf("%s: %s: check:\n%sserver:\n%stext:\n%s", when, p,
				w.String(), g.String(), s.texts[p])
		}
	}
}

// TestLSPDiagnosticsFixturesEqualCheck: every testdata/lsp/diagnostics
// fixture prints the diagnostics `gohard check` reports for its file
// (#25: the unsupported/construct diagnostic of a construct GoHard
// does not implement is identical in the editor).
func TestLSPDiagnosticsFixturesEqualCheck(t *testing.T) {
	dir := filepath.Join(lspRoot, "diagnostics")
	cases, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			files := map[string][]byte{}
			in := os.DirFS(filepath.Join(dir, c.Name(), "in"))
			err := fs.WalkDir(in, ".", func(p string, d fs.DirEntry,
				err error,
			) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, err := fs.ReadFile(in, p)
				files[path.Join("/work", p)] = b
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := os.ReadFile(filepath.Join(dir, c.Name(), "cmd"))
			if err != nil {
				t.Fatal(err)
			}
			var at string
			for _, l := range strings.Split(string(cmd), "\n") {
				if f := strings.Fields(l); len(f) == 5 && f[0] == "gohard" {
					at = f[4]
				}
			}
			rel := strings.Split(at, ":")[0]
			sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
			want := checkDiags(t, sim)[rel]
			if len(want) == 0 {
				t.Fatalf("check reports nothing for %s", rel)
			}
			s := newSession(t, sim, 1)
			s.open(rel, files["/work/"+rel])
			if !slices.Equal(want, s.pubs[rel]) {
				t.Fatalf("check %v\nserver %v", want, s.pubs[rel])
			}
		})
	}
}
