package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/ide"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// event is one analysis phase start seen by the probe; for
// "cross-file" ovl is the time spent in overload resolution during
// the "units" phase just finished (summed over the job workers).
type event struct {
	at   time.Time
	name string
	ovl  time.Duration
}

// recorder is the sema/ide probe of the benchmark: it timestamps every
// phase start and sums the time in overload resolution.
type recorder struct {
	mu  sync.Mutex
	ev  []event
	ovl atomic.Int64 // ns in overload resolution since "units"
}

func (r *recorder) probe() *sema.Probe {
	return &sema.Probe{
		Phase: func(name string) {
			now := time.Now()
			e := event{at: now, name: name}
			switch name {
			case "units":
				r.ovl.Store(0)
			case "cross-file":
				e.ovl = time.Duration(r.ovl.Load())
			}
			r.mu.Lock()
			r.ev = append(r.ev, e)
			r.mu.Unlock()
		},
		Overload: func() func() {
			t := time.Now()
			return func() { r.ovl.Add(int64(time.Since(t))) }
		},
	}
}

func (r *recorder) events() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event{}, r.ev...)
}

// msg is one server message and the time the client read it.
type msg struct {
	at time.Time
	m  map[string]json.RawMessage
}

// client is a scripted LSP client of an in-process `gohard lsp` (the
// same analyzer, formatting wrapper, simulator and analysis cache as
// cli's newAnalyzer) over pipes, with the probe attached to the
// analyzer.
type client struct {
	in    *io.PipeWriter
	msgs  chan msg
	errs  chan error
	rec   *recorder
	next  int
	snap  uint64   // the largest $/gohard/snapshot ID seen
	snapU []string // URIs of the notifications of snap
	// created are the progress tokens the server created, in order.
	created []string
	stop    context.CancelFunc
}

func start(dir string) *client {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	e := realenv.New(realenv.Options{Dir: dir, Stdin: c2sR,
		Stdout: s2cW, Stderr: io.Discard})
	c := &client{in: c2sW, msgs: make(chan msg, 1<<16),
		errs: make(chan error, 1), rec: &recorder{}}
	o := ide.Options{ToolVersion: cli.Version, Jobs: e.Jobs,
		Probe: c.rec.probe()}
	// The on-disk analysis cache of the project (#37), as gohard lsp
	// uses it; GOHARD_BENCH_NOCACHE=1 measures without it.
	if os.Getenv("GOHARD_BENCH_NOCACHE") == "" {
		o.Cache = cli.CacheOpener(e.FS)
	}
	an := ide.New(o)
	opts := server.Options{Analyzer: server.WithFormatting(an,
		cli.Version), Simulator: an, Version: cli.Version}
	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	go func() {
		server.Serve(ctx, e, opts, 0, nil)
		s2cW.Close()
	}()
	go c.readLoop(bufio.NewReaderSize(s2cR, 1<<20))
	return c
}

func (c *client) readLoop(r *bufio.Reader) {
	for {
		m, err := readFrame(r)
		if err != nil {
			c.errs <- err
			return
		}
		c.msgs <- msg{at: time.Now(), m: m}
	}
}

func readFrame(r *bufio.Reader) (map[string]json.RawMessage, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			if n, err = strconv.Atoi(v); err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("no Content-Length header")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	return m, json.Unmarshal(body, &m)
}

// debug logs every message on stderr (BENCH_DEBUG=1).
var debug = os.Getenv("BENCH_DEBUG") != ""

func (c *client) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "%s -> %.300s\n",
			time.Now().Format("15:04:05.000"), b)
	}
	fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (c *client) notify(method string, params any) {
	c.write(map[string]any{"jsonrpc": "2.0", "method": method,
		"params": params})
}

func method(m map[string]json.RawMessage) string {
	var s string
	_ = json.Unmarshal(m["method"], &s)
	return s
}

// observe answers server requests (null configuration items, accepted
// progress tokens) and tracks snapshot IDs.
func (c *client) observe(m map[string]json.RawMessage) {
	switch meth := method(m); {
	case meth != "" && m["id"] != nil:
		var res any
		var p struct {
			Items []any
			Token json.RawMessage
		}
		_ = json.Unmarshal(m["params"], &p)
		switch meth {
		case "workspace/configuration":
			res = make([]any, len(p.Items))
		case "window/workDoneProgress/create":
			c.created = append(c.created, string(p.Token))
		}
		c.write(map[string]any{"jsonrpc": "2.0", "id": m["id"],
			"result": res})
	case meth == server.SnapshotMethod:
		var p struct {
			Snapshot uint64
			URI      string
		}
		_ = json.Unmarshal(m["params"], &p)
		if p.Snapshot > c.snap {
			c.snap, c.snapU = p.Snapshot, nil
		}
		if p.Snapshot == c.snap {
			c.snapU = append(c.snapU, p.URI)
		}
	}
}

