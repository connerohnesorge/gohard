package server

import (
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/span"
)

// Tick is the virtual time one Step takes. The core never reads a
// clock: its time is the number of steps it ran, plus the due time of
// events pushed for later, so every timing rule is a step bound (P7).
const Tick = time.Millisecond

// NoProgress is the liveness bound of #25: work that runs this long in
// virtual time (NoProgress/Tick steps) without finishing or reporting
// progress is stopped, with RequestFailed for a request.
const NoProgress = 5 * time.Second

// SliceBudget is the step budget a task gets per slice.
const SliceBudget = 10_000

// Options configure a server.
type Options struct {
	// Analyzer computes the features; nil means Nop.
	Analyzer Analyzer
	// Version is reported in serverInfo.
	Version string
	// Commit and Go are the commit and Go toolchain gohard was built
	// from, recorded in the repro headers of editor runs (#39); "" is
	// recorded as unknown.
	Commit, Go string
	// Simulator, when set, runs testbenches in the editor: code
	// lenses, the Open waveform code action and the gohard.* commands
	// (sim.go, #25, #35, #36).
	Simulator Simulator
}

type phase uint8

const (
	phaseNew         phase = iota // before initialize
	phaseInitialized              // initialize answered
	phaseRunning                  // initialized received
	phaseShutdown                 // shutdown answered
	phaseExited
)

// State is the whole state of the server core. It is a value: Step
// takes a State and returns a new one, and never changes anything the
// old State can see, so a State can be kept, compared or forked.
type State struct {
	opts  Options
	an    Analyzer
	now   time.Duration
	queue Queue
	phase phase

	enc        span.Encoding
	progressOK bool // window.workDoneProgress
	// watchReg: the client registers file watchers dynamically.
	watchReg bool
	// createFiles: the client applies CreateFile resource operations
	// (workspace.workspaceEdit.resourceOperations has "create"), so
	// code actions may create files (#57).
	createFiles bool
	folder      string
	root        string

	file     *config.Loaded // gohard.yaml, once loaded
	editor   *config.Config // editor settings, if the client sent any
	cfg      config.Config  // gohard.yaml over the editor settings
	cfgDiags bool           // gohard.yaml diagnostics are published
	loaded   bool           // the workspace files were read
	// rereading is set from a watched-file change until the reread
	// lands: requests arriving meanwhile wait for the new snapshot.
	rereading bool
	discovery []diag.Diagnostic

	open map[string]*Document // by docKey; copy on write
	disk []*Document
	snap *Snapshot
	ids  uint64 // last snapshot ID

	work      map[uint64]work   // copy on write
	byRequest map[string]uint64 // idKey -> work ID; copy on write
	nextWork  uint64
	diagWork  uint64   // the running diagnostics work, or 0
	published []string // URIs whose last publish was non-empty
	nextOut   int64    // last server-to-client request ID
	creating  map[int64]uint64
	edits     uint64 // count of document changes

	// Simulation (sim.go): the last run of each testbench (copy on
	// write), the analysis diagnostics last published, the save count
	// of the auto_run_on_save debounce, and whether the client takes
	// workspace/codeLens/refresh.
	runs        map[string]simRun
	anDiags     []FileDiagnostics
	saves       uint64
	lensRefresh bool
	// hintRefresh: the client takes workspace/inlayHint/refresh (#56).
	hintRefresh bool

	// tokens is the last full or delta semantic tokens result of each
	// document (by docKey; copy on write), the base of the next delta
	// request; nextTokens numbers the result IDs (semtokens.go).
	tokens     map[string]storedTokens
	nextTokens uint64
}

// New returns the state of a server that has not been initialized.
func New(o Options) State {
	an := o.Analyzer
	if an == nil {
		an = Nop{}
	}
	s := State{opts: o, an: an, enc: span.UTF16, cfg: config.Defaults()}
	s.snap = newSnapshot(0, "", s.cfg, nil, nil)
	return s
}

// Now returns the virtual time of s.
func (s State) Now() time.Duration { return s.now }

// Snapshot returns the latest snapshot.
func (s State) Snapshot() *Snapshot { return s.snap }

// Encoding returns the negotiated position encoding.
func (s State) Encoding() span.Encoding { return s.enc }

