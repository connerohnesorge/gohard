package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
	"github.com/connerohnesorge/gohard/internal/span"
)

func init() { lookup("lsp").Run = runLSP }

// newAnalyzer returns the analyzer of the language server and of every
// `gohard lsp <action>`. Later stages (#34, #35) replace the Nop;
// formatting (#32) wraps whatever analyzer they provide.
var newAnalyzer = func() server.Analyzer {
	return server.WithFormatting(server.Nop{}, Version)
}

func serverOptions() server.Options {
	return server.Options{Analyzer: newAnalyzer(), Version: Version}
}

// runLSP is `gohard lsp` (#25, #26): with no arguments the stdio
// server (optionally recording every session with --record DIR),
// `gohard lsp replay FILE`, or one action `gohard lsp ACTION --at
// PATH:LINE:COL`.
func runLSP(inv *Invocation) Result {
	if len(inv.Args) == 0 {
		return serveLSP(inv)
	}
	if inv.Args[0] == "replay" {
		return replayLSP(inv)
	}
	return lspAction(inv)
}

// serveLSP runs the stdio server. It streams: the server writes to
// stdout as it goes, so the Result carries only the exit code.
func serveLSP(inv *Invocation) Result {
	var rec *recording
	if inv.IsSet("record") {
		rec = &recording{inv: inv, dir: inv.String("record")}
	}
	var record func(server.Arrival) error
	if rec != nil {
		record = rec.record
	}
	code := server.Serve(inv.Ctx, inv.Env, serverOptions(), inv.Jobs,
		record)
	if rec != nil {
		if rec.close(); rec.err != nil {
			inv.Logf("--record: %s", rec.err)
		}
	}
	return Result{Exit: code}
}

// recording writes one session to DIR/<n>.jsonl (#29): the header,
// then one line per client input as it arrives (server.Arrival.Line),
// which keeps bodies that are not JSON, the core step each input
// reached it at, and every gohard.yaml the server read, so the session
// replays byte for byte. The header hashes the project of the session
// folder, which the first initialize request names, so the lines up to
// that request (anything a client sends before initialize) wait in
// memory until it arrives or the session ends.
type recording struct {
	inv     *Invocation
	dir     string
	w       io.WriteCloser
	r       *repro.Recorder
	err     error
	pending [][]byte // lines before the header is written
	bodies  [][]byte // their bodies, to find the session folder
}

func (r *recording) record(a server.Arrival) error {
	if r.err != nil {
		return r.err
	}
	line := a.Line()
	if line == nil {
		return nil
	}
	if r.r != nil {
		r.err = r.r.Record(line)
		return r.err
	}
	r.pending = append(r.pending, line)
	r.bodies = append(r.bodies, a.Body)
	if _, init := server.IsInitialize(a.Body); init ||
		a.Body == nil && a.Rejected == nil {
		r.err = r.flush()
	}
	return r.err
}

// flush writes the header and the pending lines.
func (r *recording) flush() error {
	if err := r.open(); err != nil {
		return err
	}
	for _, l := range r.pending {
		if err := r.r.Record(l); err != nil {
			return err
		}
	}
	r.pending, r.bodies = nil, nil
	return nil
}

func (r *recording) open() error {
	fsys := r.inv.Env.FS
	wd, err := fsys.Getwd()
	if err != nil {
		return err
	}
	folder := server.SessionFolder(r.bodies, wd)
	h, err := server.SessionHeader(r.inv.Env, folder, Version)
	if err != nil {
		return err
	}
	if err := fsys.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	n, err := repro.NextSession(fsys, r.dir)
	if err != nil {
		return err
	}
	w, err := fsys.Create(repro.SessionName(r.dir, n))
	if err != nil {
		return err
	}
	r.w = w
	r.r, err = repro.NewRecorder(w, h)
	return err
}

func (r *recording) close() {
	if r.r == nil && len(r.pending) > 0 && r.err == nil {
		r.err = r.flush()
	}
	if r.w != nil {
		if err := r.w.Close(); err != nil && r.err == nil {
			r.err = err
		}
	}
}

// replayLSP is `gohard lsp replay FILE`: it replays the session twice
// against the simulated Env and prints the server output; the two runs
// must agree byte for byte (lsp/replay-mismatch otherwise).
func replayLSP(inv *Invocation) Result {
	if len(inv.Args) != 2 {
		return inv.usage("gohard lsp replay needs one session file")
	}
	file := inv.Args[1]
	sess, err := repro.ReadSession(inv.Env.FS, file)
	if err != nil {
		return inv.Fail(repro.Invalid(filepath.ToSlash(file),
			errors.New(errReason(err))))
	}
	name := filepath.ToSlash(file)
	if an, _ := server.SessionAnalyzer(sess); an != "" {
		inv.Logf("note: %s names the test analyzer %s; replaying "+
			"with the built-in analyzer", name, strconv.Quote(an))
	}
	var runs [2]server.Replayed
	for i := range runs {
		runs[i], err = server.Replay(sess, inv.Env.FS, serverOptions(),
			name)
		if err != nil {
			return inv.Fail(repro.Invalid(name,
				errors.New(errReason(err))))
		}
	}
	r := inv.NewReport()
	r.Diagnostics = runs[0].Diagnostics
	if hasTool(r.Diagnostics) {
		return Result{Report: r}
	}
	for _, d := range r.Diagnostics {
		inv.Logf("%s[%s]: %s", d.Severity, d.Code, d.Message)
	}
	if n := server.Mismatch(runs[0], runs[1]); n > 0 {
		r.Diagnostics = append(r.Diagnostics, diag.New(
			diag.LSPReplayMismatch, strconv.Itoa(n)).At(name, 0, 0))
		return Result{Report: r}
	}
	return Result{Stdout: runs[0].Output}
}

