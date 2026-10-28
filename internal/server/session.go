package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"path/filepath"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
)

// SessionFolder is the folder a session works in: the workspace
// folder of the first initialize request among bodies that the server
// accepts (wherever it is: requests before initialize are answered
// with an error and read nothing, but an exit ends the session); when
// that request names none, the folder of the first file opened after
// it and before shutdown or exit, leaving out cached bundled files
// (the folder the server adopts, #48); else dir (the current
// directory).
func SessionFolder(bodies [][]byte, dir string) string {
	var f FolderScan
	for _, b := range bodies {
		if f.Add(b) {
			break
		}
	}
	return f.Folder(dir)
}

// FolderScan applies the rule of SessionFolder one body at a time, so
// a recording looks at each body once, as it arrives. The zero value
// has seen no body.
type FolderScan struct {
	init   bool // an initialize request that names no folder was seen
	known  bool
	folder string
}

// Add scans the next body. It reports whether later bodies can no
// longer change the answer: the folder is found, or the session can
// no longer get one (an exit, or a shutdown after an initialize that
// names none). Bodies added after that are ignored.
func (f *FolderScan) Add(body []byte) (known bool) {
	if f.known {
		return true
	}
	msg, err := jsonrpc2.Decode(body)
	if err != nil {
		return false
	}
	switch m := msg.(type) {
	case *jsonrpc2.Notification:
		switch {
		case m.Method == "exit":
			f.known = true
		case f.init && m.Method == "textDocument/didOpen":
			f.folder, f.known = openedFolder(m.Params)
		}
	case *jsonrpc2.Request:
		switch {
		case !f.init:
			folder, ok := IsInitialize(body)
			f.init = ok
			if ok && folder != "" {
				f.folder, f.known = folder, true
			}
		case m.Method == "shutdown":
			f.known = true
		}
	}
	return f.known
}

// Folder returns the folder found so far, else dir.
func (f *FolderScan) Folder(dir string) string {
	if f.folder != "" {
		return f.folder
	}
	return dir
}

// SessionHeader builds the header of a recorded session (#29): the
// version, the bundled IEEE library (the caller sets the commit and Go
// toolchain, #39), the configuration of the project of folder (standard, seed,
// defines, budgets, models) and the hashes of gohard.yaml and of every
// VHDL file the server reads.
func SessionHeader(e env.Env, folder, version string) (repro.Header,
	error,
) {
	l := config.Load(e.FS, folder)
	c := l.Config
	h := repro.Header{
		Version:  version,
		IEEE:     stdlib.IEEE(),
		Standard: c.Standard,
		Seed:     c.Simulation.Seed,
		Defines:  map[string]string{},
		Budgets: repro.Budgets{
			Time:  c.Simulation.TimeBudget.String(),
			Delta: c.Simulation.DeltaBudget,
			Step:  c.Simulation.StepBudget,
		},
		Models: map[string]string{},
	}
	for _, d := range c.Defines {
		h.Defines[d.Name] = d.Value
	}
	for _, m := range c.Simulation.Models {
		h.Models[m.Unit] = m.Path
	}
	cfg, err := repro.HashConfig(e.FS, l.Root)
	if err != nil {
		return repro.Header{}, err
	}
	h.Config = cfg
	found := workspace.Discover(e.FS, workspace.Options{Root: l.Root,
		Dir: l.Root})
	var paths []string
	for _, f := range found.Files {
		if !f.TooLarge {
			paths = append(paths, f.Path)
		}
	}
	h.Sources, err = repro.HashFiles(e.FS, l.Root, paths)
	return h, err
}

// Replayed is the outcome of Replay.
type Replayed struct {
	// Output is everything the server wrote, framed exactly as on
	// stdout.
	Output []byte
	// Steps[i] is the output written from the moment client input i
	// was queued until the next one was (the last entry is the end of
	// the stream when the session did not exit).
	Steps [][]byte
	// Client is the client message bodies in the order they were
	// queued.
	Client []json.RawMessage
	// Exit is the session's exit code (0 after shutdown and exit).
	Exit int
	// Diagnostics are repro/source-changed (the session cannot be
	// replayed) and repro/version-mismatch (it was replayed anyway).
	Diagnostics []diag.Diagnostic
}

