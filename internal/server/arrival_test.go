package server_test

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"sync"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// TestServeArrivedInputBeforeStep is the regression test for a flaky
// TestLSPRecordReplay (P1): the client has sent its whole session, but
// jsonrpc2's reader goroutine is slow to hand over the second frame.
// The core must not run a step meanwhile: input that has arrived
// reaches the core before it steps, so the output is the same as when
// the reader is fast (here: gohard.yaml is loaded after every message
// is queued, so no logMessage about the editor's format.indent), and
// every input is recorded at step 0.
//
// The slow reader is forced: while it holds the second frame it yields
// until the server writes anything, which only a core that stepped
// early does, so without the fix the frames always arrive late.
func TestServeArrivedInputBeforeStep(t *testing.T) {
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"rootUri":"file:///work","capabilities":{},` +
			`"initializationOptions":{"format":{"indent":2}}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":` +
			`{"textDocument":{"uri":"file:///work/a.vhd",` +
			`"languageId":"vhdl","version":1,"text":"entity a is end;"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	var first, rest []byte
	for i, m := range msgs {
		if i == 0 {
			first = jsonrpc2.AppendFrame(first, []byte(m))
		} else {
			rest = jsonrpc2.AppendFrame(rest, []byte(m))
		}
	}
	files := map[string][]byte{
		workDir + "/gohard.yaml": []byte("format:\n  indent: 4\n"),
	}
	opts := server.Options{Version: testVersion}
	run := func(in io.Reader, out io.Writer, buf *bytes.Buffer) ([]server.Arrival, []byte) {
		sim := simenv.New(simenv.Options{Dir: workDir, Files: files})
		e := sim.Env()
		e.Transport.Stdin, e.Transport.Stdout = in, out
		var arrivals []server.Arrival
		code := server.Serve(context.Background(), e, opts, 1,
			func(a server.Arrival) error {
				arrivals = append(arrivals, a)
				return nil
			})
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		return arrivals, buf.Bytes()
	}
	var fast bytes.Buffer
	_, want := run(simenv.NewScript(first, rest), &fast, &fast)
	if bytes.Contains(want, []byte("window/logMessage")) {
		t.Fatalf("fast reader: gohard.yaml loaded before the "+
			"messages were queued:\n%s", want)
	}
	for range 5 {
		out := &signalWriter{wrote: make(chan struct{})}
		in := &slowReader{parts: [][]byte{first, rest},
			wrote: out.wrote}
		arrivals, got := run(in, out, &out.buf)
		for _, a := range arrivals {
			if a.Config == nil && a.Step != 0 {
				t.Fatalf("input %q arrived after %d steps: the core "+
					"stepped before the reader delivered input that "+
					"had arrived", a.Body, a.Step)
			}
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("slow reader changed the output:\n%s\n---\n%s",
				got, want)
		}
	}
}

// slowReader is an input whose bytes have all arrived (Ready), read
// by a slow goroutine: parts[0] comes at once; each later part only
// after the server wrote something or after many yields. jsonrpc2's
// reader goroutine is the only caller.
type slowReader struct {
	parts [][]byte
	wrote <-chan struct{}
	n     int
}

func (r *slowReader) Ready() bool { return true }

func (r *slowReader) Read(p []byte) (int, error) {
	for len(r.parts) > 0 && len(r.parts[0]) == 0 {
		r.parts = r.parts[1:]
		r.n++
	}
	if len(r.parts) == 0 {
		return 0, io.EOF
	}
	if r.n > 0 {
	wait:
		for range 100_000 {
			select {
			case <-r.wrote:
				break wait
			default:
				runtime.Gosched()
			}
		}
	}
	k := copy(p, r.parts[0])
	r.parts[0] = r.parts[0][k:]
	return k, nil
}

// signalWriter collects the server output and closes wrote at the
// first write. jsonrpc2's writer goroutine is the only caller; Serve
// returns after it finished.
type signalWriter struct {
	buf   bytes.Buffer
	wrote chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.wrote) })
	return w.buf.Write(p)
}
