package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
)

// TestServeRealPipe runs the stdio shell over real OS pipes (the
// jsonrpc2 reader and writer goroutines; CI runs it under -race, #25)
// and checks that its output equals the replay of the messages it
// recorded, byte for byte (#29).
func TestServeRealPipe(t *testing.T) {
	f := loadFixture(t, sessionRoot+"/incremental-edits")
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var frames []byte
	for _, m := range f.messages {
		frames = jsonrpc2.AppendFrame(frames, m)
	}
	if _, err := inW.Write(frames); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	sim := simenv.New(simenv.Options{Dir: workDir, Files: f.files})
	e := sim.Env()
	e.Transport.Stdin, e.Transport.Stdout = inR, outW
	var recorded []json.RawMessage
	opts := server.Options{Analyzer: f.analyzer, Version: testVersion}
	code := server.Serve(context.Background(), e, opts, 1,
		func(a server.Arrival) error {
			if l := a.Line(); l != nil {
				recorded = append(recorded, l)
			}
			return nil
		})
	outW.Close()
	live, err := io.ReadAll(outR)
	if err != nil {
		t.Fatal(err)
	}
	inR.Close()
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	sess, sim2 := f.session(t)
	sess.Messages = recorded
	got, err := server.Replay(sess, sim2.Env().FS, opts, "live")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(live, got.Output) {
		t.Fatalf("live output differs from its replay:\n%s\n---\n%s",
			live, got.Output)
	}
	if len(got.Client) != len(f.messages) {
		t.Fatalf("recorded %d messages, sent %d", len(got.Client),
			len(f.messages))
	}
}