// SessionAnalyzer returns the analyzer a session names (an
// {"$gohard":"analyzer"} line), or "" for the built-in one.
func SessionAnalyzer(s repro.Session) (string, error) {
	_, p, err := parseLines(s.Messages)
	return p.analyzer, err
}

// Replay replays a recorded session against a simulated Env (#29).
// The project files the header names are read from fsys (and must
// still match their hashes), or taken from the session itself when it
// embeds them, copied into a simenv seeded with the header's seed, and
// the client inputs are queued after the same number of core steps as
// in the live session (plain lines once the core is idle). For a given
// session file and project the output is byte-identical on every run
// and every OS.
func Replay(s repro.Session, fsys env.FS, o Options, name string,
) (Replayed, error) {
	var r Replayed
	ins, proj, err := parseLines(s.Messages)
	if err != nil {
		return r, err
	}
	wd, err := fsys.Getwd()
	if err != nil {
		return r, err
	}
	var bodies [][]byte
	for _, in := range ins {
		bodies = append(bodies, in.body)
	}
	folder := SessionFolder(bodies, wd)
	if len(proj.files) > 0 {
		// A self-contained session: its project is the files it
		// embeds, placed under the session folder.
		files := map[string][]byte{}
		for _, p := range slices.Sorted(maps.Keys(proj.files)) {
			files[filepath.Join(folder, filepath.FromSlash(p))] =
				proj.files[p]
		}
		fsys = simenv.New(simenv.Options{Dir: wd, Files: files}).Env().FS
	}
	root, found := config.FindRoot(fsys, folder)
	h := s.Header
	cfgs := slices.IndexFunc(proj.configs, func(c ConfigRead) bool {
		return !c.Missing
	})
	if len(proj.configs) > 0 {
		// The session carries every gohard.yaml the server read: the
		// file on disk is not used, so it is not checked either, and
		// the root is where the live server found gohard.yaml.
		if cfgs >= 0 {
			root = filepath.Dir(filepath.Join(folder,
				filepath.FromSlash(proj.configs[cfgs].Path)))
			found = config.FoundConfig
		}
		h.Config, err = repro.HashConfig(fsys, root)
		if err != nil {
			h.Config = s.Header.Config
		}
	}
	chk, err := repro.Verify(fsys, root, h, o.Version)
	if err != nil {
		return r, err
	}
	r.Diagnostics = chk.Diagnostics(name)
	if !chk.OK() {
		return r, nil
	}
	files := map[string][]byte{}
	want := append([]repro.File(nil), s.Header.Sources...)
	if s.Header.Config != nil && len(proj.configs) == 0 {
		want = append(want, *s.Header.Config)
	}
	for _, f := range want {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		data, err := fsys.ReadFile(p)
		if err != nil {
			return r, err
		}
		files[p] = data
	}
	if found == config.FoundGit {
		files[filepath.Join(root, ".git")] = nil
	}
	// The bundled std/ieee files a location names are written to a
	// cache in the simulated file system (#33).
	cache := filepath.VolumeName(folder) + string(filepath.Separator) +
		"gohard-cache"
	sim := simenv.New(simenv.Options{Seed: s.Header.Seed, Dir: folder,
		Files: files, Vars: map[string]string{"XDG_CACHE_HOME": cache}})
	var buf bytes.Buffer
	d := NewDriver(o, sim.Env(), 1, func(m Send) error {
		buf.Write(jsonrpc2.AppendFrame(nil, m.Body()))
		return nil
	})
	if len(proj.configs) > 0 {
		d.loadConfig = feedConfig(sim.Env().FS, folder, proj.configs)
	}
	if len(proj.rereads) > 0 {
		d.loadFiles = feedFiles(sim.Env(), proj.rereads)
	}
	mark := -1
	step := func() {
		if mark >= 0 {
			r.Steps = append(r.Steps, bytes.Clone(buf.Bytes()[mark:]))
		}
		mark = buf.Len()
	}
	for _, in := range ins {
		if in.timed {
			err = d.RunTo(in.step)
		} else {
			err = d.Run()
		}
		if err != nil {
			return r, err
		}
		step()
		if in.reject != nil {
			d.PushRejected(*in.reject)
			continue
		}
		if in.body == nil {
			d.PushClosed()
			continue
		}
		r.Client = append(r.Client, in.body)
		d.Push(in.body)
	}
	if err := d.Run(); err != nil {
		return r, err
	}
	step()
	if _, ok := d.Exited(); !ok {
		if err := d.Close(); err != nil {
			return r, err
		}
		step()
	}
	r.Exit, _ = d.Exited()
	r.Output = buf.Bytes()
	return r, nil
}