// lspAction is `gohard lsp ACTION --at PATH:LINE:COL [ARG...]` (#26).
// It runs the action through server.RunAction, the server's own code
// path, and prints the LSP result as JSON on stdout. Positions in the
// output are LSP positions (zero-based) in UTF-8 code units.
func lspAction(inv *Invocation) Result {
	name := inv.Args[0]
	if name == "sim" {
		return runLSPSim(inv)
	}
	a, ok := server.LookupAction(name)
	if !ok {
		var names []string
		for _, a := range server.Actions() {
			names = append(names, a.Name)
		}
		names = append(names, "replay")
		slices.Sort(names)
		return inv.usage("unknown action gohard lsp " +
			strconv.Quote(name) + "; expected " +
			strings.Join(names, ", "))
	}
	if !inv.IsSet("at") {
		return inv.usage("gohard lsp " + name + " needs --at " +
			"PATH:LINE:COL")
	}
	file, line, col, ok := parseAt(inv.String("at"))
	if !ok {
		return inv.usage("--at must be PATH:LINE:COL with a line and " +
			"column of at least 1, not " +
			strconv.Quote(inv.String("at")))
	}
	l := inv.LoadConfig()
	if !l.Valid() {
		r := inv.NewReport()
		r.Diagnostics, r.Source = l.Diagnostics, sources(l, nil)
		return Result{Report: r}
	}
	fsys := inv.Env.FS
	wd, err := fsys.Getwd()
	if err != nil {
		return inv.Fail(diag.New(diag.CLIMissingPath, "."))
	}
	path := env.Abs(wd, file)
	text, err := fsys.ReadFile(path)
	if err != nil {
		return inv.Fail(diag.New(diag.CLIMissingPath,
			filepath.ToSlash(file)))
	}
	x := span.NewIndex(text)
	off, _ := x.OffsetOf(line, col)
	p := x.Position(off, span.UTF8)
	resp, err := server.RunAction(inv.Env, serverOptions(), inv.Jobs, wd,
		a, path, text, server.Position{Line: p.Line, Character: p.Col},
		inv.Args[1:])
	switch {
	case errors.Is(err, server.ErrNotProvided):
		return inv.Fail(diag.New(diag.CLINotImplemented, "lsp "+name))
	case err != nil:
		return inv.usage(err.Error())
	case resp.Error != nil:
		panic("gohard lsp " + name + ": " + resp.Error.Message)
	}
	if name == "rename" && !inv.Bool("dry-run") {
		if err := applyEdits(fsys, resp.Result); err != nil {
			return inv.Fail(diag.New(diag.CLIOutputFailed, file,
				errReason(err)))
		}
	}
	return Result{Stdout: append(compactJSON(resp.Result), '\n')}
}

// parseAt splits PATH:LINE:COL from the right, so a path may hold ':'.
func parseAt(s string) (path string, line, col int, ok bool) {
	rest, c, ok1 := cutLast(s)
	path, l, ok2 := cutLast(rest)
	line, err1 := strconv.Atoi(l)
	col, err2 := strconv.Atoi(c)
	if !ok1 || !ok2 || err1 != nil || err2 != nil || path == "" ||
		line < 1 || col < 1 {
		return "", 0, 0, false
	}
	return path, line, col, true
}

func cutLast(s string) (string, string, bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func compactJSON(b []byte) []byte {
	var out bytes.Buffer
	if json.Compact(&out, b) != nil || out.Len() == 0 {
		return []byte("null")
	}
	return out.Bytes()
}

// applyEdits writes a rename's WorkspaceEdit ({"changes":{uri:[...]}},
// UTF-8 positions) to the files, each through a temp file and a rename.
func applyEdits(fsys env.FS, result json.RawMessage) error {
	var we struct {
		Changes map[string][]struct {
			Range   server.Range `json:"range"`
			NewText string       `json:"newText"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(result, &we); err != nil {
		return err
	}
	for _, uri := range slices.Sorted(maps.Keys(we.Changes)) {
		path, ok := server.URIToPath(uri)
		if !ok {
			continue
		}
		text, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		x := span.NewIndex(text)
		edits := we.Changes[uri]
		type sp struct {
			a, b int
			s    string
		}
		var ss []sp
		for _, e := range edits {
			a, _ := x.Offset(span.Position{Line: e.Range.Start.Line,
				Col: e.Range.Start.Character}, span.UTF8)
			b, _ := x.Offset(span.Position{Line: e.Range.End.Line,
				Col: e.Range.End.Character}, span.UTF8)
			ss = append(ss, sp{a, max(a, b), e.NewText})
		}
		slices.SortStableFunc(ss, func(p, q sp) int { return q.a - p.a })
		for _, e := range ss {
			text = slices.Concat(text[:e.a], []byte(e.s), text[e.b:])
		}
		if err := writeOutput(fsys, path, text); err != nil {
			return err
		}
	}
	return nil
}
