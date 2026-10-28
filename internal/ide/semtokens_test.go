package ide

import (
	"encoding/json"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/protocol"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/span"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
)

// tokClient drives a whole server session with this package's
// analyzer through server.Driver, one message at a time.
type tokClient struct {
	t    testing.TB
	d    *server.Driver
	next int
	res  map[int]json.RawMessage
	errs map[int]string
	init json.RawMessage
}

// newTokClient starts a session over p in a client offering encs (nil:
// utf-16 only) and waits until it is initialized.
func newTokClient(t testing.TB, p project, encs []string) *tokClient {
	t.Helper()
	files := map[string][]byte{}
	for k, v := range p {
		files["/work/"+k] = v
	}
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	c := &tokClient{t: t, res: map[int]json.RawMessage{},
		errs: map[int]string{}}
	c.d = server.NewDriver(server.Options{Analyzer: New(Options{
		ToolVersion: "test"})}, sim.Env(), 1, func(s server.Send) error {
		if r, ok := s.Msg.(*jsonrpc2.Response); ok {
			if n, ok := r.ID.Int(); ok {
				c.res[int(n)] = r.Result
				if r.Error != nil {
					c.errs[int(n)] = r.Error.Message
				}
			}
		}
		return nil
	})
	caps := map[string]any{}
	if encs != nil {
		caps["general"] = map[string]any{"positionEncodings": encs}
	}
	c.init = c.request("initialize", map[string]any{"processId": nil,
		"rootUri": "file:///work", "capabilities": caps})
	c.notify("initialized", map[string]any{})
	return c
}

func (c *tokClient) send(m map[string]any) {
	c.t.Helper()
	m["jsonrpc"] = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.d.Deliver(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *tokClient) notify(method string, params any) {
	c.t.Helper()
	c.send(map[string]any{"method": method, "params": params})
}

// request sends a request and returns its result; an error response
// fails the test.
func (c *tokClient) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.next++
	id := c.next
	c.send(map[string]any{"id": id, "method": method, "params": params})
	res, ok := c.res[id]
	if !ok || c.errs[id] != "" {
		c.t.Fatalf("%s: no result (%s)", method, c.errs[id])
	}
	return res
}

func doc(uri string) map[string]any {
	return map[string]any{"uri": uri}
}

// tokensResult is a SemanticTokens or SemanticTokensDelta result.
type tokensResult struct {
	ResultID string   `json:"resultId"`
	Data     []uint32 `json:"data"`
	Edits    []struct {
		Start       int      `json:"start"`
		DeleteCount int      `json:"deleteCount"`
		Data        []uint32 `json:"data"`
	} `json:"edits"`
}

func (c *tokClient) tokens(method string, params map[string]any,
) tokensResult {
	c.t.Helper()
	var r tokensResult
	raw := c.request(method, params)
	if err := json.Unmarshal(raw, &r); err != nil {
		c.t.Fatalf("%s: %v: %s", method, err, raw)
	}
	return r
}

// tok is a decoded token: a byte span and its type and modifiers.
type tok struct {
	start, end int
	typ        int
	mods       uint32
}

// decode turns token data in encoding enc back into byte spans of the
// text with line index x, checking that every token is valid: inside
// one line, not empty, after the previous one, with a type and
// modifiers of the legend.
func decode(t testing.TB, x *span.Index, data []uint32,
	enc span.Encoding,
) []tok {
	t.Helper()
	if len(data)%5 != 0 {
		t.Fatalf("data length %d is not a multiple of 5", len(data))
	}
	var out []tok
	line, col, end := 0, 0, 0
	for i := 0; i < len(data); i += 5 {
		if data[i] > 0 {
			line, col = line+int(data[i]), int(data[i+1])
		} else {
			col += int(data[i+1])
		}
		a, ok1 := x.Offset(span.Position{Line: line, Col: col}, enc)
		b, ok2 := x.Offset(span.Position{Line: line,
			Col: col + int(data[i+2])}, enc)
		switch {
		case !ok1 || !ok2 || b > x.LineEnd(line):
			t.Fatalf("token %d at %d:%d+%d leaves its line", i/5, line,
				col, data[i+2])
		case b <= a || a < end:
			t.Fatalf("token %d at %d:%d+%d is empty or overlaps", i/5,
				line, col, data[i+2])
		case int(data[i+3]) >= len(TokenTypes) ||
			data[i+4] >= 1<<len(TokenModifiers):
			t.Fatalf("token %d: type %d, modifiers %b outside the "+
				"legend", i/5, data[i+3], data[i+4])
		}
		end = b
		out = append(out, tok{a, b, int(data[i+3]), data[i+4]})
	}
	return out
}