// TestServeEOF: input that ends without exit ends the session with
// exit code 1, and an interrupted context ends it at once.
func TestServeEOF(t *testing.T) {
	sim := simenv.New(simenv.Options{Dir: workDir,
		Stdin: [][]byte{jsonrpc2.AppendFrame(nil, []byte(
			`{"jsonrpc":"2.0","id":1,"method":"initialize",`+
				`"params":{"capabilities":{}}}`))}})
	if code := server.Serve(context.Background(), sim.Env(),
		server.Options{}, 1, nil); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !bytes.Contains(sim.Stdout.Bytes(), []byte(`"id":1,"result"`)) {
		t.Fatalf("no initialize result in %s", sim.Stdout.Bytes())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, w := io.Pipe() // never written: Serve must not wait for it
	defer w.Close()
	e := simenv.New(simenv.Options{}).Env()
	e.Transport.Stdin = r
	if code := server.Serve(ctx, e, server.Options{}, 1, nil); code != 0 {
		t.Fatalf("cancelled: exit %d", code)
	}
}

// TestRunAction: `gohard lsp <action>` runs the server's code path.
func TestRunAction(t *testing.T) {
	text := []byte("-- é😀 x\nentity top is end;\n")
	sim := simenv.New(simenv.Options{Dir: workDir,
		Files: map[string][]byte{"/work/top.vhd": text}})
	a, ok := server.LookupAction("hover")
	if !ok {
		t.Fatal("no hover action")
	}
	resp, err := server.RunAction(sim.Env(), server.Options{
		Analyzer: echo{}}, 1, workDir, a, "/work/top.vhd", text,
		server.Position{Line: 0, Character: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"contents":{"kind":"markdown","value":"\"x\" at byte 10"},` +
		`"range":{"start":{"line":0,"character":10},` +
		`"end":{"line":0,"character":11}}}`
	if string(resp.Result) != want {
		t.Fatalf("got %s", resp.Result)
	}
	_, err = server.RunAction(sim.Env(), server.Options{}, 1, workDir, a,
		"/work/top.vhd", text, server.Position{}, nil)
	if !errors.Is(err, server.ErrNotProvided) {
		t.Fatalf("Nop: %v", err)
	}
	r, _ := server.LookupAction("rename")
	if _, err := server.RunAction(sim.Env(), server.Options{}, 1,
		workDir, r, "/work/top.vhd", text, server.Position{},
		nil); err == nil {
		t.Fatal("rename without a new name was accepted")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering hover twice did not panic")
		}
	}()
	server.RegisterAction(a)
}

// TestNonObjectBodies: a body that is valid JSON but not a JSON-RPC
// call or response (a batch, a scalar, an empty object) gets an
// InvalidRequest error with a null ID, as JSON-RPC 2.0 requires; a
// malformed response gets nothing.
func TestNonObjectBodies(t *testing.T) {
	cases := []struct {
		body string
		code string // "" for no answer
	}{
		{`[]`, "-32600"},
		{`[{"jsonrpc":"2.0","id":1,"method":"initialize"}]`, "-32600"},
		{`42`, "-32600"},
		{`null`, "-32600"},
		{`{}`, "-32600"},
		{`{not json`, "-32700"},
		{"{\"jsonrpc\":\"2.0\",\"method\":\"x\",\"params\":" +
			"{\"s\":\"\xff\"}}", "-32700"},
		{`{"jsonrpc":"2.0","id":7}`, ""},
		{`{"jsonrpc":"2.0","result":1}`, ""},
	}
	for _, c := range cases {
		var out []string
		d := server.NewDriver(server.Options{}, simenv.New(
			simenv.Options{Dir: workDir}).Env(), 1,
			func(s server.Send) error {
				out = append(out, string(s.Body()))
				return nil
			})
		if err := d.Deliver([]byte(c.body)); err != nil {
			t.Fatal(err)
		}
		switch {
		case c.code == "" && len(out) != 0:
			t.Errorf("%q: answered %q", c.body, out)
		case c.code != "" && (len(out) != 1 || !bytes.Contains(
			[]byte(out[0]), []byte(`"id":null,"error":{"code":`+
				c.code))):
			t.Errorf("%q: got %q, want one %s error with a null ID",
				c.body, out, c.code)
		}
	}
}

// spin is an analyzer whose hover reports progress for spinSlices
// slices before it answers "done", so only a cancel or an edit that
// reaches it while it runs can end it early.
type spin struct{ server.Nop }

const spinSlices = 200_000

func (spin) Features() server.Features {
	return server.Features{Set: server.FeatureHover}
}

func (spin) Hover(server.Query) server.Task[*server.Hover] {
	return spinTask(spinSlices)
}

type hoverSlice = server.Slice[*server.Hover]

func spinTask(n int) server.Task[*server.Hover] {
	return server.TaskFunc[*server.Hover](func(int) hoverSlice {
		if n <= 1 {
			return hoverSlice{Done: true,
				Value: &server.Hover{Markdown: "done"}}
		}
		return hoverSlice{Next: spinTask(n - 1),
			Report: &server.Report{Message: "spinning",
				Percentage: -1}}
	})
}

// TestServeCancelsRunningWork is the #25 invariant through the jsonrpc2
// reader and writer goroutines: a
// $/cancelRequest that arrives while a request runs ends it with
// RequestCancelled at the next slice boundary, an edit that arrives
// while a request runs ends it with ContentModified, every request
// gets exactly one response, and the recorded session (with the step
// each input arrived at) replays to the same bytes.
func TestServeCancelsRunningWork(t *testing.T) {
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"rootUri":"file:///work","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":` +
			`{"textDocument":{"uri":"file:///work/a.vhd",` +
			`"languageId":"vhdl","version":1,"text":"entity a"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover",` +
			`"params":{"textDocument":{"uri":"file:///work/a.vhd"},` +
			`"position":{"line":0,"character":1}}}`,
		`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":2}}`,
		`{"jsonrpc":"2.0","id":3,"method":"textDocument/hover",` +
			`"params":{"textDocument":{"uri":"file:///work/a.vhd"},` +
			`"position":{"line":0,"character":1}}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":` +
			`{"textDocument":{"uri":"file:///work/a.vhd","version":2},` +
			`"contentChanges":[{"range":{"start":{"line":0,` +
			`"character":8},"end":{"line":0,"character":8}},` +
			`"text":" is"}]}}`,
		`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	// The client sends everything up to the edit at once, and
	// shutdown and exit only after it saw the answer to request 3, so
	// the edit reaches request 3 while it runs.
	var first, rest []byte
	for i, m := range msgs {
		if i < 7 {
			first = jsonrpc2.AppendFrame(first, []byte(m))
		} else {
			rest = jsonrpc2.AppendFrame(rest, []byte(m))
		}
	}
	gate := make(chan struct{})
	out := &gatedWriter{trigger: []byte(`"id":3,`), gate: gate}
	sim := simenv.New(simenv.Options{Dir: workDir})
	e := sim.Env()
	e.Transport.Stdin = &gatedReader{parts: [][]byte{first, rest},
		gate: gate}
	e.Transport.Stdout = out
	var lines []json.RawMessage
	busy := 0
	opts := server.Options{Analyzer: spin{}, Version: testVersion}
	code := server.Serve(context.Background(), e, opts, 1,
		func(a server.Arrival) error {
			if a.Busy {
				busy++
			}
			if l := a.Line(); l != nil {
				lines = append(lines, l)
			}
			return nil
		})
	live := out.buf.Bytes()
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	bodies, err := server.Frames(live)
	if err != nil {
		t.Fatal(err)
	}
	answers := map[string][]string{}
	for _, b := range bodies {
		m, err := jsonrpc2.Decode(b)
		if r, ok := m.(*jsonrpc2.Response); err == nil && ok {
			answers[r.ID.String()] = append(answers[r.ID.String()],
				string(b))
		}
	}
	want := map[string]string{
		"2": `"code":-32800`, "3": `"code":-32801`, "4": `"result":null`,
	}
	for id, frag := range want {
		if len(answers[id]) != 1 ||
			!strings.Contains(answers[id][0], frag) {
			t.Errorf("request %s: answers %q, want one with %s", id,
				answers[id], frag)
		}
	}
	if busy < 2 {
		t.Errorf("only %d inputs arrived while work ran", busy)
	}
	var client []json.RawMessage
	for _, m := range msgs {
		client = append(client, json.RawMessage(m))
	}
	for _, p := range checkSession(client, bodies) {
		t.Errorf("invariant: %s", p)
	}
	h, err := server.SessionHeader(sim.Env(), workDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	sess := repro.Session{Header: h, Messages: lines}
	file, err := sess.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := repro.ParseSession(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.Replay(back, simenv.New(simenv.Options{
		Dir: workDir}).Env().FS, opts, "live")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(live, got.Output) {
		t.Fatalf("live output differs from its replay:\n%s\n---\n%s",
			live, got.Output)
	}
}

// gatedReader returns parts[0], then waits until gate is closed before
// it returns the other parts, then io.EOF. jsonrpc2's reader goroutine
// is the only caller.
type gatedReader struct {
	parts [][]byte
	gate  <-chan struct{}
	n     int
}

func (r *gatedReader) Read(p []byte) (int, error) {
	for len(r.parts) > 0 && len(r.parts[0]) == 0 {
		r.parts = r.parts[1:]
		r.n++
	}
	if len(r.parts) == 0 {
		return 0, io.EOF
	}
	if r.n > 0 {
		<-r.gate
	}
	k := copy(p, r.parts[0])
	r.parts[0] = r.parts[0][k:]
	return k, nil
}

// gatedWriter collects the server output and closes gate once a frame
// contains trigger. jsonrpc2's writer goroutine is the only caller;
// Serve returns after it finished.
type gatedWriter struct {
	buf     bytes.Buffer
	trigger []byte
	gate    chan struct{}
	once    sync.Once
	// gates, when set instead of gate, are closed one by one by the
	// frames that contain trigger.
	gates []chan struct{}
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, w.trigger) {
		if w.gate != nil {
			w.once.Do(func() { close(w.gate) })
		} else if len(w.gates) > 0 {
			close(w.gates[0])
			w.gates = w.gates[1:]
		}
	}
	return w.buf.Write(p)
}

// stagedReader returns parts[0], then parts[i] once gates[i-1] is
// closed (running before[i] first), then io.EOF. jsonrpc2's reader
// goroutine is the only caller.
type stagedReader struct {
	parts  [][]byte
	gates  []chan struct{}
	before map[int]func()
	i      int
}

func (r *stagedReader) Read(p []byte) (int, error) {
	for r.i < len(r.parts) && len(r.parts[r.i]) == 0 {
		r.i++
		if r.i < len(r.parts) {
			<-r.gates[r.i-1]
			if f := r.before[r.i]; f != nil {
				f()
			}
		}
	}
	if r.i >= len(r.parts) {
		return 0, io.EOF
	}
	k := copy(p, r.parts[r.i])
	r.parts[r.i] = r.parts[r.i][k:]
	return k, nil
}

// TestServeRecordsConfigReload: a session that rewrites gohard.yaml
// and saves it replays byte for byte (#29): every gohard.yaml read is
// in the recording, so the replay needs neither the file as it was at
// the start nor as it was left, and the reload's diagnostics match.
func TestServeRecordsConfigReload(t *testing.T) {
	const yaml = "/work/gohard.yaml"
	old := []byte("standard: 2008\n") // config/unquoted-standard
	edited := []byte("standard: \"2008\"\n")
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"rootUri":"file:///work","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didSave","params":` +
			`{"textDocument":{"uri":"file:///work/gohard.yaml"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	// The client sends initialize and initialized; once the first
	// load was published it edits gohard.yaml and saves it; once the
	// reload was published it shuts down.
	parts := make([][]byte, 3)
	for i, m := range msgs {
		k := []int{0, 0, 1, 2, 2}[i]
		parts[k] = jsonrpc2.AppendFrame(parts[k], []byte(m))
	}
	files := map[string][]byte{yaml: old,
		"/work/top.vhd": []byte("entity top is end;\n")}
	sim := simenv.New(simenv.Options{Dir: workDir, Files: files})
	e := sim.Env()
	// The header is built when the session starts, before the edit.
	h, err := server.SessionHeader(e, workDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	out := &gatedWriter{gates: gates, trigger: []byte(
		`"uri":"file:///work/gohard.yaml","diagnostics"`)}
	e.Transport.Stdin = &stagedReader{parts: parts, gates: gates,
		before: map[int]func(){1: func() {
			if err := sim.FS.WriteFile(yaml, edited, 0o644); err != nil {
				panic(err)
			}
		}}}
	e.Transport.Stdout = out
	var lines []json.RawMessage
	opts := server.Options{Version: testVersion}
	code := server.Serve(context.Background(), e, opts, 1,
		func(a server.Arrival) error {
			if l := a.Line(); l != nil {
				lines = append(lines, l)
			}
			return nil
		})
	live := out.buf.Bytes()
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := bytes.Count(live, out.trigger); n != 2 {
		t.Fatalf("live: %d gohard.yaml publishes, want 2:"+
			"\n%s", n, live)
	}
	if n := bytes.Count(live, []byte("unquoted-standard")); n != 1 {
		t.Fatalf("live: %d unquoted-standard diagnostics, want 1 "+
			"(the first load only):\n%s", n, live)
	}
	sess := repro.Session{Header: h, Messages: lines}
	replay := func(yamlData []byte, s repro.Session) server.Replayed {
		t.Helper()
		fs := map[string][]byte{yaml: yamlData,
			"/work/top.vhd": files["/work/top.vhd"]}
		got, err := server.Replay(s, simenv.New(simenv.Options{
			Dir: "/elsewhere", Files: fs}).Env().FS, opts, "live")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for name, data := range map[string][]byte{"restored": old,
		"left edited": edited} {
		got := replay(data, sess)
		if len(got.Diagnostics) > 0 {
			t.Fatalf("%s: %v", name, got.Diagnostics)
		}
		if !bytes.Equal(live, got.Output) {
			t.Fatalf("%s: live output differs from its replay:\n%s\n"+
				"---\n%s", name, live, got.Output)
		}
	}
	// Without the config entries (the old format) the replay cannot
	// know the edit: the test would catch a recorder that drops them.
	var stripped []json.RawMessage
	for _, l := range lines {
		if !bytes.HasPrefix(l, []byte(`{"$gohard":"config"`)) {
			stripped = append(stripped, l)
		}
	}
	if len(stripped) == len(lines) {
		t.Fatal("no gohard.yaml read was recorded")
	}
	sess.Messages = stripped
	if got := replay(old, sess); bytes.Equal(live, got.Output) {
		t.Fatal("a replay without the recorded reads matched")
	}
}

// TestServeRejectsSkippedRequest: a request the transport skips (a
// charset other than UTF-8) still gets exactly one error response
// with its ID, the skip is logged on stderr, and the session replays
// byte for byte (#25, #29).
func TestServeRejectsSkippedRequest(t *testing.T) {
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"capabilities":{}}}`
	skipped := `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`
	var in []byte
	in = jsonrpc2.AppendFrame(in, []byte(init))
	in = append(in, "Content-Length: "+strconv.Itoa(len(skipped))+
		"\r\nContent-Type: application/vscode-jsonrpc; "+
		"charset=latin1\r\n\r\n"+skipped...)
	in = jsonrpc2.AppendFrame(in, []byte(
		`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`))
	in = jsonrpc2.AppendFrame(in, []byte(
		`{"jsonrpc":"2.0","method":"exit"}`))
	sim := simenv.New(simenv.Options{Dir: workDir, Stdin: [][]byte{in}})
	var lines []json.RawMessage
	opts := server.Options{Version: testVersion}
	code := server.Serve(context.Background(), sim.Env(), opts, 1,
		func(a server.Arrival) error {
			if l := a.Line(); l != nil {
				lines = append(lines, l)
			}
			return nil
		})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	live := sim.Stdout.Bytes()
	bodies, err := server.Frames(live)
	if err != nil {
		t.Fatal(err)
	}
	var two []string
	for _, b := range bodies {
		m, err := jsonrpc2.Decode(b)
		if r, ok := m.(*jsonrpc2.Response); err == nil && ok &&
			r.ID == jsonrpc2.IntID(2) {
			two = append(two, string(b))
		}
	}
	if len(two) != 1 || !strings.Contains(two[0], `"code":-32600`) {
		t.Fatalf("request 2 got %q, want one InvalidRequest", two)
	}
	if !strings.Contains(sim.Stderr.String(),
		`gohard: lsp: jsonrpc2: skipped a`) {
		t.Fatalf("stderr %q", sim.Stderr.String())
	}
	h, err := server.SessionHeader(sim.Env(), workDir, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.Replay(repro.Session{Header: h, Messages: lines},
		simenv.New(simenv.Options{Dir: workDir}).Env().FS, opts, "live")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(live, got.Output) {
		t.Fatalf("live output differs from its replay:\n%s\n---\n%s",
			live, got.Output)
	}
}