// Exited reports whether the exit notification (or the end of the
// client stream) ended the session.
func (s State) Exited() bool { return s.phase == phaseExited }

// Pending returns the number of queued events.
func (s State) Pending() int { return s.queue.Len() }

// Busy reports whether work (a request or diagnostics) is running.
func (s State) Busy() bool { return len(s.work) > 0 }

// Push queues b at the current virtual time with priority pri.
func (s State) Push(b Body, pri Priority) State {
	return s.PushAt(s.now, b, pri)
}

// PushAt queues b at virtual time at.
func (s State) PushAt(at time.Duration, b Body, pri Priority) State {
	s.queue = s.queue.Push(Event{At: at, Pri: pri, Body: b})
	return s
}

// Next removes the first queued event.
func (s State) Next() (State, Event, bool) {
	q, ev, ok := s.queue.Pop()
	s.queue = q
	return s, ev, ok
}

// Step runs one event and returns the new state and the effects the
// shell must perform, in order (P3). It never panics on any client
// input and never reads the world.
func Step(s State, ev Event) (State, []Effect) {
	if ev.At > s.now {
		s.now = ev.At
	}
	st := &s
	var o out
	o.now = s.now
	switch b := ev.Body.(type) {
	case Message:
		st.message(&o, b.Data)
	case Rejected:
		st.rejected(&o, b)
	case Closed:
		st.exit(&o)
	case ConfigLoaded:
		st.configLoaded(&o, b.Loaded)
	case WorkspaceLoaded:
		st.workspaceLoaded(&o, b)
	case Work:
		st.slice(&o, b.ID)
	case AutoRun:
		st.autoRun(b.Gen)
	case WaveformOpened:
		st.waveformOpened(&o, b)
	case RunWritten:
		if b.Err != "" && st.phase != phaseExited {
			st.log(&o, LogError, "simulation: "+b.Err)
		}
	}
	s.now += Tick
	return s, o.fx
}

// out collects the effects of one step.
type out struct {
	fx  []Effect
	now time.Duration
}

func (o *out) send(m jsonrpc2.Message) { o.fx = append(o.fx, Send{m}) }

func (o *out) do(e Effect) { o.fx = append(o.fx, e) }

func (o *out) notify(method string, params any) {
	o.send(&jsonrpc2.Notification{Method: method,
		Params: marshal(params)})
}

// respond sends a result (any JSON value; nil is null) for id, then
// the snapshot notification.
func (s *State) respond(o *out, id jsonrpc2.ID, result any) {
	o.send(&jsonrpc2.Response{ID: id, Result: marshal(result)})
	s.snapshotNote(o, id)
}

// fail sends an error response for id, then the snapshot
// notification.
func (s *State) fail(o *out, id jsonrpc2.ID, code int64, msg string) {
	o.send(&jsonrpc2.Response{ID: id,
		Error: &jsonrpc2.Error{Code: code, Message: msg}})
	s.snapshotNote(o, id)
}

func (s *State) snapshotNote(o *out, id jsonrpc2.ID) {
	// Before initialize is answered only a few notifications may be
	// sent (LSP 3.17), so early errors carry no snapshot notification.
	if s.phase == phaseNew {
		return
	}
	o.notify(SnapshotMethod, struct {
		Snapshot uint64      `json:"snapshot"`
		ID       jsonrpc2.ID `json:"id"`
	}{s.snap.ID, id})
}

// MessageType values of window/logMessage.
const (
	LogError   = 1
	LogWarning = 2
	LogInfo    = 3
	LogLog     = 4
)

func (s *State) log(o *out, typ int, msg string) {
	o.notify("window/logMessage", struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}{typ, msg})
}

// message decodes and dispatches one client message. A body that is
// not JSON-RPC gets the error response JSON-RPC asks for (none for a
// malformed response).
func (s *State) message(o *out, data json.RawMessage) {
	msg, err := jsonrpc2.Decode(data)
	if err != nil {
		var de *jsonrpc2.DecodeError
		if errors.As(err, &de) && de.Reply() &&
			s.phase != phaseExited {
			o.send(de.Response())
			s.snapshotNote(o, de.ID)
		}
		return
	}
	switch m := msg.(type) {
	case *jsonrpc2.Response:
		s.clientResponse(o, m)
	case *jsonrpc2.Request:
		s.request(o, call{id: m.ID, isReq: true, method: m.Method,
			params: m.Params})
	case *jsonrpc2.Notification:
		s.notification(o, call{method: m.Method, params: m.Params})
	}
}