// overlapping returns the tokens of ts that overlap [a, b).
func overlapping(ts []tok, a, b int) []tok {
	var out []tok
	for _, t := range ts {
		if t.start < b && t.end > a {
			out = append(out, t)
		}
	}
	return out
}

func lspPos(x *span.Index, off int, enc span.Encoding) map[string]int {
	p := x.Position(off, enc)
	return map[string]int{"line": p.Line, "character": p.Col}
}

// TestSemanticTokensCorpus checks the semantic tokens properties of
// #55 on every file of every corpus design, in a utf-16 session:
//
//   - full results decode to valid tokens (one line each, sorted, not
//     overlapping, inside the legend);
//   - a range result is exactly the slice of the full result that
//     overlaps the range (the whole file and random ranges);
//   - a delta result applied to the result it names gives the full
//     result of the same text, over two edits in a row (a comment
//     line inserted, then a name changed).
func TestSemanticTokensCorpus(t *testing.T) {
	ps := corpus(t)
	counts := make([]int, len(TokenTypes))
	files := 0
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		p := ps[name]
		c := newTokClient(t, p, nil)
		for _, rel := range slices.Sorted(maps.Keys(p)) {
			if !workspace.IsVHDL(rel) {
				continue
			}
			files++
			full := checkFile(t, c, name+"/"+rel, rel, p[rel])
			for _, tk := range full {
				counts[tk.typ]++
			}
		}
	}
	t.Logf("%d files; tokens by type: %v", files, counts)
	for _, typ := range []int{tokClass, tokType, tokFunction,
		tokVariable, tokSignal, tokPort, tokKeyword, tokComment,
		tokEnumMember, tokProperty, tokParameter, tokNumber, tokString} {
		if counts[typ] < 10 {
			t.Errorf("only %d %s tokens in the corpus", counts[typ],
				TokenTypes[typ])
		}
	}
}

// checkFile checks one file (see TestSemanticTokensCorpus) and returns
// its tokens.
func checkFile(t *testing.T, c *tokClient, name, rel string,
	text []byte,
) []tok {
	t.Helper()
	enc := span.UTF16
	u := uri(rel)
	c.notify(
		"textDocument/didOpen",
		map[string]any{"textDocument": map[string]any{"uri": u,
			"languageId": "vhdl", "version": 1, "text": string(text)}},
	)
	defer c.notify("textDocument/didClose",
		map[string]any{"textDocument": doc(u)})
	x := span.NewIndex(text)
	full := c.tokens("textDocument/semanticTokens/full",
		map[string]any{"textDocument": doc(u)})
	ts := decode(t, x, full.Data, enc)
	if len(ts) == 0 || full.ResultID == "" {
		t.Fatalf("%s: no tokens", name)
	}
	h := fnv.New64a()
	h.Write([]byte(name))
	rnd := &env.SplitMix64{State: h.Sum64()}
	for i := range 6 {
		a, b := 0, len(text)
		if i > 0 {
			a = env.Intn(rnd, len(text)+1)
			b = a + env.Intn(rnd, min(len(text)-a, 400)+1)
		}
		pa, pb := lspPos(x, a, enc), lspPos(x, b, enc)
		// The span the server sees for these positions.
		a, _ = x.Offset(span.Position{Line: pa["line"],
			Col: pa["character"]}, enc)
		b, _ = x.Offset(span.Position{Line: pb["line"],
			Col: pb["character"]}, enc)
		r := c.tokens("textDocument/semanticTokens/range",
			map[string]any{"textDocument": doc(u),
				"range": map[string]any{"start": pa, "end": pb}})
		got := decode(t, x, r.Data, enc)
		if want := overlapping(ts, a, b); !slices.Equal(got, want) {
			t.Fatalf("%s: range [%d,%d): %d tokens, want the %d of the "+
				"full result overlapping it", name, a, b, len(got),
				len(want))
		}
	}
	// Two edits, each followed by a delta against the last result.
	base, prev, version := full.Data, full.ResultID, 1
	line := env.Intn(rnd, x.LineCount())
	edits := []struct {
		at, end int
		text    string
	}{{x.LineStart(line), x.LineStart(line), "-- edited\n"}}
	for _, tk := range ts {
		if tk.typ == tokSignal || tk.typ == tokVariable ||
			tk.typ == tokPort {
			n := 0 // the first edit moved what follows it
			if tk.start >= edits[0].at {
				n = len(edits[0].text)
			}
			edits = append(edits, struct {
				at, end int
				text    string
			}{tk.start + n, tk.end + n, "renamed_x"})
			break
		}
	}
	cur := text
	for _, e := range edits {
		cx := span.NewIndex(cur)
		version++
		c.notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{"uri": u, "version": version},
			"contentChanges": []any{map[string]any{
				"range": map[string]any{"start": lspPos(cx, e.at, enc),
					"end": lspPos(cx, e.end, enc)},
				"text": e.text}}})
		cur = slices.Concat(cur[:e.at], []byte(e.text), cur[e.end:])
		d := c.tokens("textDocument/semanticTokens/full/delta",
			map[string]any{"textDocument": doc(u),
				"previousResultId": prev})
		if d.Data != nil || d.Edits == nil || d.ResultID == prev {
			t.Fatalf("%s: not a delta: %+v", name, d)
		}
		got := slices.Clone(base)
		for _, ed := range d.Edits {
			got = slices.Concat(got[:ed.Start], ed.Data,
				got[ed.Start+ed.DeleteCount:])
		}
		again := c.tokens("textDocument/semanticTokens/full",
			map[string]any{"textDocument": doc(u)})
		if !slices.Equal(got, again.Data) {
			t.Fatalf("%s: full result plus delta %+v differs from "+
				"the full result", name, d.Edits)
		}
		decode(t, span.NewIndex(cur), again.Data, enc)
		base, prev = again.Data, again.ResultID
	}
	return ts
}