// feedConfig returns a Driver.loadConfig that, before each load, puts
// the next recorded gohard.yaml read in place (or removes the file
// when the live server found none), so every load sees what the live
// server saw. Loads past the recorded ones read fsys as it is.
func feedConfig(fsys env.FS, folder string, cfgs []ConfigRead,
) func(LoadConfig) config.Loaded {
	last := ""
	return func(e LoadConfig) config.Loaded {
		if len(cfgs) > 0 {
			c := cfgs[0]
			cfgs = cfgs[1:]
			if last != "" {
				_ = fsys.Remove(last)
				last = ""
			}
			if !c.Missing {
				p := filepath.Join(folder, filepath.FromSlash(c.Path))
				if fsys.MkdirAll(filepath.Dir(p), 0o755) == nil &&
					fsys.WriteFile(p, c.Data, 0o644) == nil {
					last = p
				}
			}
		}
		return config.Load(fsys, e.Folder)
	}
}

// feedFiles returns a Driver.loadFiles that leaves the first load
// alone (the header's files) and, before each later one, makes the
// project files under the root exactly the next recorded reread, so
// every reread sees what the live server read.
func feedFiles(e env.Env, reads []WorkspaceRead,
) func(LoadWorkspace) WorkspaceLoaded {
	first := true
	var placed []string
	return func(l LoadWorkspace) WorkspaceLoaded {
		if !first && len(reads) > 0 {
			r := reads[0]
			reads = reads[1:]
			for _, p := range found(e, l.Root) {
				_ = e.FS.Remove(p)
			}
			for _, p := range placed {
				_ = e.FS.Remove(p)
			}
			placed = placed[:0]
			for i, rel := range r.Paths {
				p := filepath.Join(l.Root, filepath.FromSlash(rel))
				if e.FS.MkdirAll(filepath.Dir(p), 0o755) == nil &&
					e.FS.WriteFile(p, r.Data[i], 0o644) == nil {
					placed = append(placed, p)
				}
			}
		}
		first = false
		return LoadFiles(e, 1, l.Root)
	}
}

// found lists the project files a load of root would read.
func found(e env.Env, root string) []string {
	f := workspace.Discover(e.FS, workspace.Options{Root: root, Dir: root})
	names := make([]string, len(f.Files))
	for i, x := range f.Files {
		names[i] = x.Name
	}
	return names
}

// Mismatch compares two replays and returns the one-based number of
// the first client message after which their output differs, or 0.
func Mismatch(a, b Replayed) int {
	n := max(len(a.Steps), len(b.Steps))
	for i := range n {
		if i >= len(a.Steps) || i >= len(b.Steps) ||
			!bytes.Equal(a.Steps[i], b.Steps[i]) {
			return i + 1
		}
	}
	return 0
}

// Frames splits framed server output into message bodies.
func Frames(out []byte) ([]json.RawMessage, error) {
	rd := jsonrpc2.NewReader(bytes.NewReader(out), 0)
	var msgs []json.RawMessage
	for {
		body, err := rd.ReadFrame()
		if errors.Is(err, io.EOF) {
			return msgs, nil
		}
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, body)
	}
}