// rejected answers a request the transport skipped unread.
func (s *State) rejected(o *out, r Rejected) {
	if s.phase == phaseExited {
		return
	}
	s.fail(o, r.ID, jsonrpc2.CodeInvalidRequest,
		"the message was not read: "+r.Reason)
}

func (s *State) request(o *out, m call) {
	switch {
	case s.phase == phaseExited:
		return
	case m.method == "initialize":
		s.initialize(o, m)
		return
	case s.phase == phaseNew:
		s.fail(o, m.id, jsonrpc2.CodeServerNotInitialized,
			"the server is not initialized")
		return
	case s.phase == phaseShutdown:
		s.fail(o, m.id, jsonrpc2.CodeInvalidRequest,
			"the server is shutting down")
		return
	case m.method == "shutdown":
		s.shutdown(o, m)
		return
	}
	if _, dup := s.byRequest[m.id.String()]; dup {
		s.fail(o, m.id, jsonrpc2.CodeInvalidRequest,
			"request ID "+m.id.String()+" is already in use")
		return
	}
	s.feature(o, m)
}

func (s *State) notification(o *out, m call) {
	if m.method == "exit" {
		s.exit(o)
		return
	}
	if s.phase == phaseNew || s.phase == phaseExited {
		return // LSP: drop notifications before initialize
	}
	switch m.method {
	case "initialized":
		s.initialized(o)
	case "$/cancelRequest":
		s.cancel(o, m.params)
	case "textDocument/didOpen":
		s.didOpen(o, m.params)
	case "textDocument/didChange":
		s.didChange(o, m.params)
	case "textDocument/didClose":
		s.didClose(o, m.params)
	case "textDocument/didSave":
		s.didSave(o, m.params)
	case "workspace/didChangeConfiguration":
		s.didChangeConfiguration(o, m.params)
	case "workspace/didChangeWatchedFiles":
		s.didChangeWatchedFiles(o, m.params)
	}
}

// clientResponse handles the answer to a server-to-client request.
func (s *State) clientResponse(o *out, m *jsonrpc2.Response) {
	id, ok := m.ID.Int()
	if !ok {
		return
	}
	wid, ok := s.creating[id]
	if !ok {
		return
	}
	s.creating = cloneDelete(s.creating, id)
	w, ok := s.work[wid]
	if !ok || m.Error != nil {
		return
	}
	w.tokenReady = true
	s.beginProgress(o, &w)
	s.setWork(w)
}

// newSnapshot makes the next snapshot from the current documents and
// configuration, and restarts diagnostics for it.
func (s *State) newSnapshot(o *out) {
	s.ids++
	s.snap = newSnapshot(s.ids, s.root, s.cfg, s.open, s.disk)
	s.snap.Discovery = s.discovery
	if s.file != nil && s.root != "" {
		name := filepath.Join(s.root, config.FileName)
		d := newDocument(PathToURI(name), s.root, s.file.Source)
		s.snap.config, s.snap.configPresent = d, s.file.Present
	}
	s.startDiagnostics(o)
}

func (s *State) exit(o *out) {
	if s.phase == phaseExited {
		return
	}
	code := 1
	if s.phase == phaseShutdown {
		code = 0
	}
	// Exit without shutdown: every request still running or parked
	// gets its one response before the session ends (#25).
	s.cancelRequests(o, "the server is exiting")
	s.phase = phaseExited
	s.work, s.byRequest, s.diagWork = nil, nil, 0
	s.queue = Queue{}
	o.do(Exit{Code: code})
}

func cloneSet[K comparable, V any](m map[K]V, k K, v V) map[K]V {
	m = maps.Clone(m)
	if m == nil {
		m = map[K]V{}
	}
	m[k] = v
	return m
}

func cloneDelete[K comparable, V any](m map[K]V, k K) map[K]V {
	m = maps.Clone(m)
	delete(m, k)
	return m
}

func sortedKeys[K interface {
	~uint64 | ~int64 | ~string
}, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}