// fixture reads the in/ tree of a testdata/lsp case.
func fixture(t testing.TB, dir string) project {
	t.Helper()
	root := filepath.Join("../../testdata/lsp", dir, "in")
	p := project{}
	es, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		p[e.Name()] = b
	}
	return p
}

// TestSemanticTokensEncodings: on the non-ASCII fixture the tokens
// decode to the same byte spans under utf-8, utf-16 and utf-32 (#55).
// utf-8 and utf-16 sessions are negotiated as a client offers them;
// the server never picks utf-32 (it keeps utf-16 for a client that
// offers only utf-32, #25), so utf-32 runs the server's encoder,
// server.EncodeTokens, which the sessions are shown to use.
func TestSemanticTokensEncodings(t *testing.T) {
	p := fixture(t, "semantic-tokens/non-ascii")
	text := p["top.vhd"]
	x := span.NewIndex(text)
	u := uri("top.vhd")
	ts := analyze(t, snapshotOf(t, p, nil)).SemanticTokens(u)
	session := map[span.Encoding][]uint32{}
	for _, c := range []struct {
		encs []string
		enc  span.Encoding
	}{{[]string{"utf-8"}, span.UTF8}, {nil, span.UTF16}} {
		cl := newTokClient(t, p, c.encs)
		session[c.enc] = cl.tokens("textDocument/semanticTokens/full",
			map[string]any{"textDocument": doc(u)}).Data
	}
	var want []tok
	data := map[span.Encoding][]uint32{}
	for _, enc := range []span.Encoding{span.UTF8, span.UTF16,
		span.UTF32} {
		data[enc] = server.EncodeTokens(x, ts, nil, enc)
		if s, ok := session[enc]; ok && !slices.Equal(s, data[enc]) {
			t.Errorf("%s: the session's data differs from "+
				"EncodeTokens", enc)
		}
		got := decode(t, x, data[enc], enc)
		if want == nil {
			want = got
		} else if !slices.Equal(got, want) {
			t.Errorf("%s: tokens differ from utf-8:\n%v\n%v", enc, got,
				want)
		}
	}
	// The text makes the encodings differ: Latin-1 letters change
	// utf-8 against utf-16, emoji utf-16 against utf-32.
	if slices.Equal(data[span.UTF8], data[span.UTF16]) ||
		slices.Equal(data[span.UTF16], data[span.UTF32]) {
		t.Error("the fixture does not tell the encodings apart")
	}
	if len(want) < 20 {
		t.Errorf("only %d tokens", len(want))
	}
}