// await returns the first message match accepts, observing every
// message on the way; it fails after timeout.
func (c *client) await(what string, timeout time.Duration,
	match func(map[string]json.RawMessage) bool,
) (msg, error) {
	deadline := time.After(timeout)
	for {
		select {
		case m := <-c.msgs:
			if debug {
				b, _ := json.Marshal(m.m)
				fmt.Fprintf(os.Stderr, "%s <- %.300s\n",
					m.at.Format("15:04:05.000"), b)
			}
			c.observe(m.m)
			if match(m.m) {
				return m, nil
			}
		case err := <-c.errs:
			return msg{}, fmt.Errorf("waiting for %s: %w", what, err)
		case <-deadline:
			return msg{}, fmt.Errorf("no %s within %s", what, timeout)
		}
	}
}

// request sends a request and returns its response.
func (c *client) request(meth string, params any) (msg, error) {
	c.next++
	id := strconv.Itoa(c.next)
	c.write(map[string]any{"jsonrpc": "2.0", "id": c.next,
		"method": meth, "params": params})
	m, err := c.await(meth+" response", time.Minute,
		func(m map[string]json.RawMessage) bool {
			return method(m) == "" && string(m["id"]) == id
		})
	if err == nil && m.m["error"] != nil {
		err = fmt.Errorf("%s: %s", meth, m.m["error"])
	}
	return m, err
}

// clientCaps are the capabilities the benchmark client announces:
// configuration, progress and UTF-16 positions, as Helix and Neovim.
var clientCaps = json.RawMessage(`{
 "workspace": {"configuration": true, "workspaceFolders": true},
 "textDocument": {"publishDiagnostics": {"versionSupport": true},
  "hover": {"contentFormat": ["markdown"]}},
 "window": {"workDoneProgress": true},
 "general": {"positionEncodings": ["utf-16"]}
}`)

func (c *client) initialize(rootURI string) error {
	_, err := c.request("initialize", map[string]any{
		"processId": nil, "rootUri": rootURI,
		"capabilities": clientCaps,
		"workspaceFolders": []any{map[string]string{
			"uri": rootURI, "name": "bench"}},
	})
	if err == nil {
		c.notify("initialized", map[string]any{})
	}
	return err
}

func (c *client) didOpen(uri string, text []byte) {
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri,
			"languageId": "vhdl", "version": 1, "text": string(text)}})
}

func (c *client) didChange(uri string, version int, e Edit) {
	c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{
			"range": map[string]any{
				"start": map[string]int{"line": e.Line,
					"character": e.Char},
				"end": map[string]int{"line": e.EndLine,
					"character": e.EndChar}},
			"text": e.Text}}})
}

// published matches the publishDiagnostics of uri at version, or
// without a version (the server clears a document's last diagnostics
// without one).
func published(uri string, version int,
) func(map[string]json.RawMessage) bool {
	return func(m map[string]json.RawMessage) bool {
		if method(m) != "textDocument/publishDiagnostics" {
			return false
		}
		var p struct {
			URI     string
			Version *int
		}
		_ = json.Unmarshal(m["params"], &p)
		return p.URI == uri && (p.Version == nil || *p.Version == version)
	}
}

// anyPublished matches any publishDiagnostics.
func anyPublished(m map[string]json.RawMessage) bool {
	return method(m) == "textDocument/publishDiagnostics"
}

// firstDiagnostics waits for the first published diagnostics. A
// project without any diagnostics publishes nothing; then the end of
// its first analysis counts (clean is true).
func (c *client) firstDiagnostics(timeout time.Duration) (time.Time,
	bool, error,
) {
	deadline := time.Now().Add(timeout)
	var analyzed time.Time
	for time.Now().Before(deadline) {
		m, err := c.await("first diagnostics", 50*time.Millisecond,
			anyPublished)
		if err == nil {
			return m.at, false, nil
		}
		if analyzed.IsZero() {
			for _, e := range c.rec.events() {
				if e.name == "analyzed" {
					analyzed = e.at
					break
				}
			}
		} else if time.Since(analyzed) > time.Second {
			return analyzed, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("no first diagnostics "+
		"within %s", timeout)
}
