package server

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/stdlib"
)

// Text synchronization is incremental only (#25): didChange carries
// ranges in the negotiated position encoding, applied in order. A
// change without a range replaces the whole text, as LSP allows. A
// change for a document that is not open, or whose version does not
// increase, is a stale edit (P6): it is logged and ignored.

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

type versionedID struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

type contentChange struct {
	Range *Range `json:"range"`
	Text  string `json:"text"`
}

func (s *State) didOpen(o *out, params json.RawMessage) {
	var p struct {
		TextDocument textDocumentItem `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		s.log(o, LogWarning, "didOpen: invalid params")
		return
	}
	if s.folder == "" &&
		(s.phase == phaseInitialized || s.phase == phaseRunning) {
		if dir, ok := openedFolder(params); ok {
			s.adoptFolder(o, dir)
		}
	}
	t := p.TextDocument
	d := newDocument(t.URI, s.root, []byte(t.Text))
	d.Version, d.Open, d.LanguageID = t.Version, true, t.LanguageID
	s.open = cloneSet(s.open, d.key, d)
	s.edits++
	s.newSnapshot(o)
}

// openedFolder returns the folder of the file a didOpen opens; ok is
// false for invalid params, for a document that is not a file and for
// a cached bundled std/ieee file. The Driver maps a cached file of its
// own cache root to a bundle URI before the core sees it, but a
// recording keeps the URI the client sent and its replay has another
// cache root, so the core skips a cached file of any root: the live
// session, its replay and SessionFolder then agree (#48).
func openedFolder(params json.RawMessage) (string, bool) {
	var p struct {
		TextDocument textDocumentItem `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", false
	}
	path, ok := URIToPath(p.TextDocument.URI)
	if !ok {
		return "", false
	}
	if _, cached := stdlib.InAnyCache(path); cached {
		return "", false
	}
	return filepath.Dir(path), true
}

// adoptFolder makes dir the workspace folder of a session whose
// client named none (#48): the folder of the first file it opens, as
// the CLI uses the current directory. The project is then loaded as
// for a workspace folder, and requests wait for it.
func (s *State) adoptFolder(o *out, dir string) {
	s.folder, s.root = dir, dir
	if s.phase != phaseRunning {
		return // initialized loads it
	}
	s.loaded = false
	s.registerWatchers(o)
	o.do(LoadConfig{Folder: dir})
}

func (s *State) didChange(o *out, params json.RawMessage) {
	var p struct {
		TextDocument   versionedID     `json:"textDocument"`
		ContentChanges []contentChange `json:"contentChanges"`
	}
	if json.Unmarshal(params, &p) != nil {
		s.log(o, LogWarning, "didChange: invalid params")
		return
	}
	key := docKey(p.TextDocument.URI)
	d, ok := s.open[key]
	switch {
	case !ok:
		s.log(o, LogWarning, "didChange: "+p.TextDocument.URI+
			" is not open; change ignored")
		return
	case p.TextDocument.Version <= d.Version:
		s.log(o, LogWarning, "didChange: stale version "+
			strconv.Itoa(int(p.TextDocument.Version))+" of "+
			p.TextDocument.URI+" (have "+strconv.Itoa(int(d.Version))+
			"); change ignored")
		return
	}
	text, x := d.text, d.index
	for i, c := range p.ContentChanges {
		if c.Range == nil {
			text = []byte(c.Text)
		} else {
			text = applyChange(text, x, *c.Range, s.enc, c.Text)
		}
		if i+1 < len(p.ContentChanges) {
			x = span.NewIndex(text)
		}
	}
	s.open = cloneSet(s.open, key, d.with(text, p.TextDocument.Version))
	s.edits++
	s.newSnapshot(o)
}

func (s *State) didClose(o *out, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	key := docKey(p.TextDocument.URI)
	if _, ok := s.open[key]; !ok {
		return
	}
	s.open = cloneDelete(s.open, key)
	s.edits++
	s.newSnapshot(o)
}

// didSave of gohard.yaml reloads the configuration; a save of a VHDL
// file rereads the workspace, so the disk copy behind a buffer (used
// again once it closes) and files saved without being open stay
// current.
func (s *State) didSave(o *out, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil || s.root == "" {
		return
	}
	path, ok := URIToPath(p.TextDocument.URI)
	switch {
	case ok && path == filepath.Join(s.root, config.FileName):
		s.rereading = s.loaded
		o.do(LoadConfig{Folder: s.folder})
	case ok && s.loaded && isVHDLPath(path) && within(s.root, path):
		s.rereading = true
		o.do(LoadWorkspace{Root: s.root})
	}
	if ok {
		s.didSaveSim(path)
	}
}

// watchGlobs are the files the server asks the client to watch.
var watchGlobs = []string{"**/*.vhd", "**/*.vhdl", "**/" + config.FileName}

// registerWatchers asks a client that supports it to report changes
// made outside the editor (deletes, git checkouts, generators).
func (s *State) registerWatchers(o *out) {
	if !s.watchReg {
		return
	}
	type watcher struct {
		GlobPattern string `json:"globPattern"`
	}
	ws := make([]watcher, len(watchGlobs))
	for i, g := range watchGlobs {
		ws[i] = watcher{g}
	}
	type registration struct {
		ID              string `json:"id"`
		Method          string `json:"method"`
		RegisterOptions any    `json:"registerOptions"`
	}
	params, _ := json.Marshal(struct {
		Registrations []registration `json:"registrations"`
	}{[]registration{{"gohard-watch", "workspace/didChangeWatchedFiles",
		struct {
			Watchers []watcher `json:"watchers"`
		}{ws}}}})
	s.nextOut++
	o.send(&jsonrpc2.Request{ID: jsonrpc2.IntID(s.nextOut),
		Method: "client/registerCapability", Params: params})
}

// didChangeWatchedFiles rereads the workspace (or gohard.yaml, which
// rereads the workspace in turn) when a file changed on disk. Open
// buffers still win over their disk copies; the reread result is an
// event, so replay sees exactly what the live session read.
func (s *State) didChangeWatchedFiles(o *out, params json.RawMessage) {
	var p struct {
		Changes []struct {
			URI string `json:"uri"`
		} `json:"changes"`
	}
	if json.Unmarshal(params, &p) != nil || s.root == "" || !s.loaded {
		return
	}
	cfg, vhdl := false, false
	for _, c := range p.Changes {
		path, ok := URIToPath(c.URI)
		switch {
		case !ok || !within(s.root, path):
		case path == filepath.Join(s.root, config.FileName):
			cfg = true
		case isVHDLPath(path):
			vhdl = true
		}
	}
	switch {
	case cfg:
		s.rereading = true
		o.do(LoadConfig{Folder: s.folder})
	case vhdl:
		s.rereading = true
		o.do(LoadWorkspace{Root: s.root})
	}
}

func isVHDLPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".vhd", ".vhdl":
		return true
	}
	return false
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