// TestSemanticTokensInactive: on the inactive-branch fixture each
// non-blank line of an inactive `elsif or `else branch is one comment
// token with the inactive modifier, and no other token lies in a
// branch; the active branch keeps its name tokens.
func TestSemanticTokensInactive(t *testing.T) {
	p := fixture(t, "semantic-tokens/inactive-branch")
	an := analyze(t, snapshotOf(t, p, nil))
	u := uri("top.vhd")
	f, _ := an.Result.File("top.vhd")
	regions := f.Parse.Inactive
	if len(regions) != 2 {
		t.Fatalf("%d inactive regions, want 2", len(regions))
	}
	text := f.Text
	var inactive []string
	for _, tk := range an.SemanticTokens(u) {
		s := string(text[tk.Span.Start:tk.Span.End])
		in := inRegion(regions, tk.Span)
		if (tk.Mods&modInactive != 0) != in ||
			in && (tk.Type != tokComment || tk.Mods != modInactive) {
			t.Errorf("%q: type %d, modifiers %b, in a branch %v", s,
				tk.Type, tk.Mods, in)
		}
		if in {
			inactive = append(inactive, s)
		}
	}
	want := []string{"q <= '0'; -- slow", "-- an indented comment",
		"q <= bad name;"}
	if !slices.Equal(inactive, want) {
		t.Errorf("inactive tokens %q, want %q", inactive, want)
	}
	active := strings.Index(string(text), "q <= '1'")
	q, ok := tokenAt(an.SemanticTokens(u), active)
	if !ok || q.Type != tokPort {
		t.Errorf("q in the active branch: %+v", q)
	}
}

func tokenAt(ts []server.SemToken, off int) (server.SemToken, bool) {
	for _, t := range ts {
		if t.Span.Start == off {
			return t, true
		}
	}
	return server.SemToken{}, false
}

// TestSemanticTokensKinds: the type and modifiers of each kind of name
// on the full fixture, and the legend the server advertises
// (decoded with internal/protocol).
func TestSemanticTokensKinds(t *testing.T) {
	p := fixture(t, "semantic-tokens/full")
	an := analyze(t, snapshotOf(t, p, nil))
	u := uri("top.vhd")
	text := string(p["top.vhd"])
	ts := an.SemanticTokens(u)
	for _, c := range []struct {
		at   string // the text before the name, the name
		name string
		typ  int
		mods uint32
	}{
		{"library ", "ieee", tokNamespace, modDefaultLibrary},
		{"use ", "work", tokNamespace, 0},
		{"work.", "defs", tokClass, 0},
		{"entity ", "top", tokClass, modDeclaration},
		{"generic (", "N", tokVariable,
			modDeclaration | modReadonly | modStatic},
		{"    ", "clk", tokPort, modDeclaration | modReadonly},
		{"    ", "q", tokPort, modDeclaration},
		{": in  ", "std_logic", tokType, modDefaultLibrary},
		{"vector(", "WIDTH", tokVariable, modReadonly},
		{"signal ", "st", tokSignal, modDeclaration},
		{":= ", "IDLE", tokEnumMember, 0},
		{"variable ", "count", tokVariable, modDeclaration},
		{":= ", "inc", tokFunction, 0},
		{"for ", "i", tokVariable, modDeclaration | modReadonly},
		{"p.", "first", tokProperty, 0},
		{"if ", "rising_edge", tokFunction, modDefaultLibrary},
		{"10 ", "ns", tokEnumMember, modDefaultLibrary},
		{"-- ", "", tokComment, 0},
		{"report ", `"count "`, tokString, 0},
		{"after ", "10", tokNumber, 0},
		{"\n", "architecture", tokKeyword, 0},
	} {
		i := strings.Index(text, c.at+c.name)
		if i < 0 {
			t.Fatalf("no %q in top.vhd", c.at+c.name)
		}
		off := i + len(c.at)
		if c.name == "" { // the comment starts at its --
			off = i
		}
		tk, ok := tokenAt(ts, off)
		if !ok || tk.Type != c.typ || tk.Mods != c.mods {
			t.Errorf("%s: %+v, want type %s, modifiers %b", c.at+c.name,
				tk, TokenTypes[c.typ], c.mods)
		}
	}
	cl := newTokClient(t, p, nil)
	var res protocol.InitializeResult
	if err := json.Unmarshal(cl.init, &res); err != nil {
		t.Fatal(err)
	}
	opts, ok := res.Capabilities.SemanticTokensProvider.Value.(protocol.SemanticTokensOptions)
	if !ok || !slices.Equal(opts.Legend.TokenTypes, TokenTypes) ||
		!slices.Equal(opts.Legend.TokenModifiers, TokenModifiers) ||
		opts.Range.Value != true {
		t.Errorf(
			"semanticTokensProvider %+v",
			res.Capabilities.SemanticTokensProvider,
		)
	}
	full, ok := opts.Full.Value.(protocol.SemanticTokensOptionsFull)
	if !ok || full.Delta == nil || !*full.Delta {
		t.Errorf("full %+v", opts.Full)
	}
}
